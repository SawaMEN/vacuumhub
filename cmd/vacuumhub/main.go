package main

import (
	"log/slog"
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
