package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"tronbyt-server/internal/data"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"gorm.io/gorm"
)

func (s *Server) handleCreateDeviceGet(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)

	localizer := s.getLocalizer(r)
	s.renderTemplate(w, r, "create", TemplateData{
		User:              user,
		DeviceTypeChoices: s.getDeviceTypeChoices(localizer),
		Localizer:         localizer,
		Form:              CreateDeviceFormData{Brightness: data.Brightness(20).UIScale(nil)}, // Default brightness 20%
	})
}

func (s *Server) handleCreateDevicePost(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)

	// Parse form data
	formData := CreateDeviceFormData{
		Name:           r.FormValue("name"),
		DeviceType:     r.FormValue("device_type"),
		ImgURL:         r.FormValue("img_url"),
		WsURL:          r.FormValue("ws_url"),
		Notes:          r.FormValue("notes"),
		LocationJSON:   r.FormValue("location"),
		LocationSearch: r.FormValue("location_search"), // Used for re-populating form
	}

	brightnessStr := r.FormValue("brightness")
	if brightness, err := strconv.Atoi(brightnessStr); err == nil {
		formData.Brightness = brightness
	} else {
		formData.Brightness = 3 // Default
	}

	// Validation
	localizer := s.getLocalizer(r)

	if formData.Name == "" {
		// Flash message
		slog.Warn("Validation error: Device name required")
		s.renderTemplate(w, r, "create", TemplateData{
			User:              user,
			Flashes:           []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Name is required."})},
			DeviceTypeChoices: s.getDeviceTypeChoices(localizer),
			Localizer:         localizer,
			Form:              formData,
		})
		return
	}

	// Check if device name already exists for this user
	for _, dev := range user.Devices {
		if dev.Name == formData.Name {
			slog.Warn("Validation error: Device name already exists", "name", formData.Name)
			s.renderTemplate(w, r, "create", TemplateData{
				User:              user,
				Flashes:           []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Name already exists."})},
				DeviceTypeChoices: s.getDeviceTypeChoices(localizer),
				Localizer:         localizer,
				Form:              formData,
			})
			return
		}
	}

	// Generate unique ID and API Key
	deviceID, err := generateSecureToken(8)
	if err != nil {
		slog.Error("Failed to generate device ID", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	apiKey, err := generateSecureToken(32)
	if err != nil {
		slog.Error("Failed to generate API key", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Parse location JSON
	var location data.DeviceLocation
	if formData.LocationJSON != "" && formData.LocationJSON != "{}" {
		var locMap map[string]any
		if err := json.Unmarshal([]byte(formData.LocationJSON), &locMap); err == nil {
			safeStr := func(v any) string {
				if s, ok := v.(string); ok {
					return s
				}
				if f, ok := v.(float64); ok {
					return fmt.Sprintf("%v", f)
				}
				return ""
			}
			safeFloat := func(v any) float64 {
				if f, ok := v.(float64); ok {
					return f
				}
				if s, ok := v.(string); ok {
					if f, err := strconv.ParseFloat(s, 64); err == nil {
						return f
					}
				}
				return 0
			}
			location.Description = safeStr(locMap["description"])
			location.Lat = safeFloat(locMap["lat"])
			location.Lng = safeFloat(locMap["lng"])
			location.Locality = safeStr(locMap["locality"])
			location.PlaceID = safeStr(locMap["place_id"])
			location.Timezone = safeStr(locMap["timezone"])
		} else {
			slog.Warn("Invalid location JSON", "error", err)
		}
	}

	// Create new device
	newDevice := data.Device{
		ID:                    deviceID,
		Username:              user.Username,
		Name:                  formData.Name,
		Type:                  data.DeviceType(formData.DeviceType),
		APIKey:                apiKey,
		ImgURL:                formData.ImgURL, // Can be overridden by default logic later
		WsURL:                 formData.WsURL,  // Can be overridden by default logic later
		Notes:                 formData.Notes,
		Brightness:            data.Brightness(formData.Brightness),
		CustomBrightnessScale: "",
		NightBrightness:       0,
		DefaultInterval:       15,
		Location:              location,
		LastAppIndex:          0,
		InterstitialEnabled:   false,
	}

	// Default to 'None' color filter
	defaultColorFilter := data.ColorFilterNone
	newDevice.ColorFilter = &defaultColorFilter

	// Set default ImgURL and WsURL if empty
	if newDevice.ImgURL == "" {
		newDevice.ImgURL = fmt.Sprintf("/%s/next", newDevice.ID)
	}
	// Need to determine absolute path for WS. For now, relative.
	if newDevice.WsURL == "" {
		newDevice.WsURL = fmt.Sprintf("/%s/ws", newDevice.ID)
	}

	// Save to DB
	if err := s.DB.Create(&newDevice).Error; err != nil {
		slog.Error("Failed to save new device to DB", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Create device webp directory
	deviceWebpDir := s.getDeviceWebPDir(newDevice.ID)
	if err := os.MkdirAll(deviceWebpDir, 0755); err != nil {
		slog.Error("Failed to create device webp directory", "path", deviceWebpDir, "error", err)
		// Not a fatal error, but log it
	}

	// Redirect to dashboard
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleUpdateDeviceGet(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)

	// Parse custom brightness scale if device has one
	var customScale map[int]int
	if device.CustomBrightnessScale != "" {
		customScale = data.ParseCustomBrightnessScale(device.CustomBrightnessScale)
	}

	// Calculate UI Brightness
	bUI := device.Brightness.UIScale(customScale)

	// Calculate Night Brightness UI
	nbUI := device.NightBrightness.UIScale(customScale)

	// Calculate Dim Brightness UI
	dbUI := 2 // Default
	if device.DimBrightness != nil {
		dbUI = (*device.DimBrightness).UIScale(customScale)
	}

	// Get available locales
	locales := []string{"en_US", "de_DE"} // Add more as needed or scan directory
	localizer := s.getLocalizer(r)

	// Determine scheme and host for default URLs
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	wsScheme := "ws"
	if scheme == "https" {
		wsScheme = "wss"
	}
	host := r.Host

	s.renderTemplate(w, r, "update", TemplateData{
		User:               user,
		Device:             device,
		DeviceTypeChoices:  s.getDeviceTypeChoices(localizer),
		ColorFilterOptions: s.getColorFilterChoices(),
		AvailableLocales:   locales,
		DefaultImgURL:      fmt.Sprintf("%s://%s/%s/next", scheme, host, device.ID),
		DefaultWsURL:       fmt.Sprintf("%s://%s/%s/ws", wsScheme, host, device.ID),
		BrightnessUI:       bUI,
		NightBrightnessUI:  nbUI,
		DimBrightnessUI:    dbUI,

		Localizer: localizer,
	})
}

func (s *Server) handleUpdateDevicePost(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

	// 1. Basic Info
	name := r.FormValue("name")
	if name == "" {
		s.flashAndRedirect(w, r, "Name is required.", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
		return
	}
	device.Name = name
	device.Type = data.DeviceType(r.FormValue("device_type"))
	device.ImgURL = s.sanitizeURL(r.FormValue("img_url"))
	if device.ImgURL == "" {
		device.ImgURL = fmt.Sprintf("/%s/next", device.ID)
	}
	device.WsURL = s.sanitizeURL(r.FormValue("ws_url"))
	if device.WsURL == "" {
		device.WsURL = fmt.Sprintf("/%s/ws", device.ID)
	}
	device.Notes = r.FormValue("notes")

	if i, err := strconv.Atoi(r.FormValue("default_interval")); err == nil {
		device.DefaultInterval = i
	}

	// 2. Color Filter
	colorFilter := r.FormValue("color_filter")
	if colorFilter != "none" {
		val := data.ColorFilter(colorFilter)
		device.ColorFilter = &val
	} else {
		device.ColorFilter = nil
	}

	// 3. Brightness & Scale
	useCustomScale := r.FormValue("use_custom_brightness_scale") == "on"
	customScaleStr := r.FormValue("custom_brightness_scale")
	if useCustomScale {
		if data.ParseCustomBrightnessScale(customScaleStr) == nil {
			s.flashAndRedirect(w, r, "Invalid custom brightness scale format. Use 6 comma-separated values (0-100).", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
			return
		}
		device.CustomBrightnessScale = customScaleStr
	} else {
		device.CustomBrightnessScale = ""
	}

	// Parse Scale
	var customScale map[int]int
	if device.CustomBrightnessScale != "" {
		customScale = data.ParseCustomBrightnessScale(device.CustomBrightnessScale)
	}

	if bUI, err := strconv.Atoi(r.FormValue("brightness")); err == nil {
		device.Brightness = data.BrightnessFromUIScale(bUI, customScale)
	}

	// 4. Interstitial
	device.InterstitialEnabled = r.FormValue("interstitial_enabled") == "on"
	interstitialApp := r.FormValue("interstitial_app")
	if interstitialApp != "None" {
		exists := false
		for _, app := range device.Apps {
			if app.Iname == interstitialApp {
				exists = true
				break
			}
		}
		if !exists {
			slog.Warn("Interstitial app not found", "app", interstitialApp)
		}
		device.InterstitialApp = &interstitialApp
	} else {
		device.InterstitialApp = nil
	}

	// 5. Night Mode
	device.NightModeEnabled = r.FormValue("night_mode_enabled") == "on"

	nightStart := r.FormValue("night_start")
	if nightStart != "" {
		parsed, err := parseTimeInput(nightStart)
		if err != nil {
			s.flashAndRedirect(w, r, fmt.Sprintf("Invalid night start time: %v", err), fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
			return
		}
		device.NightStart = parsed
	}

	nightEnd := r.FormValue("night_end")
	if nightEnd != "" {
		parsed, err := parseTimeInput(nightEnd)
		if err != nil {
			s.flashAndRedirect(w, r, fmt.Sprintf("Invalid night end time: %v", err), fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
			return
		}
		device.NightEnd = parsed
	}

	if nbUI, err := strconv.Atoi(r.FormValue("night_brightness")); err == nil {
		device.NightBrightness = data.BrightnessFromUIScale(nbUI, customScale)
	}

	nightApp := r.FormValue("night_mode_app")
	if nightApp != "None" {
		exists := false
		for _, app := range device.Apps {
			if app.Iname == nightApp {
				exists = true
				break
			}
		}
		if !exists {
			slog.Warn("Night mode app not found", "app", nightApp)
		}
		device.NightModeApp = nightApp
	} else {
		device.NightModeApp = ""
	}

	nightColorFilter := r.FormValue("night_color_filter")
	if nightColorFilter != "none" {
		val := data.ColorFilter(nightColorFilter)
		device.NightColorFilter = &val
	} else {
		device.NightColorFilter = nil
	}

	// 6. Dim Mode
	dimTime := r.FormValue("dim_time")
	if dimTime != "" {
		parsed, err := parseTimeInput(dimTime)
		if err != nil {
			s.flashAndRedirect(w, r, fmt.Sprintf("Invalid dim time: %v", err), fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
			return
		}
		device.DimTime = &parsed
	} else {
		device.DimTime = nil
	}

	if dimUI, err := strconv.Atoi(r.FormValue("dim_brightness")); err == nil {
		val := data.BrightnessFromUIScale(dimUI, customScale)
		device.DimBrightness = &val
	}

	// 7. Location & Locale
	locationJSON := r.FormValue("location")
	if locationJSON != "" && locationJSON != "{}" {
		var locMap map[string]any
		if err := json.Unmarshal([]byte(locationJSON), &locMap); err == nil {
			safeStr := func(v any) string {
				if s, ok := v.(string); ok {
					return s
				}
				if f, ok := v.(float64); ok {
					return fmt.Sprintf("%v", f)
				}
				return ""
			}
			safeFloat := func(v any) float64 {
				if f, ok := v.(float64); ok {
					return f
				}
				if s, ok := v.(string); ok {
					if f, err := strconv.ParseFloat(s, 64); err == nil {
						return f
					}
				}
				return 0
			}
			device.Location = data.DeviceLocation{
				Description: safeStr(locMap["description"]),
				Lat:         safeFloat(locMap["lat"]),
				Lng:         safeFloat(locMap["lng"]),
				Locality:    safeStr(locMap["locality"]),
				PlaceID:     safeStr(locMap["place_id"]),
				Timezone:    safeStr(locMap["timezone"]),
			}
		} else {
			s.flashAndRedirect(w, r, fmt.Sprintf("Location JSON error: %v", err), fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
			return
		}
	}
	locale := r.FormValue("locale")
	if locale != "" {
		device.Locale = &locale
	} else {
		device.Locale = nil
	}

	// 8. API Key
	device.APIKey = r.FormValue("api_key")

	if err := s.DB.Save(device).Error; err != nil {
		slog.Error("Failed to update device", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

	// Clean up files
	if err := os.RemoveAll(s.getDeviceWebPDir(device.ID)); err != nil {
		slog.Error("Failed to remove device webp directory", "device_id", device.ID, "error", err)
	}

	// Cascading delete in transaction
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Delete Apps
		if err := tx.Where("device_id = ?", device.ID).Delete(&data.App{}).Error; err != nil {
			return err
		}
		// 2. Delete Device
		if err := tx.Delete(device).Error; err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		slog.Error("Failed to delete device", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleExportDeviceConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user := GetUser(r)

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}

	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s_config.json", device.Name))

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(device); err != nil {
		slog.Error("Failed to export device config", "error", err)
	}
}

func (s *Server) handleImportDeviceConfig(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)

	// Max 1MB file size
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		slog.Error("Failed to parse multipart form for device import", "error", err)
		http.Error(w, "File upload failed: invalid form data", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		slog.Error("Failed to get uploaded file for device import", "error", err)
		http.Error(w, "File upload failed", http.StatusBadRequest)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Error("Failed to close uploaded device config file", "error", err)
		}
	}()

	var importedDevice data.Device
	if err := json.NewDecoder(file).Decode(&importedDevice); err != nil {
		slog.Error("Failed to decode imported device JSON", "error", err)
		http.Error(w, "Invalid JSON file", http.StatusBadRequest)
		return
	}

	// Clean up WebP files for this device to prevent orphans
	if err := os.RemoveAll(s.getDeviceWebPDir(device.ID)); err != nil {
		slog.Error("Failed to clear device webp directory during import", "device_id", device.ID, "error", err)
	}
	// Re-create the directory immediately
	s.getDeviceWebPDir(device.ID)

	// Begin a transaction to ensure atomicity
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Delete existing apps for this device
		if err := tx.Where("device_id = ?", device.ID).Delete(&data.App{}).Error; err != nil {
			return fmt.Errorf("failed to delete existing apps: %w", err)
		}

		// 2. Update device fields with imported data (excluding ID, Username, APIKey)
		device.Name = importedDevice.Name
		device.Type = importedDevice.Type
		device.ImgURL = importedDevice.ImgURL
		device.WsURL = importedDevice.WsURL
		device.Notes = importedDevice.Notes
		device.Brightness = importedDevice.Brightness
		device.CustomBrightnessScale = importedDevice.CustomBrightnessScale
		device.NightModeEnabled = importedDevice.NightModeEnabled
		device.NightModeApp = importedDevice.NightModeApp
		device.NightStart = importedDevice.NightStart
		device.NightEnd = importedDevice.NightEnd
		device.NightBrightness = importedDevice.NightBrightness
		device.DimTime = importedDevice.DimTime
		device.DimBrightness = importedDevice.DimBrightness
		device.DefaultInterval = importedDevice.DefaultInterval
		device.Timezone = importedDevice.Timezone
		device.Locale = importedDevice.Locale
		device.Location = importedDevice.Location
		device.LastAppIndex = importedDevice.LastAppIndex
		device.PinnedApp = importedDevice.PinnedApp
		device.InterstitialEnabled = importedDevice.InterstitialEnabled
		device.InterstitialApp = importedDevice.InterstitialApp
		device.LastSeen = importedDevice.LastSeen
		device.Info = importedDevice.Info
		device.ColorFilter = importedDevice.ColorFilter
		device.NightColorFilter = importedDevice.NightColorFilter

		if err := tx.Save(device).Error; err != nil {
			return fmt.Errorf("failed to save updated device: %w", err)
		}

		// 3. Create new apps from imported device
		for _, app := range importedDevice.Apps {
			app.DeviceID = device.ID // Ensure DeviceID is set to the current device's ID
			app.ID = 0               // GORM will assign a new primary key
			if err := tx.Create(&app).Error; err != nil {
				return fmt.Errorf("failed to create imported app '%s': %w", app.Name, err)
			}
		}

		return nil
	})

	if err != nil {
		slog.Error("Device import transaction failed", "device_id", device.ID, "error", err)
		http.Error(w, fmt.Sprintf("Import failed: %s", err.Error()), http.StatusInternalServerError)
		return
	}

	slog.Info("Device config imported successfully", "device_id", device.ID, "username", user.Username)
	http.Redirect(w, r, fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
}
