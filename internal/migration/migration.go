package migration

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/legacy"

	_ "github.com/mattn/go-sqlite3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// MigrateLegacyDB performs the migration from the old SQLite DB to the new GORM DB.
func MigrateLegacyDB(oldDBPath, newDBPath string) error {
	slog.Info("Migrating database", "old", oldDBPath, "new", newDBPath)

	// 1. Read Legacy Data
	users, err := readLegacyUsers(oldDBPath)
	if err != nil {
		return fmt.Errorf("failed to read legacy users: %w", err)
	}
	slog.Info("Found users to migrate", "count", len(users))

	// 2. Setup New DB
	newDB, err := gorm.Open(sqlite.Open(newDBPath), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to open new DB: %w", err)
	}

	// AutoMigrate schema
	err = newDB.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.WebAuthnCredential{})
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
	// Map User
	user := data.User{
		Username:        lUser.Username,
		Password:        lUser.Password,
		Email:           lUser.Email,
		APIKey:          lUser.APIKey,
		ThemePreference: data.ThemePreference(lUser.ThemePreference),
		SystemRepoURL:   lUser.SystemRepoURL,
		AppRepoURL:      lUser.AppRepoURL,
	}

	// Save User first
	if err := db.Create(&user).Error; err != nil {
		return fmt.Errorf("create user: %w", err)
	}

	// Map Devices
	for _, lDevice := range lUser.Devices {
		dev, err := mapDevice(lUser.Username, lDevice)
		if err != nil {
			slog.Warn("Skipping invalid device", "id", lDevice.ID, "error", err)
			continue
		}

		if err := db.Create(&dev).Error; err != nil {
			return fmt.Errorf("create device %s: %w", dev.ID, err)
		}
	}

	return nil
}

func mapDevice(username string, lDevice legacy.LegacyDevice) (data.Device, error) {
	// Handle Location
	var loc data.DeviceLocation
	if lDevice.Location != nil {
		loc.Locality = lDevice.Location.Locality
		loc.Description = lDevice.Location.Description
		loc.PlaceID = lDevice.Location.PlaceID
		loc.Lat = fmt.Sprintf("%v", lDevice.Location.Lat)
		loc.Lng = fmt.Sprintf("%v", lDevice.Location.Lng)
		if lDevice.Location.Timezone != nil {
			loc.Timezone = *lDevice.Location.Timezone
		}
	}

	// Handle Info
	var info data.DeviceInfo
	if lDevice.Info.FirmwareVersion != nil {
		info.FirmwareVersion = *lDevice.Info.FirmwareVersion
	}
	if lDevice.Info.FirmwareType != nil {
		info.FirmwareType = *lDevice.Info.FirmwareType
	}
	if lDevice.Info.ProtocolVersion != nil {
		info.ProtocolVersion = lDevice.Info.ProtocolVersion
	}
	if lDevice.Info.MacAddress != nil {
		info.MACAddress = *lDevice.Info.MacAddress
	}
	if lDevice.Info.ProtocolType != nil {
		info.ProtocolType = *lDevice.Info.ProtocolType
	}

	// Handle Brightness polymorphism
	brightness := data.Brightness(legacy.ParseBrightness(lDevice.Brightness))
	nightBrightness := data.Brightness(legacy.ParseBrightness(lDevice.NightBrightness))
	var dimBrightness *data.Brightness
	if lDevice.DimBrightness != nil {
		val := data.Brightness(legacy.ParseBrightness(lDevice.DimBrightness))
		dimBrightness = &val
	}

	nightStart := legacy.ParseTimeStr(lDevice.NightStart)
	nightEnd := legacy.ParseTimeStr(lDevice.NightEnd)

	// Color Filters
	var cf *data.ColorFilter
	if lDevice.ColorFilter != nil {
		val := data.ColorFilter(*lDevice.ColorFilter)
		cf = &val
	}
	var ncf *data.ColorFilter
	if lDevice.NightColorFilter != nil {
		val := data.ColorFilter(*lDevice.NightColorFilter)
		ncf = &val
	}

	dev := data.Device{
		ID:                    lDevice.ID,
		Username:              username,
		Name:                  lDevice.Name,
		Type:                  data.DeviceType(lDevice.Type),
		APIKey:                lDevice.APIKey,
		ImgURL:                lDevice.ImgURL,
		WsURL:                 lDevice.WsURL,
		Notes:                 lDevice.Notes,
		Brightness:            brightness,
		CustomBrightnessScale: lDevice.CustomBrightnessScale,
		NightModeEnabled:      lDevice.NightModeEnabled,
		NightModeApp:          lDevice.NightModeApp,
		NightStart:            nightStart,
		NightEnd:              nightEnd,
		NightBrightness:       nightBrightness,
		DimTime:               lDevice.DimTime,
		DimBrightness:         dimBrightness,
		DefaultInterval:       lDevice.DefaultInterval,
		Timezone:              lDevice.Timezone,
		Locale:                lDevice.Locale,
		Location:              loc,
		LastAppIndex:          lDevice.LastAppIndex,
		PinnedApp:             lDevice.PinnedApp,
		InterstitialEnabled:   lDevice.InterstitialEnabled,
		InterstitialApp:       lDevice.InterstitialApp,
		LastSeen:              lDevice.LastSeen,
		Info:                  info,
		ColorFilter:           cf,
		NightColorFilter:      ncf,
	}

	// Map Apps
	for _, lApp := range lDevice.Apps {
		app, err := mapApp(dev.ID, lApp)
		if err != nil {
			slog.Warn("Skipping invalid app", "iname", lApp.Iname, "error", err)
			continue
		}
		dev.Apps = append(dev.Apps, app)
	}

	return dev, nil
}

func mapApp(deviceID string, lApp legacy.LegacyApp) (data.App, error) {
	// Parse Time fields
	startTime := legacy.ParseTimeStr(lApp.StartTime)
	endTime := legacy.ParseTimeStr(lApp.EndTime)

	// Color Filter
	var cf *data.ColorFilter
	if lApp.ColorFilter != nil {
		val := data.ColorFilter(*lApp.ColorFilter)
		cf = &val
	}

	app := data.App{
		DeviceID:            deviceID,
		Iname:               lApp.Iname,
		Name:                lApp.Name,
		UInterval:           lApp.UInterval,
		DisplayTime:         lApp.DisplayTime,
		Notes:               lApp.Notes,
		Enabled:             lApp.Enabled,
		Pushed:              lApp.Pushed,
		Order:               lApp.Order,
		LastRender:          lApp.LastRender,
		LastRenderDur:       legacy.ParseDuration(lApp.LastRenderDuration),
		Path:                lApp.Path,
		StartTime:           &startTime,
		EndTime:             &endTime,
		Days:                data.StringSlice(lApp.Days),
		UseCustomRecurrence: lApp.UseCustomRecurrence,
		RecurrenceType:      data.RecurrenceType(lApp.RecurrenceType),
		RecurrenceInterval:  lApp.RecurrenceInterval,
		RecurrencePattern:   data.JSONMap(lApp.RecurrencePattern),
		RecurrenceStartDate: lApp.RecurrenceStartDate,
		RecurrenceEndDate:   lApp.RecurrenceEndDate,
		Config:              data.JSONMap(lApp.Config),
		EmptyLastRender:     lApp.EmptyLastRender,
		RenderMessages:      data.StringSlice(lApp.RenderMessages),
		AutoPin:             lApp.AutoPin,
		ColorFilter:         cf,
	}

	return app, nil
}
