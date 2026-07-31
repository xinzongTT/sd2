# 画布对接说明

面向后续把 `higgsfield-proxy` 接到本地画布 / 节点编辑器。

## 1. 密钥分层（推荐）

| 密钥 | 用途 | 谁用 |
|------|------|------|
| **Client API Key** (`api_keys`) | 生成、上传、查任务 | 画布前端 / 后端代理 |
| **Admin Key** (`admin_api_key`) | 账号导入/禁用/OAuth | 仅管理台，不要进画布 |
| **Higgsfield OAuth token** | 上游订阅账号 | 只存在 proxy `data/accounts/`，画布永远不碰 |

原则：

1. 画布只拿 **proxy 自己的 sk-xxx**，不拿 Higgsfield 的 `oat_`。
2. 多用户画布时：每个用户/工作区发独立 client key（后续可扩展 key 表）。
3. 当前 v1：`config.yaml` 里配置 `api_keys` 列表即可。

鉴权头（三选一）：

```http
Authorization: Bearer sk-local-demo
X-API-Key: sk-local-demo
GET /v1/models?api_key=sk-local-demo
```

已开 CORS，本地画布跨端口可直接调。

## 2. 推荐对接 API

Base: `http://127.0.0.1:8317`

### 2.1 模型列表

```http
GET /v1/models
Authorization: Bearer sk-xxx
```

### 2.2 上传参考媒体（画布本地文件）

```http
POST /v1/files
Authorization: Bearer sk-xxx
Content-Type: multipart/form-data

file=<blob>
```

返回：

```json
{ "id": "up_....png", "path": "C:\\...\\data\\uploads\\up_....png", "filename": "a.png", "bytes": 12345 }
```

画布后续生成时把 `path` 或 `id` 填进媒体字段。

### 2.3 统一生成（画布首选）

```http
POST /v1/generate
Authorization: Bearer sk-xxx
Content-Type: application/json
```

```json
{
  "model": "seedance_2_0",
  "kind": "video",
  "prompt": "camera orbit around product",
  "size": "1280x720",
  "seconds": 5,
  "wait": false,
  "image": "C:/.../uploads/up_1.png",
  "image_references": ["C:/.../uploads/up_1.png", "C:/.../uploads/up_2.png"],
  "start_image": "C:/.../uploads/start.png",
  "end_image": "C:/.../uploads/end.png",
  "video": "C:/.../uploads/ref.mp4",
  "video_references": [],
  "audio": "C:/.../uploads/bgm.mp3",
  "audio_references": []
}
```

媒体字段支持：

- 本地 path（上传接口返回的 path）
- 上传文件 id（uploads 目录文件名）
- `http(s)://` URL（proxy 会下载）
- `data:image/...;base64,...`（节点内嵌小图）
- Higgsfield UUID（历史 job / upload id）

`wait`：

- `true`：同步等到完成，返回 `data[0].url`
- `false`：立即返回 `{ id, status, status_url }`，画布轮询

### 2.4 OpenAI 兼容（可选）

```http
POST /v1/images/generations
POST /v1/videos/generations
```

字段与上面相同（含 `image_references` 等扩展）。

### 2.5 查任务

```http
GET /v1/jobs/{id}
```

```json
{ "id": "...", "status": "completed", "url": "https://...", "model": "..." }
```

## 3. 画布节点建议

```
[Asset Node] --file/url--> [Upload] --path--> [Generate Node]
                                              | model
                                              | prompt
                                              | refs
                                              v
                                         [Job Poll] --> [Preview]
```

- **Upload 节点**：调 `/v1/files`，输出 `path`
- **Generate 节点**：调 `/v1/generate`，`wait=false` 适合长视频
- **Poll 节点**：`/v1/jobs/{id}` 直到 `completed|failed`
- **Preview 节点**：用返回的 CDN `url`

## 4. 错误码（画布可映射）

| HTTP | code | 含义 |
|------|------|------|
| 401 | invalid_api_key | 密钥错误 |
| 400 | invalid_media / missing_prompt | 参数/媒体问题 |
| 402/503 | no_available_account | 无可用账号/额度 |
| 502 | upstream_error | 上游失败（会自动尝试换号） |

## 5. 安全注意

- 默认只监听 `127.0.0.1`，画布同机访问最安全。
- 若局域网开放：务必改掉默认 sk，并考虑反代 + HTTPS。
- Admin 接口不要暴露给画布。
