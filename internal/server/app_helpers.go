package server

import (
	"context"
	"fmt"

	"tronbyt-server/internal/data"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// generateUniqueIname generates a unique 3-digit string for an app installation name (iname).
// It finds the maximum existing iname for the device and increments it.
func generateUniqueIname(db *gorm.DB, deviceID string) (string, error) {
	var maxIname int
	// Use COALESCE to handle case with no apps, defaulting to 99 so next is 100.
	// CAST(iname AS INTEGER) works in both SQLite and Postgres.
	err := gorm.G[data.App](db).
		Where("device_id = ?", deviceID).
		Select("COALESCE(MAX(CAST(iname AS INTEGER)), 99)").
		Scan(context.Background(), &maxIname)

	if err != nil {
		return "", fmt.Errorf("failed to get max iname: %w", err)
	}

	return fmt.Sprintf("%d", maxIname+1), nil
}

// getMaxAppOrder retrieves the current maximum order for apps on a specific device.
// It returns 0 if no apps exist or if an error (other than record not found) occurs.
func getMaxAppOrder(db *gorm.DB, deviceID string) (int, error) {
	var currentMax int
	// Use Order + Limit + Pluck which is dialect-agnostic and handles quoting
	// This approach is safer than Select("MAX(order)") or Select(clause.Expr) which caused issues.
	// If the column is indexed, performance is comparable to MAX().
	if err := db.Model(&data.App{}).
		Where("device_id = ?", deviceID).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "order"}, Desc: true}).
		Limit(1).
		Pluck("order", &currentMax).Error; err != nil && err != gorm.ErrRecordNotFound {
		return 0, fmt.Errorf("failed to get max app order: %w", err)
	}

	return currentMax, nil
}
