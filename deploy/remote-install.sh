#!/usr/bin/env bash
# Run on the server as root after files are uploaded to /opt/higgsfield-proxy
set -euo pipefail

APP_DIR=/opt/higgsfield-proxy
cd "$APP_DIR"

export DEBIAN_FRONTEND=noninteractive
if command -v apt-get >/dev/null 2>&1; then
  apt-get update -y
  apt-get install -y ca-certificates curl tar
fi

# Install Node LTS if missing (for higgsfield CLI)
if ! command -v node >/dev/null 2>&1; then
  curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
  apt-get install -y nodejs
fi

# Install / update Higgsfield CLI
npm i -g @higgsfield/cli@1.1.20 || npm i -g @higgsfield/cli

mkdir -p "$APP_DIR/data/accounts" "$APP_DIR/data/cli-homes" "$APP_DIR/data/uploads"
chmod 700 "$APP_DIR/data" || true
chmod +x "$APP_DIR/higgsfield-proxy" || true

# Prefer server config
if [[ ! -f "$APP_DIR/config.yaml" ]]; then
  cp "$APP_DIR/config.server.yaml" "$APP_DIR/config.yaml"
fi

# systemd unit
cat >/etc/systemd/system/higgsfield-proxy.service <<'EOF'
[Unit]
Description=Higgsfield OpenAI Proxy
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/higgsfield-proxy
Environment=HF_PROXY_HOST=0.0.0.0
Environment=HF_PROXY_PORT=8317
Environment=HF_PROXY_DATA_DIR=/opt/higgsfield-proxy/data
Environment=HF_PROXY_CLI_PATH=higgsfield
# Override these with your own secrets:
Environment=HF_PROXY_API_KEYS=sk-canvas-change-me
Environment=HF_PROXY_ADMIN_KEY=sk-admin-change-me
Environment=HF_PROXY_ADMIN_PASSWORD=admin123
ExecStart=/opt/higgsfield-proxy/higgsfield-proxy -config /opt/higgsfield-proxy/config.yaml
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable higgsfield-proxy
systemctl restart higgsfield-proxy
sleep 2
systemctl --no-pager status higgsfield-proxy || true
curl -fsS http://127.0.0.1:8317/healthz || true
echo
echo "Deploy done. Open: http://SERVER_IP:8317/"
echo "Login password: admin123 (change immediately)"
echo "Import account: copy data/accounts + data/cli-homes from your PC, then systemctl restart higgsfield-proxy"
