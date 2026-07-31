package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/xinzo/higgsfield-proxy/internal/account"
	"github.com/xinzo/higgsfield-proxy/internal/admin"
	"github.com/xinzo/higgsfield-proxy/internal/apikey"
	"github.com/xinzo/higgsfield-proxy/internal/config"
	"github.com/xinzo/higgsfield-proxy/internal/higgs"
	"github.com/xinzo/higgsfield-proxy/internal/openaiapi"
	"github.com/xinzo/higgsfield-proxy/internal/reqlog"
	"github.com/xinzo/higgsfield-proxy/internal/server"
	"github.com/xinzo/higgsfield-proxy/internal/session"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config.yaml (optional if env set)")
	flag.Parse()

	path := *cfgPath
	if path == "config.yaml" {
		if _, err := os.Stat(path); err != nil {
			path = ""
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "accounts"), 0o700); err != nil {
		log.Fatalf("accounts dir: %v", err)
	}

	store, err := account.NewStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	pool := account.NewPool(store, cfg.MinCredits, cfg.AccountConcurrency)
	cli := &higgs.CLI{Path: cfg.CLIPath, DataDir: cfg.DataDir}

	keys, err := apikey.NewStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("apikey store: %v", err)
	}
	_ = keys.SeedFromConfig(cfg.APIKeys)

	logs, err := reqlog.NewStore(cfg.DataDir, cfg.LogMaxEntries)
	if err != nil {
		log.Fatalf("log store: %v", err)
	}

	sess := session.New(cfg.AdminPassword, 24*time.Hour)
	oai := openaiapi.NewHandler(cfg, pool, cli)
	adm := admin.New(cfg, pool, cli, keys, logs, sess)
	srv := server.New(cfg, oai, adm, keys, logs, sess)

	addr := cfg.Addr()
	fmt.Printf("higgsfield-proxy listening on http://%s\n", addr)
	fmt.Printf("  ui:      http://%s/  (login password from admin_password)\n", addr)
	fmt.Printf("  health:  GET  /healthz\n")
	fmt.Printf("  models:  GET  /v1/models\n")
	fmt.Printf("  keys:    GET/POST /admin/keys\n")
	fmt.Printf("  logs:    GET  /admin/logs\n")
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
