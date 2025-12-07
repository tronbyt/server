package main

import (
	"flag"
	"log/slog"
	"os"

	"tronbyt-server/internal/migration"
)

func main() {
	oldDBPath := flag.String("old", "tronbyt.db", "Path to legacy SQLite database")
	newDBPath := flag.String("new", "new_tronbyt.db", "Path to new GORM database")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := migration.MigrateLegacyDB(*oldDBPath, *newDBPath); err != nil {
		slog.Error("Database migration failed", "error", err)
		os.Exit(1)
	}
}
