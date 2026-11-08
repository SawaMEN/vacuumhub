package main

import (
	"log/slog"
	"fmt"
	"net/http"
	"time"
	"os"
	"path/filepath"

	"github.com/SawaMEN/vacuumhub/internal/app"
	"github.com/SawaMEN/vacuumhub/internal/store"
)

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
func main() {
	// A fast, read-only probe used only by the container HEALTHCHECK.
	if len(os.Args) == 2 && os.Args[1] == "--healthcheck" {
		client := &http.Client{Timeout: 3 * time.Second}
		url := fmt.Sprintf("http://127.0.0.1%s/api/v1/ready", env("LISTEN_ADDR", ":8080"))
		resp, err := client.Get(url)
		if err != nil { os.Exit(1) }
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK { os.Exit(1) }
		return
	}
	dataDir := env("DATA_DIR", "./data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		slog.Error("data dir", "error", err)
		os.Exit(1)
	}
	db, err := store.Open(filepath.Join(dataDir, "vacuumhub.sqlite"))
	if err != nil {
		slog.Error("open DB", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		slog.Error("migrate", "error", err)
		os.Exit(1)
	}
	if err := db.Seed(); err != nil {
		slog.Error("seed", "error", err)
		os.Exit(1)
	}
	server, err := app.New(db, filepath.Join(dataDir, "assets"), os.Getenv("ADMIN_API_TOKEN"), env("WEB_DIR", "./web"))
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}
	if err := app.Run(env("LISTEN_ADDR", ":8080"), server.Handler()); err != nil {
		slog.Error("server", "error", err)
		os.Exit(1)
	}
}
