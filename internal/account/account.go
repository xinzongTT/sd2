package account

import (
	"time"
)

type Account struct {
	ID            string     `json:"id"`
	Email         string     `json:"email"`
	AccessToken   string     `json:"access_token"`
	RefreshToken  string     `json:"refresh_token,omitempty"`
	ExpiresAt     int64      `json:"expires_at,omitempty"`
	TokenType     string     `json:"token_type,omitempty"`
	Scope         string     `json:"scope,omitempty"`
	WorkspaceID   string     `json:"workspace_id"`
	Plan          string     `json:"plan,omitempty"`
	Credits       float64    `json:"credits"`
	Disabled      bool       `json:"disabled"`
	CooldownUntil  *time.Time `json:"cooldown_until,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ConfigDir     string     `json:"config_dir,omitempty"`
	// AuthStatus is runtime-only (not always persisted): valid|expired|refresh_failed|disabled|cooldown|no_token
	AuthStatus string `json:"auth_status,omitempty"`
}

// TokenExpired reports access token past expires_at (with 60s skew).
func (a *Account) TokenExpired(now time.Time) bool {
	if a == nil {
		return true
	}
	if a.AccessToken == "" {
		return true
	}
	if a.ExpiresAt <= 0 {
		// unknown expiry — treat as not expired by timestamp alone
		return false
	}
	return now.Unix() >= (a.ExpiresAt - 60)
}

// CanRefresh reports whether a refresh_token exists.
func (a *Account) CanRefresh() bool {
	return a != nil && a.RefreshToken != ""
}

// Healthy is used for job scheduling: must have usable token (or refreshable near-expiry is OK only if refresh works later).
// For selection we require non-expired access token OR presence of refresh_token (will be refreshed on use).
// If both expired and no refresh, not healthy. If last_error indicates refresh/auth failure, not healthy.
func (a *Account) Healthy(minCredits float64, now time.Time) bool {
	if a == nil || a.Disabled {
		return false
	}
	if a.AccessToken == "" {
		return false
	}
	if a.CooldownUntil != nil && a.CooldownUntil.After(now) {
		return false
	}
	if a.Credits > 0 && a.Credits < minCredits {
		return false
	}
	// hard auth failure sticky error
	if isAuthFailureStatus(a.AuthStatus) || isAuthFailureError(a.LastError) {
		// still allow if token is currently valid by time
		if a.TokenExpired(now) {
			return false
		}
	}
	// expired and cannot refresh
	if a.TokenExpired(now) && !a.CanRefresh() {
		return false
	}
	// expired with refresh token: optimistic (refresh on use); if AuthStatus=refresh_failed, block
	if a.TokenExpired(now) && a.AuthStatus == "refresh_failed" {
		return false
	}
	return true
}

// StatusLabel for UI: 可用 / 已过期 / 刷新失败 / 冷却中 / 禁用 / 无Token
func (a *Account) StatusLabel(now time.Time) string {
	if a == nil {
		return "unknown"
	}
	if a.Disabled {
		return "disabled"
	}
	if a.CooldownUntil != nil && a.CooldownUntil.After(now) {
		return "cooldown"
	}
	if a.AccessToken == "" {
		return "no_token"
	}
	if a.AuthStatus == "refresh_failed" || isAuthFailureError(a.LastError) {
		if a.TokenExpired(now) {
			return "refresh_failed"
		}
	}
	if a.TokenExpired(now) {
		if a.CanRefresh() && a.AuthStatus != "refresh_failed" {
			return "expired" // can try refresh
		}
		return "expired"
	}
	if a.AuthStatus == "valid" || a.AuthStatus == "" {
		return "valid"
	}
	if a.AuthStatus != "" {
		return a.AuthStatus
	}
	return "valid"
}

func (a *Account) Public() map[string]any {
	now := time.Now()
	var cd any
	if a.CooldownUntil != nil {
		cd = a.CooldownUntil.UTC().Format(time.RFC3339)
	}
	status := a.StatusLabel(now)
	ready := a.Healthy(0, now) && status == "valid"
	// if expired but refreshable and not marked refresh_failed, not "ready" for display as 可用
	if status != "valid" {
		ready = false
	}
	var exp any
	if a.ExpiresAt > 0 {
		exp = time.Unix(a.ExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"id":             a.ID,
		"email":          a.Email,
		"workspace_id":   a.WorkspaceID,
		"plan":           a.Plan,
		"credits":        a.Credits,
		"disabled":       a.Disabled,
		"cooldown_until": cd,
		"last_error":     a.LastError,
		"created_at":     a.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":     a.UpdatedAt.UTC().Format(time.RFC3339),
		"has_token":      a.AccessToken != "",
		"has_refresh":    a.CanRefresh(),
		"expires_at":     exp,
		"token_expired":  a.TokenExpired(now),
		"auth_status":    status,
		"ready":          ready,
	}
}

func isAuthFailureStatus(s string) bool {
	switch s {
	case "expired", "refresh_failed", "no_token", "disabled":
		return true
	default:
		return false
	}
}

func isAuthFailureError(msg string) bool {
	if msg == "" {
		return false
	}
	// lowercase check without importing strings in hot path many times - use simple contains via map
	m := toLower(msg)
	return contains(m, "not authenticated") ||
		contains(m, "session expired") ||
		contains(m, "invalid_grant") ||
		contains(m, "refresh failed") ||
		contains(m, "unauthorized") ||
		contains(m, "no refresh_token")
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func contains(s, sub string) bool {
	if len(sub) == 0 || len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
