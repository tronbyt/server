package server

import (
	"fmt"

	"tronbyt-server/internal/data"

	"gorm.io/gorm"
)

// generateUniqueIname generates a unique 3-digit string for an app installation name (iname).
// It finds the maximum existing iname for the device and increments it.
func generateUniqueIname(db *gorm.DB, deviceID string) (string, error) {
	var maxIname int
	// Use COALESCE to handle case with no apps, defaulting to 99 so next is 100.
	// CAST(iname AS INTEGER) works in both SQLite and Postgres.
	result := db.Model(&data.App{}).
		Where("device_id = ?", deviceID).
		Select("COALESCE(MAX(CAST(iname AS INTEGER)), 99)").
		Scan(&maxIname)

	if result.Error != nil {
		return "", fmt.Errorf("failed to get max iname: %w", result.Error)
	}

	return fmt.Sprintf("%d", maxIname+1), nil
}
