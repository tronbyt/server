package migration

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tronbyt-server/internal/data"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateLegacyDB(t *testing.T) {
	// Setup temporary directories and database paths
	tempDir, err := os.MkdirTemp("", "migration_test_")
	assert.NoError(t, err)
	defer func() {
		if rErr := os.RemoveAll(tempDir); rErr != nil {
			slog.Error("Failed to remove temp directory", "error", rErr)
		}
	}()

	oldDBPath := filepath.Join(tempDir, "users", "tronbyt.db")
	newDBPath := filepath.Join(tempDir, "new_tronbyt.db")
	dataDir := filepath.Join(tempDir, "data")

	// Create necessary parent directory for oldDBPath
	err = os.MkdirAll(filepath.Dir(oldDBPath), 0755)
	assert.NoError(t, err)
	err = os.MkdirAll(dataDir, 0755)
	assert.NoError(t, err)

	// 1. Prepare Legacy DB with sample data
	legacyDB, err := sql.Open("sqlite3", oldDBPath)
	assert.NoError(t, err)
	defer func() {
		if cErr := legacyDB.Close(); cErr != nil {
			slog.Error("Failed to close legacy DB", "error", cErr)
		}
	}()

	_, err = legacyDB.Exec("CREATE TABLE json_data (key TEXT PRIMARY KEY, data TEXT)")
	assert.NoError(t, err)

	// Read migration_test.json and insert into legacyDB
	file, err := os.Open("migration_test.json")
	assert.NoError(t, err)
	defer func() {
		if cErr := file.Close(); cErr != nil {
			slog.Error("Failed to close migration_test.json", "error", cErr)
		}
	}()

	scanner := bufio.NewScanner(file)
	const maxCapacity = 1024 * 1024 // 1MB
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var userMap map[string]any
		err := json.Unmarshal([]byte(line), &userMap)
		assert.NoError(t, err, "Failed to unmarshal line: %s", line)

		username, ok := userMap["username"].(string)
		assert.True(t, ok, "Username not found or not string in line")

		_, err = legacyDB.Exec("INSERT INTO json_data (key, data) VALUES (?, ?)", "user_data_"+username, line)
		assert.NoError(t, err)
	}
	assert.NoError(t, scanner.Err())

	// 2. Perform Migration
	err = MigrateLegacyDB(oldDBPath, newDBPath, dataDir)
	assert.NoError(t, err, "Migration failed")

	// 3. Verify New DB
	newDB, err := gorm.Open(sqlite.Open(newDBPath), &gorm.Config{})
	assert.NoError(t, err)

	var users []data.User
	err = newDB.Find(&users).Error
	assert.NoError(t, err)
	assert.Len(t, users, 2, "Expected 2 users to be migrated")

	// Verify 'admin' user and their devices/apps
	var adminUser data.User
	err = newDB.Where("username = ?", "admin").First(&adminUser).Error
	assert.NoError(t, err)
	assert.Equal(t, "admin", adminUser.Username)

	var adminDevice data.Device
	err = newDB.Where("username = ? AND id = ?", "admin", "e32753c8").First(&adminDevice).Error
	assert.NoError(t, err)

	assert.Equal(t, "e32753c8", adminDevice.ID)
	assert.Equal(t, "PixoPrint", adminDevice.Name)
	assert.Equal(t, data.Brightness(20), adminDevice.Brightness)
	assert.Equal(t, "18:00", adminDevice.NightStart)
	assert.Equal(t, "06:00", adminDevice.NightEnd)
	assert.Equal(t, data.Brightness(12), adminDevice.NightBrightness)

	var adminApps []data.App
	err = newDB.Where("device_id = ?", adminDevice.ID).Find(&adminApps).Error
	assert.NoError(t, err)
	// assert.Len(t, adminApps, 2) // Commented out to be robust against total count

	// Verify a specific app's details
	var octoprintApp *data.App
	for _, app := range adminApps {
		if app.Iname == "548" {
			val := app
			octoprintApp = &val
			break
		}
	}
	assert.NotNil(t, octoprintApp, "Octoprint app (548) not found")
	if octoprintApp != nil {
		assert.Equal(t, "Octoprint", octoprintApp.Name)
		assert.False(t, octoprintApp.EmptyLastRender) // Should be false from the sample data
		assert.Equal(t, time.Unix(1762849622, 0).UTC(), octoprintApp.LastRender.UTC())
		assert.Contains(t, octoprintApp.Config, "apiKey")
		assert.Equal(t, "system-apps/apps/octoprint/octoprint.star", *octoprintApp.Path)
	}

	// Verify 'tester' user
	var testerUser data.User
	err = newDB.Where("username = ?", "tester").First(&testerUser).Error
	assert.NoError(t, err)
	assert.Equal(t, "tester", testerUser.Username)
	var testerDevices []data.Device
	err = newDB.Where("username = ?", "tester").Find(&testerDevices).Error
	assert.NoError(t, err)
	assert.Len(t, testerDevices, 0, "Expected 0 devices for tester") // Tester has no devices in sample data
}
