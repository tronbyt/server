package migrate

import (
	"fmt"
	"tronbyt-server/internal/config"

	"tronbyt-server/internal/migration"

	"github.com/spf13/cobra"
)

const (
	Name          = "migrate"
	flagOldDBPath = "old"
	flagNewDBPath = "new"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   Name,
		Short: "Migrate legacy SQLite database to GORM database",
		RunE:  run,
	}

	fs := cmd.Flags()
	fs.String(flagOldDBPath, "users/tronbyt.db", "Path to legacy SQLite database")
	fs.String(flagNewDBPath, "", "Path to new GORM database (or DSN)")
	_ = fs.MarkHidden(flagNewDBPath)

	return cmd
}

func run(cmd *cobra.Command, _ []string) error {
	cfg, err := config.FromContext(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	oldDBPath := cmd.Flag(flagOldDBPath).Value.String()
	if newDBPath := cmd.Flag(flagNewDBPath).Value.String(); newDBPath != "" {
		cfg.DBDSN = newDBPath
	}

	if err := migration.MigrateLegacyDB(oldDBPath, cfg.DBDSN, cfg.DataDir); err != nil {
		return fmt.Errorf("database migration failed: %w", err)
	}
	return nil
}
