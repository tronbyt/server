package main

import (
	"flag"
	"log/slog"
	"os"

	"tronbyt-server/internal/migration"
)

func main() {
	oldDBPath := flag.String("old", "users/tronbyt.db", "Path to legacy SQLite database")
	newDBPath := flag.String("new", "data/tronbyt.db", "Path to new GORM database (or DSN)")
	dataDir := flag.String("data", "data", "Path to data directory for files")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := migration.MigrateLegacyDB(*oldDBPath, *newDBPath, *dataDir); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}
}
