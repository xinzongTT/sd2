package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Host               string            `yaml:"host"`
	Port               int               `yaml:"port"`
	APIKeys            []string          `yaml:"api_keys"`
	AdminAPIKey        string            `yaml:"admin_api_key"`
	AdminPassword      string            `yaml:"admin_password"` // web console login
	DataDir            string            `yaml:"data_dir"`
	CLIPath            string            `yaml:"cli_path"`
	MinCredits         float64           `yaml:"min_credits"`
	AccountConcurrency int               `yaml:"account_concurrency"`
	GlobalConcurrency  int               `yaml:"global_concurrency"`
	ImageTimeoutSec    int               `yaml:"image_timeout_sec"`
	VideoTimeoutSec    int               `yaml:"video_timeout_sec"`
	PollIntervalMS     int               `yaml:"poll_interval_ms"`
	DefaultImageModel  string            `yaml:"default_image_model"`
	DefaultVideoModel  string            `yaml:"default_video_model"`
	Aliases            map[string]string `yaml:"aliases"`
	LogMaxEntries      int               `yaml:"log_max_entries"`
}

func Default() *Config {
	return &Config{
		Host:               "127.0.0.1",
		Port:               8317,
		APIKeys:            []string{"sk-local-change-me"},
		AdminAPIKey:        "sk-admin-change-me",
		AdminPassword:      "admin123",
		DataDir:            "./data",
		CLIPath:            "higgsfield",
		MinCredits:         0.1,
		AccountConcurrency: 1,
		GlobalConcurrency:  4,
		ImageTimeoutSec:    180,
		VideoTimeoutSec:    1200,
		PollIntervalMS:     3000,
		DefaultImageModel:  "text2image_soul_v2",
		DefaultVideoModel:  "seedance_2_0",
		LogMaxEntries:      2000,
		Aliases: map[string]string{
			"soul-v2": "text2image_soul_v2",
			"soul":    "text2image_soul_v2",
			// seedance_2_0_fast is virtual (handled in ResolveModelSpec)
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
		} else if err := yaml.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}
	applyEnv(cfg)
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 8317
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.CLIPath == "" {
		cfg.CLIPath = "higgsfield"
	}
	if cfg.Aliases == nil {
		cfg.Aliases = map[string]string{}
	}
	if cfg.AdminPassword == "" {
		cfg.AdminPassword = "admin123"
	}
	if cfg.LogMaxEntries <= 0 {
		cfg.LogMaxEntries = 2000
	}
	abs, err := filepath.Abs(cfg.DataDir)
	if err == nil {
		cfg.DataDir = abs
	}
	return cfg, nil
}

// applyEnv overrides config from environment (server deploy friendly).
// HF_PROXY_HOST, HF_PROXY_PORT, HF_PROXY_API_KEYS (comma-separated),
// HF_PROXY_ADMIN_KEY, HF_PROXY_DATA_DIR, HF_PROXY_CLI_PATH
func applyEnv(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv("HF_PROXY_HOST")); v != "" {
		cfg.Host = v
	}
	if v := strings.TrimSpace(os.Getenv("HF_PROXY_PORT")); v != "" {
		var p int
		if _, err := fmt.Sscanf(v, "%d", &p); err == nil && p > 0 {
			cfg.Port = p
		}
	}
	if v := strings.TrimSpace(os.Getenv("HF_PROXY_API_KEYS")); v != "" {
		parts := strings.Split(v, ",")
		keys := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				keys = append(keys, p)
			}
		}
		if len(keys) > 0 {
			cfg.APIKeys = keys
		}
	}
	if v := strings.TrimSpace(os.Getenv("HF_PROXY_ADMIN_KEY")); v != "" {
		cfg.AdminAPIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("HF_PROXY_ADMIN_PASSWORD")); v != "" {
		cfg.AdminPassword = v
	}
	if v := strings.TrimSpace(os.Getenv("HF_PROXY_DATA_DIR")); v != "" {
		cfg.DataDir = v
	}
	if v := strings.TrimSpace(os.Getenv("HF_PROXY_CLI_PATH")); v != "" {
		cfg.CLIPath = v
	}
}

func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// ModelSpec is a resolved upstream job type plus fixed extra flags (e.g. mode=fast).
type ModelSpec struct {
	JobType     string
	ExtraParams map[string]string
	DisplayAs   string // virtual model id shown to clients, if any
}

func (c *Config) ResolveModel(name string) string {
	return c.ResolveModelSpec(name).JobType
}

func (c *Config) ResolveModelSpec(name string) ModelSpec {
	n := strings.TrimSpace(name)
	if n == "" {
		return ModelSpec{JobType: c.DefaultImageModel}
	}
	// config aliases first (simple rename only)
	if v, ok := c.Aliases[n]; ok && v != "" {
		n = v
	}
	key := strings.ToLower(strings.ReplaceAll(n, "-", "_"))
	switch key {
	case "seedance_2_0_fast", "seedance_2.0_fast", "seedance2_0_fast", "seedance_fast":
		return ModelSpec{
			JobType:     "seedance_2_0",
			ExtraParams: map[string]string{"mode": "fast"},
			DisplayAs:   "seedance_2_0_fast",
		}
	default:
		return ModelSpec{JobType: n}
	}
}

// VirtualModels are client-facing model ids not returned by upstream list.
func (c *Config) VirtualModels() []string {
	return []string{"seedance_2_0_fast"}
}

func (c *Config) ValidAPIKey(key string) bool {
	for _, k := range c.APIKeys {
		if k != "" && k == key {
			return true
		}
	}
	return false
}

func (c *Config) ValidAdminKey(key string) bool {
	if c.AdminAPIKey != "" && key == c.AdminAPIKey {
		return true
	}
	return c.ValidAPIKey(key)
}
