# higgsfield-proxy

Local multi-account OpenAI-compatible proxy for Higgsfield image/video generation.

## Features

- OpenAI-style APIs: `/v1/models`, `/v1/images/generations`, `/v1/videos/generations`
- Multi-account pool with round-robin + failover
- Import current CLI login, or OAuth login via admin API
- Uses official `higgsfield` CLI under the hood (Plus subscription credits)

## Requirements

- Go 1.22+
- `higgsfield` CLI on PATH (`npm i -g @higgsfield/cli`)

## Quick start

```bash
cd higgsfield-proxy
copy config.example.yaml config.yaml
go run ./cmd/server
```

Open the dashboard: [http://127.0.0.1:8317/](http://127.0.0.1:8317/)

Default keys in `config.yaml`:

- API Key: `sk-local-demo`
- Admin Key: `sk-admin-demo`

Import the account already logged in via CLI:

```bash
curl -X POST http://127.0.0.1:8317/admin/accounts/import-cli ^
  -H "Authorization: Bearer sk-admin-demo"
```

Generate an image:

```bash
curl http://127.0.0.1:8317/v1/images/generations ^
  -H "Authorization: Bearer sk-local-demo" ^
  -H "Content-Type: application/json" ^
  -d "{\"model\":\"text2image_soul_v2\",\"prompt\":\"a red apple on white table\",\"size\":\"1024x1024\"}"
```

## Auth

- Client key: `api_keys` in config
- Admin key: `admin_api_key` (or any client key)

## Admin

| Method | Path | Desc |
|--------|------|------|
| GET | `/admin/status` | pool summary |
| GET | `/admin/accounts` | list accounts (tokens masked) |
| POST | `/admin/accounts/import-cli` | import `~/.config/higgsfield` login |
| POST | `/admin/auth/login` | browser OAuth add account (long request) |
| POST | `/admin/accounts/{id}/disable` | disable |
| POST | `/admin/accounts/{id}/enable` | enable |

## Media references

Image/video generation accepts:

| Field | Meaning |
|-------|---------|
| `image` / `image_references` | reference images |
| `start_image` / `end_image` | first/last frame (i2v) |
| `video` / `video_references` | reference videos |
| `audio` / `audio_references` | reference audio |

Each value may be local path, upload id, `http(s)` URL, or base64 data URL.

```bash
# upload
curl -X POST http://127.0.0.1:8317/v1/files -H "Authorization: Bearer sk-local-demo" -F file=@./ref.png

# generate with refs
curl http://127.0.0.1:8317/v1/generate -H "Authorization: Bearer sk-local-demo" -H "Content-Type: application/json" -d "{\"model\":\"nano_banana\",\"prompt\":\"same product, studio light\",\"image_references\":[\"C:/path/to/ref.png\"]}"
```

## Server deploy (canvas OpenAI channel)

See [docs/DEPLOY.md](docs/DEPLOY.md).

Quick Docker:

```bash
docker compose up -d --build
```

Canvas channel fields:

| Field | Value |
|-------|--------|
| Protocol | OpenAI |
| Base URL | `http://SERVER_IP:8317/v1` |
| API Key | value of `api_keys` / `HF_PROXY_API_KEYS` |
| Timeout | 600 |
| Models | `seedance_2_0_fast`, `text2image_soul_v2`, ... |

## Canvas integration

See [docs/CANVAS_INTEGRATION.md](docs/CANVAS_INTEGRATION.md).

Summary:

- Canvas uses **proxy API key only** (`sk-...`), never Higgsfield OAuth tokens
- Prefer `POST /v1/generate` + `POST /v1/files` + `GET /v1/jobs/{id}`
- CORS enabled for local canvas tools

## Notes

- Generation goes through the official CLI to avoid Cloudflare blocks on raw gateway POSTs.
- Each account gets an isolated config home under `data/cli-homes/`.
- Multi-account resale/sharing may violate Higgsfield ToS; use only accounts you own.
