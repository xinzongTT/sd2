package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	dir string
	mu  sync.RWMutex
}

func NewStore(dataDir string) (*Store, error) {
	dir := filepath.Join(dataDir, "accounts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) Save(a *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" {
		return fmt.Errorf("account id required")
	}
	a.UpdatedAt = time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = a.UpdatedAt
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(a.ID), b, 0o600)
}

func (s *Store) Get(id string) (*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var a Account
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) List() ([]*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := make([]*Account, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var a Account
		if err := json.Unmarshal(b, &a); err != nil {
			continue
		}
		out = append(out, &a)
	}
	return out, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.Remove(s.path(id))
}
