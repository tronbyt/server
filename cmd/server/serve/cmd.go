package serve

import (
	"fmt"
	"log/slog"
	"os"
	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/gitutils"
	"tronbyt-server/internal/server"

	"github.com/spf13/cobra"
)

const Name = "serve"

func New() *cobra.Command {
	return &cobra.Command{
		Use:   Name,
		Short: "Run Tronbyt server",
		RunE:  run,

		SilenceUsage: true,
	}
}

func run(cmd *cobra.Command, args []string) error {
	cfg, err := config.FromContext(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	if err := migrateLegacyDB(cmd.Context(), cfg.DBDSN, cfg.DataDir); err != nil {
		return fmt.Errorf("failed to migrate legacy database: %w", err)
	}

	// Clone/Update System Apps Repo
	if err := gitutils.EnsureRepo(cfg.SystemAppsDir(), cfg.SystemAppsRepo, cfg.GitHubToken, cfg.Production); err != nil {
		slog.Error("Failed to update system apps repo", "error", err)
	}

	// Open DB
	db, err := data.Open(cfg.DBDSN, cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Sanitize data
	sanitizeDB(cmd.Context(), db)

	// AutoMigrate (ensure schema exists)
	if err := db.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.WebAuthnCredential{}, &data.Setting{}); err != nil {
		return fmt.Errorf("failed to migrate schema: %w", err)
	}

	cache, err := initCache(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	defer cache.Close()

	srv := server.NewServer(db, cfg)

	// Firmware Update (production only)
	if cfg.Production {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic during background firmware update", "panic", r)
				}
			}()
			if err := srv.UpdateFirmwareBinaries(); err != nil {
				slog.Error("Failed to update firmware binaries in background", "error", err)
			}
		}()
	} else {
		slog.Info("Skipping firmware update (dev mode)")
	}

	singleUserWarning(cmd.Context(), db, cfg.SingleUserAutoLogin)

	return serve(cfg, srv)
}
