package account

import (
	"fmt"
	"sync"
	"time"
)

type Pool struct {
	store      *Store
	minCredits float64
	mu         sync.Mutex
	rr         int
	inflight   map[string]int
	perAccount int
}

func NewPool(store *Store, minCredits float64, perAccount int) *Pool {
	if perAccount <= 0 {
		perAccount = 1
	}
	return &Pool{
		store:      store,
		minCredits: minCredits,
		inflight:   map[string]int{},
		perAccount: perAccount,
	}
}

func (p *Pool) List() ([]*Account, error) {
	return p.store.List()
}

func (p *Pool) Save(a *Account) error {
	return p.store.Save(a)
}

func (p *Pool) Get(id string) (*Account, error) {
	return p.store.Get(id)
}

// Select picks a healthy account with capacity using round-robin.
func (p *Pool) Select() (*Account, error) {
	accounts, err := p.store.List()
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts configured")
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(accounts)
	for i := 0; i < n; i++ {
		idx := (p.rr + i) % n
		a := accounts[idx]
		if !a.Healthy(p.minCredits, now) {
			continue
		}
		if p.inflight[a.ID] >= p.perAccount {
			continue
		}
		p.rr = (idx + 1) % n
		p.inflight[a.ID]++
		// return a copy
		cp := *a
		return &cp, nil
	}
	return nil, fmt.Errorf("no available account")
}

func (p *Pool) Release(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inflight[id] > 0 {
		p.inflight[id]--
	}
}

func (p *Pool) MarkError(id string, errMsg string, cooldown time.Duration, disable bool) {
	a, err := p.store.Get(id)
	if err != nil {
		return
	}
	a.LastError = errMsg
	if disable {
		a.Disabled = true
	}
	if cooldown > 0 {
		t := time.Now().Add(cooldown)
		a.CooldownUntil = &t
	}
	_ = p.store.Save(a)
}

func (p *Pool) UpdateCredits(id string, credits float64, plan, email string) {
	a, err := p.store.Get(id)
	if err != nil {
		return
	}
	a.Credits = credits
	if plan != "" {
		a.Plan = plan
	}
	if email != "" {
		a.Email = email
	}
	a.LastError = ""
	_ = p.store.Save(a)
}

// UpdateTokens persists refreshed OAuth tokens for an account.
func (p *Pool) UpdateTokens(id, access, refresh string, expiresAt int64, tokenType, scope string) {
	a, err := p.store.Get(id)
	if err != nil {
		return
	}
	if access != "" {
		a.AccessToken = access
	}
	if refresh != "" {
		a.RefreshToken = refresh
	}
	if expiresAt > 0 {
		a.ExpiresAt = expiresAt
	}
	if tokenType != "" {
		a.TokenType = tokenType
	}
	if scope != "" {
		a.Scope = scope
	}
	a.LastError = ""
	a.CooldownUntil = nil
	_ = p.store.Save(a)
}

func (p *Pool) SetDisabled(id string, disabled bool) error {
	a, err := p.store.Get(id)
	if err != nil {
		return err
	}
	a.Disabled = disabled
	if !disabled {
		a.CooldownUntil = nil
		a.LastError = ""
	}
	return p.store.Save(a)
}
