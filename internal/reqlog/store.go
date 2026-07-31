package reqlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	APIKey    string    `json:"api_key"` // masked
	KeyName   string    `json:"key_name,omitempty"`
	Model     string    `json:"model,omitempty"`
	Prompt    string    `json:"prompt,omitempty"`
	Request   string    `json:"request,omitempty"` // truncated JSON body summary
	Status    int       `json:"status"`
	LatencyMS int64     `json:"latency_ms"`
	JobID     string    `json:"job_id,omitempty"`
	ResultURL string    `json:"result_url,omitempty"`
	Error     string    `json:"error,omitempty"`
	IP        string    `json:"ip,omitempty"`
	Kind      string    `json:"kind,omitempty"` // image|video|file|other
}

type Store struct {
	path    string
	mu      sync.Mutex
	entries []Entry
	max     int
}

func NewStore(dataDir string, max int) (*Store, error) {
	if max <= 0 {
		max = 2000
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dataDir, "request_logs.json"), max: max}
	_ = s.load()
	return s, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.entries = []Entry{}
			return nil
		}
		return err
	}
	if len(b) == 0 {
		s.entries = []Entry{}
		return nil
	}
	return json.Unmarshal(b, &s.entries)
}

func (s *Store) saveLocked() error {
	b, err := json.Marshal(s.entries)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Add(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.ID == "" {
		e.ID = time.Now().UTC().Format("20060102T150405.000000000")
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	e.APIKey = MaskKey(e.APIKey)
	e.Prompt = truncate(e.Prompt, 2000)
	e.Request = truncate(e.Request, 4000)
	e.Error = truncate(e.Error, 1000)
	// newest first
	s.entries = append([]Entry{e}, s.entries...)
	if len(s.entries) > s.max {
		s.entries = s.entries[:s.max]
	}
	_ = s.saveLocked()
}

func (s *Store) List(limit int, q string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.entries) {
		limit = len(s.entries)
		if limit > 200 {
			limit = 200
		}
	}
	q = strings.ToLower(strings.TrimSpace(q))
	out := make([]Entry, 0, limit)
	for _, e := range s.entries {
		if q != "" {
			blob := strings.ToLower(e.Model + " " + e.Prompt + " " + e.Path + " " + e.KeyName + " " + e.ResultURL + " " + e.Error)
			if !strings.Contains(blob, q) {
				continue
			}
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Store) Stats() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	byModel := map[string]int{}
	byPath := map[string]int{}
	ok, fail := 0, 0
	for _, e := range s.entries {
		if e.Model != "" {
			byModel[e.Model]++
		}
		byPath[e.Path]++
		if e.Status >= 200 && e.Status < 400 && e.Error == "" {
			ok++
		} else {
			fail++
		}
	}
	type kv struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	models := make([]kv, 0, len(byModel))
	for k, v := range byModel {
		models = append(models, kv{k, v})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Count > models[j].Count })
	paths := make([]kv, 0, len(byPath))
	for k, v := range byPath {
		paths = append(paths, kv{k, v})
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].Count > paths[j].Count })
	return map[string]any{
		"total":    len(s.entries),
		"success":  ok,
		"failed":   fail,
		"by_model": models,
		"by_path":  paths,
	}
}

func MaskKey(k string) string {
	k = strings.TrimSpace(k)
	if len(k) <= 10 {
		if k == "" {
			return ""
		}
		return "***"
	}
	return k[:6] + "..." + k[len(k)-4:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
