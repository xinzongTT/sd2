package higgs

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/xinzo/higgsfield-proxy/internal/account"
)

const (
	clerkAuthorizeURL = "https://clerk.higgsfield.ai/oauth/authorize"
	clerkTokenURL     = "https://clerk.higgsfield.ai/oauth/token"
	// Official CLI OAuth client id (from higgsfield auth login authorize URL)
	clerkClientID = "sRGCQJvvJkPrrtRj"
	// CLI default callback — after browser login, address bar shows this URL even if page fails to load
	oauthRedirectURI = "http://localhost:8765/callback"
	oauthScope       = "email profile offline_access user:org:read"
	// refresh this many seconds before expires_at
	refreshSkewSec = 300
)

// PendingOAuth holds PKCE state for browser login (user opens URL on their PC).
type PendingOAuth struct {
	State         string
	CodeVerifier  string
	CodeChallenge string
	AuthorizeURL  string
	CreatedAt     time.Time
}

var (
	pendingMu sync.Mutex
	pending   = map[string]*PendingOAuth{}
)

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// NeedsRefresh reports whether access token is missing/near expiry.
func NeedsRefresh(a *account.Account) bool {
	if a == nil || a.RefreshToken == "" {
		return false
	}
	if a.AccessToken == "" {
		return true
	}
	if a.ExpiresAt <= 0 {
		return false
	}
	return time.Now().Unix() >= (a.ExpiresAt - refreshSkewSec)
}

// RefreshAccount exchanges refresh_token for new access/refresh tokens via Clerk.
func RefreshAccount(a *account.Account) error {
	if a == nil {
		return fmt.Errorf("nil account")
	}
	if strings.TrimSpace(a.RefreshToken) == "" {
		return fmt.Errorf("no refresh_token")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", a.RefreshToken)
	form.Set("client_id", clerkClientID)

	req, err := http.NewRequest(http.MethodPost, clerkTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return fmt.Errorf("refresh parse: %w body=%s", err, truncate(string(body), 200))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || tr.AccessToken == "" {
		msg := tr.Error
		if tr.ErrorDesc != "" {
			msg = tr.Error + ": " + tr.ErrorDesc
		}
		if msg == "" {
			msg = string(body)
		}
		return fmt.Errorf("refresh failed (%d): %s", resp.StatusCode, msg)
	}

	a.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		a.RefreshToken = tr.RefreshToken
	}
	if tr.TokenType != "" {
		a.TokenType = tr.TokenType
	}
	if tr.Scope != "" {
		a.Scope = tr.Scope
	}
	if tr.ExpiresIn > 0 {
		a.ExpiresAt = time.Now().Unix() + tr.ExpiresIn
	} else {
		a.ExpiresAt = time.Now().Unix() + 86000
	}
	a.UpdatedAt = time.Now().UTC()
	a.LastError = ""
	return nil
}

// StartBrowserOAuth creates a PKCE login session for remote/server use.
// User opens AuthorizeURL on any machine; after login, paste the redirected
// localhost callback URL into CompleteBrowserOAuth.
func StartBrowserOAuth() (*PendingOAuth, error) {
	verifier, err := randomURLString(32)
	if err != nil {
		return nil, err
	}
	state, err := randomURLString(24)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	q := url.Values{}
	q.Set("client_id", clerkClientID)
	q.Set("redirect_uri", oauthRedirectURI)
	q.Set("response_type", "code")
	q.Set("scope", oauthScope)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")

	p := &PendingOAuth{
		State:         state,
		CodeVerifier:  verifier,
		CodeChallenge: challenge,
		AuthorizeURL:  clerkAuthorizeURL + "?" + q.Encode(),
		CreatedAt:     time.Now(),
	}
	pendingMu.Lock()
	// drop stale
	for k, v := range pending {
		if time.Since(v.CreatedAt) > 15*time.Minute {
			delete(pending, k)
		}
	}
	pending[state] = p
	pendingMu.Unlock()
	return p, nil
}

// CompleteBrowserOAuth exchanges the callback URL (or code+state) for tokens.
func CompleteBrowserOAuth(callbackURL, code, state string) (*account.Account, error) {
	if callbackURL != "" {
		u, err := url.Parse(strings.TrimSpace(callbackURL))
		if err != nil {
			return nil, fmt.Errorf("invalid callback url: %w", err)
		}
		q := u.Query()
		if code == "" {
			code = q.Get("code")
		}
		if state == "" {
			state = q.Get("state")
		}
		if q.Get("error") != "" {
			return nil, fmt.Errorf("oauth error: %s %s", q.Get("error"), q.Get("error_description"))
		}
	}
	code = strings.TrimSpace(code)
	state = strings.TrimSpace(state)
	if code == "" || state == "" {
		return nil, fmt.Errorf("code and state required (paste full callback URL)")
	}

	pendingMu.Lock()
	p := pending[state]
	if p != nil {
		delete(pending, state)
	}
	pendingMu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("unknown or expired state; click OAuth again")
	}
	if time.Since(p.CreatedAt) > 15*time.Minute {
		return nil, fmt.Errorf("oauth session expired; start again")
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", oauthRedirectURI)
	form.Set("client_id", clerkClientID)
	form.Set("code_verifier", p.CodeVerifier)

	req, err := http.NewRequest(http.MethodPost, clerkTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || tr.AccessToken == "" {
		msg := tr.Error
		if tr.ErrorDesc != "" {
			msg = tr.Error + ": " + tr.ErrorDesc
		}
		if msg == "" {
			msg = string(body)
		}
		return nil, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, msg)
	}

	a := &account.Account{
		ID:           accountIDFromAccess(tr.AccessToken),
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    firstNonEmpty(tr.TokenType, "bearer"),
		Scope:        tr.Scope,
		AuthStatus:   "valid",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if tr.ExpiresIn > 0 {
		a.ExpiresAt = time.Now().Unix() + tr.ExpiresIn
	} else {
		a.ExpiresAt = time.Now().Unix() + 86000
	}
	return a, nil
}

func accountIDFromAccess(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "acc_" + hex.EncodeToString(sum[:8])
}

func randomURLString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func isAuthError(err error) bool {
	return IsAuthError(err)
}

// IsAuthError reports upstream auth/session failures that may be fixed by refresh.
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not authenticated") ||
		strings.Contains(msg, "session expired") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "invalid_grant") ||
		strings.Contains(msg, "401")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
