package main

import (
	"fmt"
	"log/slog"
	"strings"
	"tronbyt-server/cmd/server/boot"
	"tronbyt-server/cmd/server/health"
	"tronbyt-server/cmd/server/migrate"
	"tronbyt-server/cmd/server/resetpassword"
	"tronbyt-server/cmd/server/serve"
	"tronbyt-server/cmd/server/updatesystemapps"
	"tronbyt-server/internal/config"

	"github.com/spf13/cobra"
)

func New() *cobra.Command {
	cobra.MousetrapHelpText = ""

	cmd := &cobra.Command{
		Use:               "tronbyt-server",
		Short:             "Manage your apps on your Tronbyt completely locally",
		PersistentPreRunE: preRun,
	}

	fs := cmd.PersistentFlags()
	fs.String(config.FlagDB, "data/tronbyt.db", "Database DSN (sqlite file path or connection string)")
	fs.String(config.FlagData, "data", "Path to data directory")

	cmd.AddCommand(
		boot.New(),
		serve.New(),
		migrate.New(),
		resetpassword.New(),
		updatesystemapps.New(),
		health.New(),
	)

	return cmd
}

func preRun(cmd *cobra.Command, _ []string) error {
	// Initialize slog before anything else that might log
	slog.SetDefault(slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil)))

	// Load configuration early to get default DB path
	cfg, err := config.LoadSettings()
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	fs := cmd.Flags()
	if f := fs.Lookup(config.FlagDB); f != nil && f.Changed {
		cfg.DBDSN = f.Value.String()
	}
	if f := fs.Lookup(config.FlagData); f != nil && f.Changed {
		cfg.DataDir = f.Value.String()
	}

	// Re-initialize logger with configured log level
	var level slog.Level
	cfg.LogLevel = strings.ToUpper(cfg.LogLevel)
	cfg.LogLevel = strings.Replace(cfg.LogLevel, "WARNING", "WARN", 1)
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		slog.Warn("Invalid LOG_LEVEL, defaulting to INFO", "level", cfg.LogLevel)
		level = slog.LevelInfo
	}

	// Create handler options with the parsed level
	loggerHandlerOpts := &slog.HandlerOptions{
		Level: level,
	}
	var logHandler slog.Handler
	if cfg.LogFormat == "json" {
		logHandler = slog.NewJSONHandler(cmd.ErrOrStderr(), loggerHandlerOpts)
	} else {
		logHandler = slog.NewTextHandler(cmd.ErrOrStderr(), loggerHandlerOpts)
	}
	slog.SetDefault(slog.New(logHandler))

	slog.Debug("Logger initialized", "level", level)

	cmd.SetContext(config.NewContext(cmd.Context(), cfg))
	return nil
}
