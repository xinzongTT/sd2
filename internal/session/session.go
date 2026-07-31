package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const CookieName = "hf_proxy_session"

type Manager struct {
	password string
	ttl      time.Duration
	mu       sync.Mutex
	tokens   map[string]time.Time
}

func New(password string, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Manager{
		password: password,
		ttl:      ttl,
		tokens:   map[string]time.Time{},
	}
}

func (m *Manager) Enabled() bool {
	return m != nil && m.password != ""
}

func (m *Manager) CheckPassword(pw string) bool {
	if !m.Enabled() {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(pw), []byte(m.password)) == 1
}

func (m *Manager) Create(w http.ResponseWriter) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	m.mu.Lock()
	m.tokens[tok] = time.Now().Add(m.ttl)
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.ttl.Seconds()),
	})
	return tok, nil
}

func (m *Manager) Clear(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		m.mu.Lock()
		delete(m.tokens, c.Value)
		m.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func (m *Manager) Valid(r *http.Request) bool {
	if !m.Enabled() {
		return true
	}
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.tokens[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(m.tokens, c.Value)
		return false
	}
	// sliding
	m.tokens[c.Value] = time.Now().Add(m.ttl)
	return true
}
