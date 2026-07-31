# higgsfield-proxy server image
# Requires: official higgsfield CLI (Node) + this Go service

FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/higgsfield-proxy ./cmd/server

FROM node:22-bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl tar \
  && rm -rf /var/lib/apt/lists/*
# Official CLI (downloads platform binary in postinstall)
RUN npm i -g @higgsfield/cli@1.1.20 || npm i -g @higgsfield/cli

WORKDIR /app
COPY --from=build /out/higgsfield-proxy /app/higgsfield-proxy
COPY config.server.yaml /app/config.yaml

ENV HF_PROXY_HOST=0.0.0.0 \
    HF_PROXY_PORT=8317 \
    HF_PROXY_DATA_DIR=/data \
    HF_PROXY_CLI_PATH=higgsfield

VOLUME ["/data"]
EXPOSE 8317

# Accounts must be imported after first start via UI/admin API
CMD ["/app/higgsfield-proxy", "-config", "/app/config.yaml"]
