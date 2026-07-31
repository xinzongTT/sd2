# Higgsfield OpenAI-Compatible Proxy Design

Date: 2026-07-28  
Status: Ready for user review  
Approach: A — standalone Go service inspired by CLIProxyAPI (not a fork)

## 1. Goal

Build a local multi-account relay that:

1. Lets users add Higgsfield Plus/subscription accounts via built-in OAuth (same model as `higgsfield auth login`).
2. Exposes OpenAI-compatible image/video HTTP APIs for clients/SDKs.
3. Round-robins across accounts with failover on low credits / errors.
4. Lives in its own folder: `genmai/higgsfield-proxy/`.

Out of scope for v1:

- Full CLIProxyAPI feature parity (Claude/Gemini/Codex chat, streaming LLM tools).
- Public multi-tenant commercial billing UI.
- Pasting stale OAuth callback URLs as a primary account-add method (PKCE code is one-time and bound to verifier).

## 2. Background

### 2.1 Two Higgsfield surfaces

| Surface | Auth | Use |
|---|---|---|
| Main product / CLI (`higgsfield.ai`, Clerk OAuth) | Access token (`oat_...`) + workspace | Subscription credits (e.g. Plus 1210) |
| Cloud API (`cloud.higgsfield.ai` / `platform.higgsfield.ai`) | `Key api_key:api_secret` | Separate credit pool; may be empty |

This proxy targets the **CLI / main product OAuth path**, because that is where the user’s active Plus credits live.

### 2.2 Reference architecture

CLIProxyAPI pattern to reuse conceptually:

- Local HTTP API with OpenAI-compatible routes
- OAuth login flow managed by the proxy
- Account credential files on disk
- Multi-account load balancing + failover

Differences from CLIProxyAPI:

- Upstream work is **async media jobs**, not chat completions
- Response is media URLs, not token streams
- Primary OpenAI surfaces are images/videos, not chat

## 3. Success criteria

v1 is done when:

1. `POST /admin/auth/login` opens browser OAuth and stores a new account on success.
2. `GET /v1/models` lists mapped image + video models.
3. `POST /v1/images/generations` with a local API key returns `{ data: [{ url }] }`.
4. `POST /v1/videos/generations` returns a completed video URL (or job handle on long wait).
5. With 2+ accounts, requests rotate; if account A has insufficient credits, request retries on account B.
6. Credentials never leave local disk except to Higgsfield upstream.

## 4. Architecture

```
Client (OpenAI SDK / curl / Comfy / custom)
        |  Bearer sk-local
        v
+---------------------------+
|  higgsfield-proxy (Go)    |
|  - API key gate           |
|  - OpenAI translators     |
|  - Account pool / RR      |
|  - OAuth manager          |
|  - Job poller             |
+-------------+-------------+
              |
              | Bearer oat_... + workspace
              v
        Higgsfield backend
```

### 4.1 Package layout

```
higgsfield-proxy/
  cmd/server/main.go
  internal/
    config/          # yaml + env
    auth/oauth.go    # PKCE login, callback, token exchange/refresh
    account/         # store, select, cooldown, credit snapshot
    higgs/           # upstream HTTP client (models, generate, status)
    openaiapi/       # request/response types + handlers
    admin/           # login/list accounts/status
    server/          # router, middleware
  data/
    accounts/        # one json file per account
    jobs/            # optional local job cache
  config.example.yaml
  go.mod
  README.md
  docs/superpowers/specs/...
```

### 4.2 Components

| Component | Responsibility |
|---|---|
| `config` | Port, local API keys, default model map, timeouts, poll interval |
| `auth` | Start OAuth, handle callback, exchange code, persist tokens |
| `account` | Load/save accounts, pick next healthy account, mark bad/cooldown |
| `higgs` | Call upstream list/create/status APIs using account token |
| `openaiapi` | Map OpenAI payload ↔ Higgsfield job params; shape responses |
| `admin` | Operator endpoints for login and account inspection |
| `server` | HTTP server, auth middleware, logging |

## 5. Authentication

### 5.1 Client → proxy

- Header: `Authorization: Bearer <local_api_key>`
- Keys configured in `config.yaml` (`api_keys: [...]`)
- Missing/invalid key → `401`

### 5.2 Proxy → Higgsfield (account OAuth)

Flow mirrors official CLI:

1. Admin calls `POST /admin/auth/login` (optional `port` for callback).
2. Proxy generates PKCE `code_verifier` / `code_challenge` and `state`.
3. Opens browser to Clerk authorize URL (same client as CLI if possible).
4. Local callback receives `code` + `state`.
5. Proxy exchanges code for access token (and refresh token if provided).
6. Proxy fetches workspaces, selects default (or highest-credit) workspace.
7. Saves account file under `data/accounts/<account_id>.json`.

Account file fields (minimum):

```json
{
  "id": "uuid-or-email-hash",
  "email": "user@example.com",
  "access_token": "oat_...",
  "refresh_token": "...",
  "workspace_id": "212f48dd-...",
  "plan": "plus",
  "credits": 1210,
  "created_at": "...",
  "updated_at": "...",
  "disabled": false,
  "cooldown_until": null,
  "last_error": ""
}
```

### 5.3 Explicit non-goal: paste used callback URL

A URL like `http://localhost:8765/callback?code=...&state=...` only works if:

- the matching in-memory PKCE verifier still exists, and
- the code has not been consumed.

Therefore v1 does **not** support pasting callback links. Account add is only via proxy-owned OAuth login session (PKCE verifier held in process memory until callback completes).

## 6. Public API (OpenAI-compatible)

Base URL example: `http://127.0.0.1:8317`

### 6.1 `GET /v1/models`

Returns mapped models:

```json
{
  "object": "list",
  "data": [
    { "id": "text2image_soul_v2", "object": "model", "owned_by": "higgsfield" },
    { "id": "seedance_2_0", "object": "model", "owned_by": "higgsfield" }
  ]
}
```

Model IDs use Higgsfield CLI `job_type` strings for transparency.

### 6.2 `POST /v1/images/generations`

Request (OpenAI-style):

```json
{
  "model": "text2image_soul_v2",
  "prompt": "a red apple on a white table",
  "n": 1,
  "size": "1024x1024"
}
```

Behavior:

1. Map `size` → aspect ratio / resolution defaults.
2. Select account.
3. Create image job upstream.
4. Poll until completed or timeout.
5. Return:

```json
{
  "created": 1710000000,
  "data": [ { "url": "https://..." } ]
}
```

Notes:

- `n > 1`: either sequential jobs or rejected with `400` if upstream lacks native batch; v1 default supports `n=1` only, higher n implemented as sequential if cheap.
- v1 does not accept vendor-specific extra params beyond mapped OpenAI fields.

### 6.3 `POST /v1/videos/generations`

Not a core OpenAI endpoint everywhere, but commonly used by relays. v1 defines:

```json
{
  "model": "seedance_2_0",
  "prompt": "cinematic drone shot over mountains",
  "seconds": 5,
  "size": "1280x720"
}
```

Optional media refs (image-to-video):

```json
{
  "model": "some_i2v_model",
  "prompt": "...",
  "image": "https://... or data:image/..."
}
```

Response (sync wait mode, default):

```json
{
  "created": 1710000000,
  "data": [ { "url": "https://.../video.mp4" } ]
}
```

If `wait=false` or timeout:

```json
{
  "id": "job_xxx",
  "status": "queued",
  "status_url": "/v1/jobs/job_xxx"
}
```

### 6.4 `GET /v1/jobs/{id}`

Proxy-local job status for long video tasks.

### 6.5 Admin

| Method | Path | Purpose |
|---|---|---|
| POST | `/admin/auth/login` | Start OAuth add-account |
| GET | `/admin/accounts` | List accounts (mask tokens) |
| POST | `/admin/accounts/{id}/disable` | Disable account |
| POST | `/admin/accounts/{id}/enable` | Enable account |
| GET | `/admin/status` | Health + pool summary |

Admin endpoints protected by the same local API key or a separate `admin_api_key`.

## 7. Account pool & scheduling

### 7.1 Selection policy (v1)

1. Consider only `disabled=false` and not in cooldown.
2. Prefer accounts with known `credits > min_credits` (config, default 1).
3. Round-robin among eligible accounts.
4. On failure:
   - `not_enough_credits` / 402-like → refresh credits, cooldown short, try next account
   - auth 401 → try refresh token; if fail, disable account and try next
   - 429 / rate limit → cooldown that account, try next
   - 5xx → retry same account once, then next

### 7.2 Credit refresh

- Refresh account credits periodically (e.g. every 60s) and after failures.
- Source: same endpoint family used by `higgsfield account status` / workspace list.

### 7.3 Concurrency

- Per-account semaphore (default 1–2 in-flight jobs) to reduce ban/rate risk.
- Global max concurrent jobs configurable.

## 8. Upstream integration strategy

### 8.1 Discovery method

During implementation, reverse-engineer the official CLI binary traffic / config paths used by:

- `higgsfield auth login`
- `higgsfield account status`
- `higgsfield model list`
- `higgsfield generate create <job_type> ... --wait`

Prefer calling the same HTTPS APIs the CLI uses, with stored OAuth token + workspace header/query as required.

### 8.2 Fallback

If direct reverse-engineering is blocked temporarily, v1 may shell-exec `higgsfield` with isolated env/config dir per account. This is a **fallback only**; primary path is pure HTTP client for performance and multi-account isolation.

### 8.3 Job lifecycle

```
create job -> request_id
loop:
  status = get status(request_id)
  if completed: extract media urls
  if failed/nsfw/cancelled: error
  sleep poll_interval
until timeout
```

Defaults:

- image poll interval: 2s, timeout: 180s
- video poll interval: 5s, timeout: 20m

## 9. Config

`config.example.yaml`:

```yaml
host: 127.0.0.1
port: 8317
api_keys:
  - sk-local-change-me
admin_api_key: sk-admin-change-me
data_dir: ./data
min_credits: 1
account_concurrency: 1
global_concurrency: 4
image_timeout_sec: 180
video_timeout_sec: 1200
poll_interval_ms: 2000
default_image_model: text2image_soul_v2
default_video_model: seedance_2_0
# optional model aliases
aliases:
  soul-v2: text2image_soul_v2
```

## 10. Security

- Bind default to `127.0.0.1` only.
- Never log full access tokens.
- Account files readable only by user (Windows ACL best-effort / 0600 on Unix).
- Local API key required for all non-health routes.
- No remote account sharing features in v1.

Compliance note: multi-account automation may violate Higgsfield Terms of Service depending on use (especially resale/shared access). Design is for personal/local aggregation of accounts the operator owns.

## 11. Error mapping

| Situation | HTTP | Body idea |
|---|---|---|
| Bad local key | 401 | OpenAI-style error |
| Unknown model | 404 | model_not_found |
| No healthy accounts | 503 | no_available_account |
| All accounts out of credits | 402 | insufficient_quota |
| Upstream content filter | 400 | content_policy_violation |
| Upstream hard fail | 502 | upstream_error |

## 12. Testing plan

1. Unit: account picker (RR, cooldown, disable).
2. Unit: OpenAI request mapping (size → aspect/resolution).
3. Integration (manual): OAuth login with real Plus account.
4. Integration: one image generation end-to-end.
5. Integration: one video generation end-to-end.
6. Integration: two accounts, force first account credits=0 / disabled → second succeeds.
7. Negative: invalid local API key.

## 13. Implementation phases

### Phase 1 — Skeleton
- Go module, config, server, health, local API key middleware
- `/v1/models` static/dynamic stub

### Phase 2 — OAuth + account store
- Login flow + account JSON persistence
- `/admin/accounts`, account status/credits

### Phase 3 — Image path
- Upstream client create+poll
- `/v1/images/generations` sync response

### Phase 4 — Video path
- `/v1/videos/generations` + `/v1/jobs/{id}`

### Phase 5 — Multi-account pool
- Round-robin, failover, cooldowns, concurrency limits

### Phase 6 — Polish
- README, example curl/SDK snippets, config example, basic logging

## 14. Risks

| Risk | Mitigation |
|---|---|
| CLI upstream API undocumented / changes | Isolate in `higgs` client; add version pin notes; fallback exec |
| OAuth client_id / endpoints change | Centralize constants; detect via CLI release notes |
| Video jobs very long | Async job API + generous timeout |
| Account ban from aggressive concurrency | Low default concurrency + cooldowns |

## 15. Decisions locked

- Language: **Go**
- Scope v1: **multi-account image + video**
- Account add: **built-in OAuth**
- Client protocol: **OpenAI-compatible images/videos** (not chat wrapper)
- Approach: **standalone service (A)**, not CLIProxyAPI fork, not CLI shell wrapper as primary
