package apikey

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Key struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	Enabled   bool      `json:"enabled"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	LastUsed  time.Time `json:"last_used,omitempty"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	keys []Key
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dataDir, "api_keys.json")}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.keys = []Key{}
			return nil
		}
		return err
	}
	if len(b) == 0 {
		s.keys = []Key{}
		return nil
	}
	return json.Unmarshal(b, &s.keys)
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.keys, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) List() []Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Key, len(s.keys))
	copy(out, s.keys)
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (s *Store) Create(name, note string) (Key, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return Key{}, err
	}
	k := Key{
		ID:        hex.EncodeToString(raw[:8]),
		Name:      name,
		Key:       "sk-" + hex.EncodeToString(raw),
		Enabled:   true,
		Note:      strings.TrimSpace(note),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, k)
	if err := s.saveLocked(); err != nil {
		return Key{}, err
	}
	return k, nil
}

func (s *Store) SetEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.keys {
		if s.keys[i].ID == id {
			s.keys[i].Enabled = enabled
			s.keys[i].UpdatedAt = time.Now().UTC()
			return s.saveLocked()
		}
	}
	return fmt.Errorf("key not found")
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.keys[:0]
	found := false
	for _, k := range s.keys {
		if k.ID == id {
			found = true
			continue
		}
		n = append(n, k)
	}
	if !found {
		return fmt.Errorf("key not found")
	}
	s.keys = n
	return s.saveLocked()
}

func (s *Store) Valid(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.keys {
		if k.Enabled && k.Key == key {
			return true
		}
	}
	return false
}

func (s *Store) Touch(key string) {
	key = strings.TrimSpace(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.keys {
		if s.keys[i].Key == key {
			s.keys[i].LastUsed = time.Now().UTC()
			_ = s.saveLocked()
			return
		}
	}
}

func (s *Store) NameOf(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.keys {
		if k.Key == key {
			return k.Name
		}
	}
	return ""
}

// SeedFromConfig imports static config keys once if store empty.
func (s *Store) SeedFromConfig(keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.keys) > 0 {
		return nil
	}
	now := time.Now().UTC()
	for i, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		s.keys = append(s.keys, Key{
			ID:        fmt.Sprintf("cfg%d", i+1),
			Name:      fmt.Sprintf("config-%d", i+1),
			Key:       k,
			Enabled:   true,
			Note:      "imported from config.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	if len(s.keys) == 0 {
		return nil
	}
	return s.saveLocked()
}
