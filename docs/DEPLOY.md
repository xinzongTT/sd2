# 服务器部署 + 画布 OpenAI 渠道

把 `higgsfield-proxy` 部署到服务器后，在画布里按「新增渠道」填 OpenAI 协议即可。

## 1. 画布渠道怎么填（对照你截图）

| 字段 | 填什么 |
|------|--------|
| **渠道名称** | `Higgsfield`（随意） |
| **协议** | `OpenAI` |
| **权重** | `1` |
| **请求超时（秒）** | `600`（视频建议 ≥ 600） |
| **启用** | 开 |
| **接口地址** | `http://你的服务器IP:8317/v1` |
| **API Key** | `sk-canvas-change-me`（与服务器 `api_keys` 一致，务必改掉） |
| **渠道可用模型** | 选/填：`seedance_2_0_fast`、`seedance_2_0_mini`、`seedance_2_0`、`text2image_soul_v2`、`nano_banana` 等 |
| **备注** | 可选 |

说明：

- 多数画布会在「接口地址」后自动拼 `/models`、`/images/generations` 等，所以地址要带 **`/v1`**。
- 若测不通，可试不带 `/v1`：`http://IP:8317`（看画布是否自己加 `/v1`）。
- 本服务已提供：
  - `GET /v1/models`
  - `POST /v1/images/generations`
  - `POST /v1/videos/generations`
  - `POST /v1/generate`（画布自定义节点更稳）
  - `POST /v1/files`（参考图上传）
  - `GET /v1/jobs/{id}`

> 若画布 **只支持 chat/completions**、没有 images/videos 节点，需要额外适配；当前按 OpenAI **图像/模型列表** 渠道设计。视频优先用画布「视频生成」节点或自定义请求打 `/v1/videos/generations` / `/v1/generate`。

## 2. Docker 部署（推荐）

服务器要求：Linux x86_64，开放 `8317`（或前面加 Nginx HTTPS）。

```bash
# 上传本目录到服务器后
cd higgsfield-proxy
# 改密钥
export HF_PROXY_API_KEYS="sk-你的画布密钥"
export HF_PROXY_ADMIN_KEY="sk-你的管理密钥"

docker compose up -d --build
```

健康检查：

```bash
curl http://127.0.0.1:8317/healthz
curl http://127.0.0.1:8317/v1/models -H "Authorization: Bearer sk-你的画布密钥"
```

管理台（浏览器）：

```
http://服务器IP:8317/
```

Admin Key 登录后：

1. **导入 CLI 登录**（若容器内无账号）或 **OAuth 加号**
2. 确认账号 credits 正常

> 容器内第一次需要有 Higgsfield 账号。推荐在服务器执行一次 CLI 登录后把 `data` 挂载进来，或在管理台 OAuth（需服务器能弹浏览器/你本机登录后把 `data/accounts` 拷上去）。

### 更稳妥：本机登录后拷账号到服务器

在你 Windows 本机已登录的 proxy 目录：

```
data/accounts/*.json
data/cli-homes/**
```

拷到服务器 volume 对应路径（compose 里是命名卷 `hf_proxy_data`，可先改成 bind mount）：

```yaml
volumes:
  - ./data:/data
```

## 3. 直接二进制部署（无 Docker）

```bash
# 安装 Node + CLI
npm i -g @higgsfield/cli

# 编译
go build -o higgsfield-proxy ./cmd/server

# 配置
cp config.server.yaml config.yaml
# 编辑 api_keys / admin_api_key

export HF_PROXY_HOST=0.0.0.0
export HF_PROXY_API_KEYS=sk-你的画布密钥
export HF_PROXY_ADMIN_KEY=sk-你的管理密钥

./higgsfield-proxy -config config.yaml
```

systemd 示例：`docs/higgsfield-proxy.service`。

## 4. Nginx 反代 HTTPS（建议）

```nginx
server {
  listen 443 ssl;
  server_name hf-api.example.com;
  # ssl_certificate ...;

  location / {
    proxy_pass http://127.0.0.1:8317;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_read_timeout 600s;
    proxy_send_timeout 600s;
    client_max_body_size 200m;
  }
}
```

画布接口地址改为：

```
https://hf-api.example.com/v1
```

## 5. 环境变量

| 变量 | 含义 |
|------|------|
| `HF_PROXY_HOST` | 监听地址，服务器用 `0.0.0.0` |
| `HF_PROXY_PORT` | 端口，默认 `8317` |
| `HF_PROXY_API_KEYS` | 画布用密钥，逗号分隔多个 |
| `HF_PROXY_ADMIN_KEY` | 管理台密钥 |
| `HF_PROXY_DATA_DIR` | 数据目录（账号/上传） |
| `HF_PROXY_CLI_PATH` | `higgsfield` 可执行文件路径 |

## 6. 画布侧快速自检

```bash
# 模型列表（渠道「选择模型」依赖这个）
curl https://你的域名/v1/models -H "Authorization: Bearer sk-xxx"

# 文生图
curl https://你的域名/v1/images/generations \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"text2image_soul_v2","prompt":"a cat","size":"1024x1024"}'

# 图生视频（便宜 fast）
curl https://你的域名/v1/videos/generations \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"seedance_2_0_fast","prompt":"slow push in","size":"1280x720","seconds":5,"start_image":"https://.../ref.png","wait":true}'
```

## 7. 安全

- 必须改默认 `sk-canvas-change-me`
- 管理台 Admin Key 不要填进画布渠道
- 优先 HTTPS + 防火墙只放行 443
- Higgsfield 订阅 token 只存在服务器 `data/accounts`，不要进画布
