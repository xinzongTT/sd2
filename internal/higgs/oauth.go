package higgs

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xinzo/higgsfield-proxy/internal/account"
)

const (
	clerkTokenURL = "https://clerk.higgsfield.ai/oauth/token"
	// Official CLI OAuth client id (from higgsfield auth login authorize URL)
	clerkClientID = "sRGCQJvvJkPrrtRj"
	// refresh this many seconds before expires_at
	refreshSkewSec = 300
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
