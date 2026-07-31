# Higgsfield OpenAI Proxy Implementation Plan

> **For agentic workers:** Implement task-by-task. Steps use checkbox syntax.

**Goal:** Local Go multi-account proxy exposing OpenAI-compatible image/video APIs over Higgsfield Plus OAuth accounts.

**Architecture:** Standalone Go HTTP server. OAuth + account files on disk. Generation primarily via official `higgsfield` CLI subprocess with per-account config dirs (avoids Cloudflare blocks on raw POST). Direct HTTP used for account/workspace/status where available.

**Tech Stack:** Go 1.22+, net/http, encoding/json, yaml config, official `higgsfield` CLI on PATH.

## Global Constraints

- Folder: `genmai/higgsfield-proxy/`
- Bind default `127.0.0.1:8317`
- Client auth: `Authorization: Bearer <local_api_key>`
- Account add: built-in OAuth (CLI-compatible PKCE) or import from existing CLI credentials
- OpenAI surfaces: `/v1/models`, `/v1/images/generations`, `/v1/videos/generations`, `/v1/jobs/{id}`
- Multi-account round-robin + failover on credits/auth/errors
- Do not log full tokens

---

### Task 1: Project skeleton + config + health

**Files:**
- Create: `go.mod`, `cmd/server/main.go`, `internal/config/config.go`, `internal/server/server.go`, `config.example.yaml`, `README.md`

- [x] Init module `github.com/xinzo/higgsfield-proxy`
- [x] Load yaml config (host/port/api_keys/data_dir)
- [x] `GET /healthz` → 200
- [x] API key middleware

### Task 2: Account store + pool

**Files:**
- Create: `internal/account/account.go`, `internal/account/pool.go`, `internal/account/store.go`

- [x] Persist accounts under `data/accounts/*.json`
- [x] Round-robin Select(); Cooldown/Disable on errors
- [x] Import from `~/.config/higgsfield` CLI credentials

### Task 3: CLI executor (generate)

**Files:**
- Create: `internal/higgs/cli.go`, `internal/higgs/models.go`

- [x] Per-account temp/config dir with credentials.json + config.json
- [x] `Create(jobType, params)` → job id via `higgsfield generate create ... --json`
- [x] `Wait(jobID)` / `Get(jobID)` via CLI
- [x] `ListModels(image|video)`

### Task 4: OpenAI-compatible handlers

**Files:**
- Create: `internal/openaiapi/handlers.go`, `internal/openaiapi/types.go`, `internal/openaiapi/map.go`

- [x] `GET /v1/models`
- [x] `POST /v1/images/generations` (sync wait)
- [x] `POST /v1/videos/generations` (sync wait + optional async job)
- [x] `GET /v1/jobs/{id}`

### Task 5: Admin OAuth / accounts

**Files:**
- Create: `internal/auth/oauth.go`, `internal/admin/handlers.go`

- [x] `POST /admin/auth/login` starts browser OAuth (shell `higgsfield auth login` into isolated config dir, or native PKCE)
- [x] `GET /admin/accounts`
- [x] `POST /admin/accounts/import-cli` import current machine CLI login
- [x] enable/disable account

### Task 6: End-to-end verify

- [x] Import current Plus account
- [x] Image generation via curl OpenAI format
- [x] Multi-account selection unit test
