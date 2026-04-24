package serve

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/migration"

	"gorm.io/gorm"
)

func migrateLegacyDB(ctx context.Context, dsn, dataDir string) error {
	// Check for legacy DB for automatic migration
	legacyDBPath := filepath.Join("users", "usersdb.sqlite") // Old Python DB path
	if _, err := os.Stat(legacyDBPath); err == nil && legacyDBPath != dsn {
		slog.Info("Found legacy database, checking if migration is needed", "legacy_db", legacyDBPath, "new_db", dsn)

		tempDB, err := data.Open(dsn, "ERROR")
		if err == nil {
			if tempDB.Migrator().HasTable(&data.User{}) {
				count, err := gorm.G[data.User](tempDB).Count(ctx, "*")
				if err == nil && count > 0 {
					slog.Warn("New database already has users, skipping automatic migration.", "new_db", dsn)
					return nil
				}
			}
			if sqlDB, err := tempDB.DB(); err == nil {
				if err := sqlDB.Close(); err != nil {
					slog.Error("Failed to close temporary DB connection", "error", err)
				}
			}
		}

		// Perform migration
		if err := migration.MigrateLegacyDB(legacyDBPath, dsn, dataDir); err != nil {
			return fmt.Errorf("automatic migration failed: %w", err)
		}
		slog.Info("Automatic migration completed successfully. Renaming legacy DB.", "legacy_db", legacyDBPath)
		if err := os.Rename(legacyDBPath, legacyDBPath+".bak"); err != nil {
			slog.Error("Failed to rename legacy DB after migration", "error", err)
		}
	}

	return nil
}

func sanitizeDB(ctx context.Context, db *gorm.DB) {
	// Sanitize data before migration (fixes v2.0.x empty email constraint issue)
	if db.Migrator().HasTable(&data.User{}) {
		if _, err := gorm.G[data.User](db).Where("email IN ?", []string{"", "none"}).Update(ctx, "email", nil); err != nil {
			slog.Warn("Failed to sanitize empty emails", "error", err)
		}
	}

	// Fix timezone issues
	if db.Migrator().HasTable(&data.Device{}) {
		devices, err := gorm.G[data.Device](db).Where("location LIKE '%\"timezone\":\"None\"'").Find(ctx)
		if err != nil {
			slog.Warn("Failed to get devices with illegal timestamps", "error", err)
		} else {
			for _, device := range devices {
				device.Location.Timezone = ""
				if _, err := gorm.G[data.Device](db).Where("id = ?", device.ID).Update(ctx, "location", device.Location); err != nil {
					slog.Warn("Failed to update device location during sanitization", "device_id", device.ID, "error", err)
				}
			}
		}
	}
}

func singleUserWarning(ctx context.Context, db *gorm.DB, singleUserAutoLogin bool) {
	// Single User Auto-Login Warning
	userCount, err := gorm.G[data.User](db).Count(ctx, "*")
	if err != nil {
		slog.Error("Failed to count users for auto-login warning", "error", err)
	} else if singleUserAutoLogin && userCount == 1 {
		slog.Warn(`
======================================================================
⚠️  SINGLE-USER AUTO-LOGIN MODE IS ENABLED
======================================================================
Authentication is DISABLED for private network connections!

This mode automatically logs in the single user without password.

SECURITY REQUIREMENTS:
  ✓ Only works when exactly 1 user exists
  ✓ Only works from trusted networks:
    - Localhost (127.0.0.1, ::1)
    - Private IPv4 networks (192.168.x.x, 10.x.x.x, 172.16.x.x)
    - IPv6 local ranges (Unique Local Addresses fc00::/7, commonly fd00::/8)
    - IPv6 link-local (fe80::/10)
  ✓ Public IP connections still require authentication

To disable: Set SINGLE_USER_AUTO_LOGIN=0 in your .env file
======================================================================`)
	}
}
