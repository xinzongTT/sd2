package account

import (
	"time"
)

type Account struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    int64     `json:"expires_at,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	WorkspaceID  string    `json:"workspace_id"`
	Plan         string    `json:"plan,omitempty"`
	Credits      float64   `json:"credits"`
	Disabled     bool      `json:"disabled"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ConfigDir    string    `json:"config_dir,omitempty"`
}

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
	return true
}

func (a *Account) Public() map[string]any {
	var cd any
	if a.CooldownUntil != nil {
		cd = a.CooldownUntil.UTC().Format(time.RFC3339)
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
	}
}
