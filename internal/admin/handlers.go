package admin

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xinzo/higgsfield-proxy/internal/account"
	"github.com/xinzo/higgsfield-proxy/internal/apikey"
	"github.com/xinzo/higgsfield-proxy/internal/config"
	"github.com/xinzo/higgsfield-proxy/internal/higgs"
	"github.com/xinzo/higgsfield-proxy/internal/reqlog"
	"github.com/xinzo/higgsfield-proxy/internal/session"
)

type Handler struct {
	Cfg     *config.Config
	Pool    *account.Pool
	CLI     *higgs.CLI
	Keys    *apikey.Store
	Logs    *reqlog.Store
	Session *session.Manager
}

func New(cfg *config.Config, pool *account.Pool, cli *higgs.CLI, keys *apikey.Store, logs *reqlog.Store, sess *session.Manager) *Handler {
	return &Handler{Cfg: cfg, Pool: pool, CLI: cli, Keys: keys, Logs: logs, Session: sess}
}

func (h *Handler) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) Accounts(w http.ResponseWriter, r *http.Request) {
	list, err := h.Pool.List()
	if err != nil {
		h.writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		h.probeAccountAuth(a)
		out = append(out, a.Public())
	}
	h.writeJSON(w, 200, map[string]any{"accounts": out})
}

// probeAccountAuth refreshes if needed and verifies session; updates AuthStatus on account + disk.
func (h *Handler) probeAccountAuth(a *account.Account) {
	if a == nil || a.Disabled {
		if a != nil {
			a.AuthStatus = "disabled"
		}
		return
	}
	if a.AccessToken == "" {
		a.AuthStatus = "no_token"
		return
	}
	now := time.Now()
	// try refresh if expired / near expiry
	changed, err := h.CLI.EnsureAuth(a)
	if err != nil {
		if a.TokenExpired(now) || higgs.IsAuthError(err) {
			a.AuthStatus = "refresh_failed"
			a.LastError = err.Error()
			_ = h.Pool.Save(a)
		}
		return
	}
	if changed {
		h.Pool.UpdateTokens(a.ID, a.AccessToken, a.RefreshToken, a.ExpiresAt, a.TokenType, a.Scope)
	}
	// live check
	st, err := h.CLI.AccountStatus(a)
	if err != nil {
		if higgs.IsAuthError(err) {
			// one more forced refresh
			if rerr := higgs.RefreshAccount(a); rerr != nil {
				a.AuthStatus = "refresh_failed"
				a.LastError = rerr.Error()
				_ = h.Pool.Save(a)
				return
			}
			h.Pool.UpdateTokens(a.ID, a.AccessToken, a.RefreshToken, a.ExpiresAt, a.TokenType, a.Scope)
			st, err = h.CLI.AccountStatus(a)
			if err != nil {
				a.AuthStatus = "refresh_failed"
				a.LastError = err.Error()
				_ = h.Pool.Save(a)
				return
			}
		} else {
			// network blip: if token not expired, keep valid
			if a.TokenExpired(time.Now()) {
				a.AuthStatus = "expired"
			} else {
				a.AuthStatus = "valid"
			}
			return
		}
	}
	a.AuthStatus = "valid"
	a.LastError = ""
	if st != nil {
		a.Credits = st.Credits
		if st.Email != "" {
			a.Email = st.Email
		}
		if st.Plan != "" {
			a.Plan = st.Plan
		}
		h.Pool.UpdateCredits(a.ID, st.Credits, st.Plan, st.Email)
	}
	// clear sticky auth error on disk
	if saved, err := h.Pool.Get(a.ID); err == nil {
		saved.AuthStatus = "valid"
		saved.LastError = ""
		saved.Credits = a.Credits
		_ = h.Pool.Save(saved)
	}
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	list, _ := h.Pool.List()
	healthy := 0
	now := time.Now()
	for _, a := range list {
		// light local check (Accounts page does live probe)
		if a.StatusLabel(now) == "valid" && a.Healthy(h.Cfg.MinCredits, now) {
			healthy++
		}
	}
	st := map[string]any{
		"ok":             true,
		"accounts_total": len(list),
		"accounts_ready": healthy,
		"addr":           h.Cfg.Addr(),
		"keys_total":     0,
	}
	if h.Keys != nil {
		st["keys_total"] = len(h.Keys.List())
	}
	if h.Logs != nil {
		st["log_stats"] = h.Logs.Stats()
	}
	h.writeJSON(w, 200, st)
}

func (h *Handler) ImportCLI(w http.ResponseWriter, r *http.Request) {
	a, userHome, err := higgs.LoadAccountFromUserCLI()
	if err != nil {
		h.writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	id := accountIDFromToken(a.AccessToken)
	home := filepath.Join(h.Cfg.DataDir, "cli-homes", id)
	_ = copyCLIHome(userHome, home)
	a.ID = id
	a.ConfigDir = home
	if a.WorkspaceID == "" {
		if wss, err := h.CLI.WorkspaceList(a); err == nil && len(wss) > 0 {
			a.WorkspaceID = wss[0].ID
			a.Plan = wss[0].Plan
			a.Credits = wss[0].Credits
		}
	}
	if st, err := h.CLI.AccountStatus(a); err == nil {
		a.Credits = st.Credits
		a.Email = st.Email
		a.Plan = st.Plan
	}
	if err := h.Pool.Save(a); err != nil {
		h.writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	h.writeJSON(w, 200, map[string]any{"ok": true, "account": a.Public()})
}

// Login kept for compatibility — server has no browser; use OAuthStart/Complete.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, 400, map[string]any{
		"error":   "server cannot open a browser; use OAuth start + paste callback URL",
		"hint":    "POST /admin/auth/start then POST /admin/auth/complete with callback_url",
		"ui_flow": "click OAuth 加号 → open link → paste redirect URL",
	})
}

// OAuthStart returns authorize_url for the admin to open on their own PC.
func (h *Handler) OAuthStart(w http.ResponseWriter, r *http.Request) {
	p, err := higgs.StartBrowserOAuth()
	if err != nil {
		h.writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	h.writeJSON(w, 200, map[string]any{
		"ok":            true,
		"authorize_url": p.AuthorizeURL,
		"state":         p.State,
		"instructions":  "1) Open authorize_url in your browser 2) Login 3) Browser may show connection refused on localhost:8765 — copy the full URL from address bar 4) Paste into complete",
	})
}

// OAuthComplete exchanges pasted callback URL for tokens and saves account.
func (h *Handler) OAuthComplete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CallbackURL string `json:"callback_url"`
		Code        string `json:"code"`
		State       string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.writeJSON(w, 400, map[string]string{"error": "invalid json"})
		return
	}
	a, err := higgs.CompleteBrowserOAuth(body.CallbackURL, body.Code, body.State)
	if err != nil {
		h.writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if err := h.finalizeAndSaveAccount(a); err != nil {
		h.writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	h.writeJSON(w, 200, map[string]any{"ok": true, "account": a.Public()})
}

// ImportCredentials accepts pasted credentials.json (+ optional workspace_id).
func (h *Handler) ImportCredentials(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    int64  `json:"expires_at"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		WorkspaceID  string `json:"workspace_id"`
		// or raw JSON string of credentials file
		Raw string `json:"raw"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.writeJSON(w, 400, map[string]string{"error": "invalid json"})
		return
	}
	if body.Raw != "" {
		var raw map[string]any
		if err := json.Unmarshal([]byte(body.Raw), &raw); err != nil {
			h.writeJSON(w, 400, map[string]string{"error": "invalid raw credentials json"})
			return
		}
		if v, ok := raw["access_token"].(string); ok {
			body.AccessToken = v
		}
		if v, ok := raw["refresh_token"].(string); ok {
			body.RefreshToken = v
		}
		if v, ok := raw["token_type"].(string); ok {
			body.TokenType = v
		}
		if v, ok := raw["scope"].(string); ok {
			body.Scope = v
		}
		switch x := raw["expires_at"].(type) {
		case float64:
			body.ExpiresAt = int64(x)
		}
	}
	if strings.TrimSpace(body.AccessToken) == "" {
		h.writeJSON(w, 400, map[string]string{"error": "access_token required"})
		return
	}
	a := &account.Account{
		ID:           accountIDFromToken(body.AccessToken),
		AccessToken:  body.AccessToken,
		RefreshToken: body.RefreshToken,
		ExpiresAt:    body.ExpiresAt,
		TokenType:    firstNonEmptyStr(body.TokenType, "bearer"),
		Scope:        body.Scope,
		WorkspaceID:  body.WorkspaceID,
		AuthStatus:   "valid",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := h.finalizeAndSaveAccount(a); err != nil {
		h.writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	h.writeJSON(w, 200, map[string]any{"ok": true, "account": a.Public()})
}

func (h *Handler) finalizeAndSaveAccount(a *account.Account) error {
	if a.ID == "" {
		a.ID = accountIDFromToken(a.AccessToken)
	}
	a.ConfigDir = filepath.Join(h.Cfg.DataDir, "cli-homes", a.ID)
	_ = os.MkdirAll(filepath.Join(a.ConfigDir, ".config", "higgsfield"), 0o700)
	// write creds via CLI helper path
	if _, err := h.CLI.EnsureAuth(a); err != nil {
		// EnsureAuth may no-op; still prepare dir by status call
	}
	// force write credentials
	type prep interface {
		// not exported; call AccountStatus which prepares dir
	}
	if a.WorkspaceID == "" {
		if wss, err := h.CLI.WorkspaceList(a); err == nil && len(wss) > 0 {
			a.WorkspaceID = wss[0].ID
			a.Plan = wss[0].Plan
			a.Credits = wss[0].Credits
		}
	}
	if a.WorkspaceID != "" {
		// ensure workspace selected in config.json via prepareAccountDir side effect
		_, _ = h.CLI.WorkspaceList(a)
	}
	if st, err := h.CLI.AccountStatus(a); err == nil {
		a.Credits = st.Credits
		a.Email = st.Email
		a.Plan = st.Plan
		a.AuthStatus = "valid"
		a.LastError = ""
	} else {
		a.AuthStatus = "valid" // tokens just issued; status may fail transiently
		a.LastError = err.Error()
	}
	a.UpdatedAt = time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = a.UpdatedAt
	}
	return h.Pool.Save(a)
}

func firstNonEmptyStr(v, def string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func (h *Handler) SetDisabled(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin/accounts/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		h.writeJSON(w, 400, map[string]string{"error": "bad path"})
		return
	}
	id, action := parts[0], parts[1]
	var disabled bool
	switch action {
	case "disable":
		disabled = true
	case "enable":
		disabled = false
	default:
		h.writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	if err := h.Pool.SetDisabled(id, disabled); err != nil {
		h.writeJSON(w, 404, map[string]string{"error": err.Error()})
		return
	}
	a, _ := h.Pool.Get(id)
	h.writeJSON(w, 200, map[string]any{"ok": true, "account": a.Public()})
}

// --- Web console password login ---

func (h *Handler) ConsoleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if h.Session == nil || !h.Session.CheckPassword(body.Password) {
		h.writeJSON(w, 401, map[string]string{"error": "invalid password"})
		return
	}
	if _, err := h.Session.Create(w); err != nil {
		h.writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	h.writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *Handler) ConsoleLogout(w http.ResponseWriter, r *http.Request) {
	if h.Session != nil {
		h.Session.Clear(w, r)
	}
	h.writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *Handler) ConsoleMe(w http.ResponseWriter, r *http.Request) {
	ok := h.Session == nil || !h.Session.Enabled() || h.Session.Valid(r)
	h.writeJSON(w, 200, map[string]any{"ok": ok, "auth_required": h.Session != nil && h.Session.Enabled()})
}

// --- API keys ---

func (h *Handler) KeysList(w http.ResponseWriter, r *http.Request) {
	if h.Keys == nil {
		h.writeJSON(w, 200, map[string]any{"keys": []any{}})
		return
	}
	list := h.Keys.List()
	out := make([]map[string]any, 0, len(list))
	for _, k := range list {
		out = append(out, map[string]any{
			"id":         k.ID,
			"name":       k.Name,
			"key":        k.Key,
			"enabled":    k.Enabled,
			"note":       k.Note,
			"created_at": k.CreatedAt,
			"updated_at": k.UpdatedAt,
			"last_used":  k.LastUsed,
		})
	}
	h.writeJSON(w, 200, map[string]any{"keys": out})
}

func (h *Handler) KeysCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	k, err := h.Keys.Create(body.Name, body.Note)
	if err != nil {
		h.writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	h.writeJSON(w, 200, map[string]any{"ok": true, "key": k})
}

func (h *Handler) KeysAction(w http.ResponseWriter, r *http.Request) {
	// /admin/keys/{id}/enable|disable|delete
	path := strings.TrimPrefix(r.URL.Path, "/admin/keys/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		h.writeJSON(w, 400, map[string]string{"error": "bad path"})
		return
	}
	id, action := parts[0], parts[1]
	var err error
	switch action {
	case "enable":
		err = h.Keys.SetEnabled(id, true)
	case "disable":
		err = h.Keys.SetEnabled(id, false)
	case "delete":
		err = h.Keys.Delete(id)
	default:
		h.writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		h.writeJSON(w, 404, map[string]string{"error": err.Error()})
		return
	}
	h.writeJSON(w, 200, map[string]any{"ok": true})
}

// --- Logs ---

func (h *Handler) LogsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		var n int
		ok := true
		for _, c := range v {
			if c < '0' || c > '9' {
				ok = false
				break
			}
			n = n*10 + int(c-'0')
		}
		if ok && n > 0 {
			limit = n
		}
	}
	list := []reqlog.Entry{}
	var stats any
	if h.Logs != nil {
		list = h.Logs.List(limit, q)
		stats = h.Logs.Stats()
	}
	h.writeJSON(w, 200, map[string]any{"logs": list, "stats": stats})
}

func accountIDFromToken(token string) string {
	sum := sha1.Sum([]byte(token))
	return "acc_" + hex.EncodeToString(sum[:8])
}

func copyCLIHome(srcHome, dstHome string) error {
	src := filepath.Join(srcHome, ".config", "higgsfield")
	dst := filepath.Join(dstHome, ".config", "higgsfield")
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"credentials.json", "config.json"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dst, name), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}
