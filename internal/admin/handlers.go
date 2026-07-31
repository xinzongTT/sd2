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

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	id := "acc_" + time.Now().Format("20060102_150405")
	home := filepath.Join(h.Cfg.DataDir, "cli-homes", id)
	_ = os.MkdirAll(filepath.Join(home, ".config", "higgsfield"), 0o700)
	a, err := h.CLI.AuthLogin(home)
	if err != nil {
		h.writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
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
