package higgs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xinzo/higgsfield-proxy/internal/account"
)

type CLI struct {
	Path    string
	DataDir string
}

type JobResult struct {
	ID           string         `json:"id"`
	JobType      string         `json:"job_type"`
	DisplayName  string         `json:"display_name"`
	Status       string         `json:"status"`
	ResultURL    string         `json:"result_url"`
	MinResultURL string         `json:"min_result_url"`
	CreatedAt    string         `json:"created_at"`
	Params       map[string]any `json:"params"`
	Raw          json.RawMessage
}

type ModelInfo struct {
	JobType     string `json:"job_type"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
}

type AccountStatus struct {
	Credits float64 `json:"credits"`
	Email   string  `json:"email"`
	Plan    string  `json:"subscription_plan_type"`
}

type Workspace struct {
	ID      string  `json:"id"`
	Name    *string `json:"name"`
	Plan    string  `json:"plan_type"`
	Credits float64 `json:"credits"`
}

func (c *CLI) bin() string {
	if c.Path != "" {
		return c.Path
	}
	return "higgsfield"
}

func (c *CLI) prepareAccountDir(a *account.Account) (string, error) {
	dir := a.ConfigDir
	if dir == "" {
		dir = filepath.Join(c.DataDir, "cli-homes", a.ID)
	}
	cfgDir := filepath.Join(dir, ".config", "higgsfield")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return "", err
	}
	cred := map[string]any{
		"auth_version":  2,
		"access_token":  a.AccessToken,
		"refresh_token": a.RefreshToken,
		"expires_at":    a.ExpiresAt,
		"token_type":    firstNonEmpty(a.TokenType, "bearer"),
		"scope":         a.Scope,
	}
	b, _ := json.MarshalIndent(cred, "", "  ")
	if err := os.WriteFile(filepath.Join(cfgDir, "credentials.json"), b, 0o600); err != nil {
		return "", err
	}
	if a.WorkspaceID != "" {
		cfg := map[string]string{"workspace_id": a.WorkspaceID}
		cb, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), cb, 0o600); err != nil {
			return "", err
		}
	}
	a.ConfigDir = dir
	return dir, nil
}

func (c *CLI) run(a *account.Account, args ...string) ([]byte, error) {
	home, err := c.prepareAccountDir(a)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(c.bin(), args...)
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"HOME="+home,
	)
	// Keep USERPROFILE for Windows tools but prefer XDG for CLI
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("higgsfield %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

func (c *CLI) AccountStatus(a *account.Account) (*AccountStatus, error) {
	out, err := c.run(a, "account", "status", "--json")
	if err != nil {
		return nil, err
	}
	var st AccountStatus
	if err := json.Unmarshal(out, &st); err != nil {
		return nil, fmt.Errorf("parse account status: %w", err)
	}
	return &st, nil
}

func (c *CLI) WorkspaceList(a *account.Account) ([]Workspace, error) {
	out, err := c.run(a, "workspace", "list", "--json")
	if err != nil {
		return nil, err
	}
	var list []Workspace
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (c *CLI) ListModels(a *account.Account, kind string) ([]ModelInfo, error) {
	args := []string{"model", "list", "--json"}
	switch kind {
	case "image":
		args = append(args, "--image")
	case "video":
		args = append(args, "--video")
	case "audio":
		args = append(args, "--audio")
	case "text":
		args = append(args, "--text")
	}
	out, err := c.run(a, args...)
	if err != nil {
		return nil, err
	}
	var models []ModelInfo
	if err := json.Unmarshal(out, &models); err != nil {
		return nil, err
	}
	return models, nil
}

// CreateOpts describes a generation request for the official CLI.
type CreateOpts struct {
	JobType    string
	Params     map[string]string   // single-value flags: --prompt x
	MultiFlags map[string][]string // multi-value media flags: --image-references a --image-references b
}

// Create runs: higgsfield generate create <jobType> --param value ... --json
func (c *CLI) Create(a *account.Account, jobType string, params map[string]string) (string, error) {
	return c.CreateOpts(a, CreateOpts{JobType: jobType, Params: params})
}

// CreateOpts runs create with single and repeated media flags.
func (c *CLI) CreateOpts(a *account.Account, opts CreateOpts) (string, error) {
	jobType := opts.JobType
	if jobType == "" {
		return "", fmt.Errorf("job type required")
	}
	args := []string{"generate", "create", jobType}
	for k, v := range opts.Params {
		if strings.TrimSpace(v) == "" {
			continue
		}
		args = append(args, "--"+normalizeFlag(k), v)
	}
	for k, vals := range opts.MultiFlags {
		flag := normalizeFlag(k)
		for _, v := range vals {
			if strings.TrimSpace(v) == "" {
				continue
			}
			args = append(args, "--"+flag, v)
		}
	}
	args = append(args, "--json")
	out, err := c.run(a, args...)
	if err != nil {
		return "", err
	}
	out = bytes.TrimSpace(out)
	var arr []string
	if err := json.Unmarshal(out, &arr); err == nil && len(arr) > 0 {
		return arr[0], nil
	}
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err == nil {
		if id, ok := obj["id"].(string); ok && id != "" {
			return id, nil
		}
		if id, ok := obj["job_id"].(string); ok && id != "" {
			return id, nil
		}
	}
	s := strings.Trim(string(out), "\" \n\r\t")
	if s != "" && !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
		return s, nil
	}
	return "", fmt.Errorf("unexpected create response: %s", string(out))
}

func normalizeFlag(k string) string {
	k = strings.TrimSpace(k)
	k = strings.TrimPrefix(k, "--")
	// CLI accepts both underscore and hyphen; prefer hyphen for media flags
	switch k {
	case "image_references":
		return "image-references"
	case "video_references":
		return "video-references"
	case "audio_references":
		return "audio-references"
	case "start_image":
		return "start-image"
	case "end_image":
		return "end-image"
	case "aspect_ratio":
		return "aspect-ratio"
	default:
		return k
	}
}

func (c *CLI) Get(a *account.Account, jobID string) (*JobResult, error) {
	out, err := c.run(a, "generate", "get", jobID, "--json")
	if err != nil {
		return nil, err
	}
	var jr JobResult
	if err := json.Unmarshal(out, &jr); err != nil {
		return nil, err
	}
	jr.Raw = append(json.RawMessage(nil), out...)
	return &jr, nil
}

func (c *CLI) Wait(a *account.Account, jobID string, timeout time.Duration, interval time.Duration) (*JobResult, error) {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	args := []string{
		"generate", "wait", jobID,
		"--timeout", formatDuration(timeout),
		"--interval", formatDuration(interval),
		"--quiet",
		"--json",
	}
	out, err := c.run(a, args...)
	if err != nil {
		return nil, err
	}
	var jr JobResult
	if err := json.Unmarshal(out, &jr); err != nil {
		return nil, err
	}
	jr.Raw = append(json.RawMessage(nil), out...)
	return &jr, nil
}

// AuthLogin runs interactive browser login into an isolated home and returns credentials.
func (c *CLI) AuthLogin(homeDir string) (*account.Account, error) {
	if err := os.MkdirAll(filepath.Join(homeDir, ".config", "higgsfield"), 0o700); err != nil {
		return nil, err
	}
	cmd := exec.Command(c.bin(), "auth", "login")
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+filepath.Join(homeDir, ".config"),
		"HOME="+homeDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("auth login failed: %w", err)
	}
	return LoadAccountFromCLIHome(homeDir)
}

func LoadAccountFromCLIHome(homeDir string) (*account.Account, error) {
	cfgDir := filepath.Join(homeDir, ".config", "higgsfield")
	credPath := filepath.Join(cfgDir, "credentials.json")
	b, err := os.ReadFile(credPath)
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	var cred struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    int64  `json:"expires_at"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(b, &cred); err != nil {
		return nil, err
	}
	if cred.AccessToken == "" {
		return nil, fmt.Errorf("empty access_token in %s", credPath)
	}
	ws := ""
	if cb, err := os.ReadFile(filepath.Join(cfgDir, "config.json")); err == nil {
		var cfg struct {
			WorkspaceID string `json:"workspace_id"`
		}
		_ = json.Unmarshal(cb, &cfg)
		ws = cfg.WorkspaceID
	}
	a := &account.Account{
		ID:           shortID(cred.AccessToken),
		AccessToken:  cred.AccessToken,
		RefreshToken: cred.RefreshToken,
		ExpiresAt:    cred.ExpiresAt,
		TokenType:    cred.TokenType,
		Scope:        cred.Scope,
		WorkspaceID:  ws,
		ConfigDir:    homeDir,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	return a, nil
}

func LoadAccountFromUserCLI() (*account.Account, string, error) {
	// default ~/.config/higgsfield
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	// On Windows CLI uses %USERPROFILE%\.config\higgsfield when XDG unset
	userHome := home
	a, err := LoadAccountFromCLIHome(userHome)
	if err != nil {
		return nil, "", err
	}
	return a, userHome, nil
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d%d == 0 || d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return d.String()
}

func firstNonEmpty(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func shortID(token string) string {
	// use last 12 chars of token for stable-ish id without full secret in filename
	t := token
	if len(t) > 12 {
		t = t[len(t)-12:]
	}
	return "acc_" + sanitize(t)
}

func sanitize(s string) string {
	r := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
	return r
}
