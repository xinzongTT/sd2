package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/xinzo/higgsfield-proxy/internal/admin"
	"github.com/xinzo/higgsfield-proxy/internal/apikey"
	"github.com/xinzo/higgsfield-proxy/internal/config"
	"github.com/xinzo/higgsfield-proxy/internal/openaiapi"
	"github.com/xinzo/higgsfield-proxy/internal/reqlog"
	"github.com/xinzo/higgsfield-proxy/internal/session"
	"github.com/xinzo/higgsfield-proxy/internal/webui"
)

type Server struct {
	Cfg     *config.Config
	OpenAI  *openaiapi.Handler
	Admin   *admin.Handler
	Keys    *apikey.Store
	Logs    *reqlog.Store
	Session *session.Manager
	mux     *http.ServeMux
}

func New(cfg *config.Config, oai *openaiapi.Handler, adm *admin.Handler, keys *apikey.Store, logs *reqlog.Store, sess *session.Manager) *Server {
	s := &Server{Cfg: cfg, OpenAI: oai, Admin: adm, Keys: keys, Logs: logs, Session: sess, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	// console auth (no session required to login)
	s.mux.HandleFunc("/admin/console/login", method("POST", s.Admin.ConsoleLogin))
	s.mux.HandleFunc("/admin/console/logout", method("POST", s.Admin.ConsoleLogout))
	s.mux.HandleFunc("/admin/console/me", s.Admin.ConsoleMe)

	// OpenAI compatible + canvas (API key + request log)
	s.mux.HandleFunc("/v1/models", s.withAPIKeyLogged(s.OpenAI.Models, "other"))
	s.mux.HandleFunc("/v1/images/generations", s.withAPIKeyLogged(method("POST", s.OpenAI.ImagesGenerations), "image"))
	// infinite-canvas OpenAI video: POST /v1/videos + GET /v1/videos/{id}
	// Seedance models are rewritten by canvas to /contents/generations/tasks
	// also keep POST /v1/videos/generations
	s.mux.HandleFunc("/v1/videos", s.withAPIKeyLogged(s.OpenAI.Videos, "video"))
	s.mux.HandleFunc("/v1/videos/", s.withAPIKeyLogged(s.OpenAI.Videos, "video"))
	s.mux.HandleFunc("/v1/videos/generations", s.withAPIKeyLogged(method("POST", s.OpenAI.VideosGenerations), "video"))
	s.mux.HandleFunc("/v1/contents/generations/tasks", s.withAPIKeyLogged(s.OpenAI.Videos, "video"))
	s.mux.HandleFunc("/v1/contents/generations/tasks/", s.withAPIKeyLogged(s.OpenAI.Videos, "video"))
	// without /v1 prefix (if baseUrl already ends with /v1 stripped incorrectly)
	s.mux.HandleFunc("/contents/generations/tasks", s.withAPIKeyLogged(s.OpenAI.Videos, "video"))
	s.mux.HandleFunc("/contents/generations/tasks/", s.withAPIKeyLogged(s.OpenAI.Videos, "video"))
	s.mux.HandleFunc("/v1/generate", s.withAPIKeyLogged(method("POST", s.OpenAI.Generate), "auto"))
	s.mux.HandleFunc("/v1/files", s.withAPIKeyLogged(method("POST", s.OpenAI.FilesUpload), "file"))
	s.mux.HandleFunc("/v1/jobs/", s.withAPIKeyLogged(s.OpenAI.JobGet, "job"))

	// Admin API (session cookie OR admin api key)
	s.mux.HandleFunc("/admin/status", s.withAdminAuth(s.Admin.Status))
	s.mux.HandleFunc("/admin/accounts", s.withAdminAuth(s.Admin.Accounts))
	s.mux.HandleFunc("/admin/accounts/import-cli", s.withAdminAuth(method("POST", s.Admin.ImportCLI)))
	s.mux.HandleFunc("/admin/accounts/import-credentials", s.withAdminAuth(method("POST", s.Admin.ImportCredentials)))
	s.mux.HandleFunc("/admin/auth/login", s.withAdminAuth(method("POST", s.Admin.Login)))
	s.mux.HandleFunc("/admin/auth/start", s.withAdminAuth(method("POST", s.Admin.OAuthStart)))
	s.mux.HandleFunc("/admin/auth/complete", s.withAdminAuth(method("POST", s.Admin.OAuthComplete)))
	s.mux.HandleFunc("/admin/accounts/", s.withAdminAuth(s.Admin.SetDisabled))
	s.mux.HandleFunc("/admin/keys", s.withAdminAuth(s.keysRouter))
	s.mux.HandleFunc("/admin/keys/", s.withAdminAuth(s.Admin.KeysAction))
	s.mux.HandleFunc("/admin/logs", s.withAdminAuth(s.Admin.LogsList))

	// Web UI with session gate
	s.mux.Handle("/", s.withUIAuth(webui.Handler()))
}

func (s *Server) keysRouter(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.Admin.KeysList(w, r)
		return
	}
	if r.Method == http.MethodPost {
		s.Admin.KeysCreate(w, r)
		return
	}
	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
}

func (s *Server) Handler() http.Handler {
	return s.withCORS(s.mux)
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) validAPIKey(key string) bool {
	if s.Cfg.ValidAPIKey(key) {
		return true
	}
	if s.Keys != nil && s.Keys.Valid(key) {
		return true
	}
	return false
}

func (s *Server) withAPIKeyLogged(next http.HandlerFunc, kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := extractKey(r)
		if !s.validAPIKey(key) {
			http.Error(w, `{"error":{"message":"invalid api key","type":"invalid_request_error","code":"invalid_api_key"}}`, http.StatusUnauthorized)
			return
		}
		if s.Keys != nil {
			s.Keys.Touch(key)
		}

		start := time.Now()
		ct := r.Header.Get("Content-Type")
		isMultipart := strings.Contains(strings.ToLower(ct), "multipart/")
		var bodyBytes []byte
		// Never truncate multipart: canvas uploads ref images/videos can be tens of MB.
		// Truncating caused: invalid multipart: unexpected EOF
		if r.Body != nil && r.Method == http.MethodPost && !isMultipart {
			// JSON bodies are small; cap at 8MB for logging/replay
			bodyBytes, _ = io.ReadAll(io.LimitReader(r.Body, 8<<20))
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		rw := &captureWriter{ResponseWriter: w, status: 200}
		next(rw, r)

		if s.Logs == nil {
			return
		}
		model, prompt, reqSummary := "", "", ""
		if isMultipart {
			reqSummary = "multipart/form-data (body not buffered for logging)"
			// try read model/prompt from form if handler already parsed
			if r.MultipartForm != nil {
				if v := r.FormValue("model"); v != "" {
					model = v
				}
				if v := r.FormValue("prompt"); v != "" {
					prompt = v
				}
				reqSummary = "multipart fields model=" + model
			}
		} else {
			model, prompt, reqSummary = parseBodySummary(bodyBytes)
		}
		resultURL, jobID, errMsg := parseResponseSummary(rw.buf.Bytes())
		if kind == "auto" {
			if strings.Contains(strings.ToLower(model), "seedance") || strings.Contains(strings.ToLower(model), "video") {
				kind = "video"
			} else {
				kind = "image"
			}
		}
		keyName := ""
		if s.Keys != nil {
			keyName = s.Keys.NameOf(key)
		}
		s.Logs.Add(reqlog.Entry{
			Method:    r.Method,
			Path:      r.URL.Path,
			APIKey:    key,
			KeyName:   keyName,
			Model:     model,
			Prompt:    prompt,
			Request:   reqSummary,
			Status:    rw.status,
			LatencyMS: time.Since(start).Milliseconds(),
			JobID:     jobID,
			ResultURL: resultURL,
			Error:     errMsg,
			IP:        clientIP(r),
			Kind:      kind,
		})
	}
}

func (s *Server) withAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// session cookie OR admin/api key
		if s.Session != nil && s.Session.Valid(r) {
			next(w, r)
			return
		}
		key := extractKey(r)
		if s.Cfg.ValidAdminKey(key) || s.validAPIKey(key) {
			next(w, r)
			return
		}
		http.Error(w, `{"error":{"message":"unauthorized"}}`, http.StatusUnauthorized)
	}
}

func (s *Server) withUIAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// public assets / login page
		if path == "/login" || path == "/login.html" || strings.HasPrefix(path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		if s.Session != nil && s.Session.Enabled() && !s.Session.Valid(r) {
			// API-ish under / still blocked; redirect browser to login
			if strings.HasPrefix(path, "/admin/") || strings.HasPrefix(path, "/v1/") {
				http.Error(w, `{"error":"login required"}`, http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login.html", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type captureWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (c *captureWriter) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if c.buf.Len() < 64<<10 {
		_, _ = c.buf.Write(b)
	}
	return c.ResponseWriter.Write(b)
}

func parseBodySummary(b []byte) (model, prompt, summary string) {
	if len(b) == 0 {
		return "", "", ""
	}
	summary = string(b)
	if len(summary) > 4000 {
		summary = summary[:4000] + "..."
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return "", "", summary
	}
	if v, ok := m["model"].(string); ok {
		model = v
	}
	if v, ok := m["prompt"].(string); ok {
		prompt = v
	}
	return model, prompt, summary
}

func parseResponseSummary(b []byte) (resultURL, jobID, errMsg string) {
	if len(b) == 0 {
		return "", "", ""
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return "", "", ""
	}
	if e, ok := m["error"].(map[string]any); ok {
		if msg, ok := e["message"].(string); ok {
			errMsg = msg
		}
	}
	if id, ok := m["id"].(string); ok {
		jobID = id
	}
	if u, ok := m["url"].(string); ok {
		resultURL = u
	}
	if data, ok := m["data"].([]any); ok && len(data) > 0 {
		if first, ok := data[0].(map[string]any); ok {
			if u, ok := first["url"].(string); ok {
				resultURL = u
			}
		}
	}
	return resultURL, jobID, errMsg
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func extractKey(r *http.Request) string {
	if k := bearer(r.Header.Get("Authorization")); k != "" {
		return k
	}
	if k := strings.TrimSpace(r.Header.Get("X-API-Key")); k != "" {
		return k
	}
	return strings.TrimSpace(r.URL.Query().Get("api_key"))
}

func bearer(h string) string {
	h = strings.TrimSpace(h)
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return h
}

func method(m string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != m {
			http.Error(w, `{"error":{"message":"method not allowed"}}`, http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}
