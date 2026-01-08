package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/legacy"

	_ "github.com/mattn/go-sqlite3"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// MigrateLegacyDB performs the migration from the old SQLite DB to the new GORM DB.
func MigrateLegacyDB(oldDBPath, newDBLocation, dataDir string) error {
	slog.Info("Migrating database", "old", oldDBPath, "new", newDBLocation, "dataDir", dataDir)

	// 1. Read Legacy Data
	users, err := readLegacyUsers(oldDBPath)
	if err != nil {
		return fmt.Errorf("failed to read legacy users: %w", err)
	}
	slog.Info("Found users to migrate", "count", len(users))

	// 2. Setup New DB
	var newDB *gorm.DB
	if strings.HasPrefix(newDBLocation, "postgres") || strings.Contains(newDBLocation, "host=") {
		slog.Info("Using Postgres for new DB")
		newDB, err = gorm.Open(postgres.Open(newDBLocation), &gorm.Config{})
	} else if strings.Contains(newDBLocation, "@tcp(") || strings.Contains(newDBLocation, "@unix(") {
		slog.Info("Using MySQL for new DB")
		newDB, err = gorm.Open(mysql.Open(newDBLocation), &gorm.Config{})
	} else {
		slog.Info("Using SQLite for new DB")
		newDB, err = gorm.Open(sqlite.Open(newDBLocation), &gorm.Config{})
		if err == nil {
			if err := newDB.Exec("PRAGMA busy_timeout=5000;").Error; err != nil {
				slog.Warn("Failed to set busy timeout for new SQLite DB", "error", err)
			}
		}
	}

	if err != nil {
		return fmt.Errorf("failed to open new DB: %w", err)
	}

	// AutoMigrate schema
	err = newDB.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.WebAuthnCredential{}, &data.Setting{})
	if err != nil {
		return fmt.Errorf("failed to migrate schema: %w", err)
	}

	// 3. Transform and Insert
	for _, lUser := range users {
		if err := migrateUser(newDB, lUser); err != nil {
			slog.Error("Error migrating user", "username", lUser.Username, "error", err)
			// Continue to next user if one fails, but log error.
		} else {
			slog.Info("Migrated user", "username", lUser.Username)
		}
	}

	// 4. Migrate Directories
	if err := migrateDirectories(oldDBPath, dataDir); err != nil {
		return fmt.Errorf("failed to migrate directories: %w", err)
	}

	slog.Info("Migration complete.")

	return nil
}

func readLegacyUsers(dbPath string) ([]legacy.LegacyUser, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Failed to close legacy DB", "error", err)
		}
	}()

	rows, err := db.Query("SELECT data FROM json_data")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close legacy DB rows", "error", err)
		}
	}()

	var users []legacy.LegacyUser
	for rows.Next() {
		var dataBlob []byte
		if err := rows.Scan(&dataBlob); err != nil {
			return nil, err
		}

		var user legacy.LegacyUser
		if err := json.Unmarshal(dataBlob, &user); err != nil {
			slog.Warn("Skipping corrupted user row", "error", err)
			continue
		}
		users = append(users, user)
	}

	return users, nil
}

func migrateUser(db *gorm.DB, lUser legacy.LegacyUser) error {
	user := lUser.ToDataUser()

	// Set Admin flag manually (not in legacy JSON usually, but implied by username)
	user.IsAdmin = (lUser.Username == "admin")

	// Save User (and cascading Devices/Apps)
	if err := gorm.G[data.User](db).Create(context.Background(), &user); err != nil {
		return fmt.Errorf("create user: %w", err)
	}

	return nil
}

func migrateDirectories(oldDBPath, newDataDir string) error {
	oldDir := filepath.Dir(oldDBPath)

	dbFileName := filepath.Base(oldDBPath)

	// Safety check: ensure old directory is named "users"
	if filepath.Base(oldDir) != "users" {
		slog.Warn("Skipping directory migration: old DB directory is not named 'users'", "dir", oldDir)
		return nil
	}

	targetUsersDir := filepath.Join(newDataDir, "users")

	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		return nil
	}

	// Create target directory
	if err := os.MkdirAll(targetUsersDir, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(oldDir)
	if err != nil {
		return fmt.Errorf("failed to read old users directory: %w", err)
	}

	for _, entry := range entries {
		if entry.Name() == dbFileName {
			slog.Info("Skipping DB file during directory migration", "file", entry.Name())
			continue
		}

		srcPath := filepath.Join(oldDir, entry.Name())
		dstPath := filepath.Join(targetUsersDir, entry.Name())

		// Check if destination exists
		if _, err := os.Stat(dstPath); err == nil {
			slog.Warn("Target already exists, skipping move", "src", srcPath, "dst", dstPath)
			continue
		}

		slog.Info("Moving item", "src", srcPath, "dst", dstPath)
		if err := movePath(srcPath, dstPath); err != nil {
			return fmt.Errorf("failed to move %s: %w", srcPath, err)
		}
	}

	return nil
}

func movePath(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		slog.Debug("os.Rename failed (likely cross-volume), attempting copy-and-delete", "src", src, "dst", dst, "error", err)
		if err := copyRecursive(src, dst); err != nil {
			return fmt.Errorf("copy failed: %w", err)
		}
		if err := os.RemoveAll(src); err != nil {
			return fmt.Errorf("remove source failed: %w", err)
		}
	}
	return nil
}

func copyRecursive(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) (err error) {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := sourceFile.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close source file %s: %w", src, closeErr)
		}
	}()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := destFile.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close destination file %s: %w", dst, closeErr)
		}
	}()

	// Perform the copy
	if _, copyErr := io.Copy(destFile, sourceFile); copyErr != nil {
		return copyErr
	}

	return err // This will return nil if no errors, or the first error encountered (open, create, copy, or close)
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if err := copyRecursive(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}
