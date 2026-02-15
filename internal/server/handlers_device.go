package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"tronbyt-server/internal/data"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"gorm.io/gorm"
)

var validDeviceIDRe = regexp.MustCompile(`^[a-zA-Z0-9]+$`)

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
		DeviceID:       r.FormValue("device_id"),
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

	// Validate and check custom device ID if provided
	if formData.DeviceID != "" {
		// Validate device ID format (alphanumeric only)
		if !validDeviceIDRe.MatchString(formData.DeviceID) {
			slog.Warn("Validation error: Device ID contains invalid characters", "device_id", formData.DeviceID)
			s.renderTemplate(w, r, "create", TemplateData{
				User:              user,
				Flashes:           []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Invalid Device ID."})},
				DeviceTypeChoices: s.getDeviceTypeChoices(localizer),
				Localizer:         localizer,
				Form:              formData,
			})
			return
		}

		// Check if device ID already exists (across all users)
		if _, err := gorm.G[data.Device](s.DB).Where("id = ?", formData.DeviceID).First(r.Context()); err == nil {
			// Device ID already exists
			slog.Warn("Validation error: Device ID already exists", "device_id", formData.DeviceID)
			s.renderTemplate(w, r, "create", TemplateData{
				User:              user,
				Flashes:           []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Invalid Device ID."})},
				DeviceTypeChoices: s.getDeviceTypeChoices(localizer),
				Localizer:         localizer,
				Form:              formData,
			})
			return
		}
	}

	// Generate unique ID and API Key
	var deviceID string
	if formData.DeviceID != "" {
		deviceID = formData.DeviceID
	} else {
		var err error
		deviceID, err = generateSecureToken(8)
		if err != nil {
			slog.Error("Failed to generate device ID", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
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

	deviceType, ok := data.StringToDeviceType[formData.DeviceType]
	if !ok {
		deviceType = data.DeviceOther
	}

	// Create new device
	newDevice := data.Device{
		ID:                    deviceID,
		Username:              user.Username,
		Name:                  formData.Name,
		Type:                  deviceType,
		APIKey:                apiKey,
		ImgURL:                formData.ImgURL, // Can be overridden by default logic later
		WsURL:                 formData.WsURL,  // Can be overridden by default logic later
		Notes:                 formData.Notes,
		Brightness:            data.BrightnessFromUIScale(formData.Brightness, nil),
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
		newDevice.ImgURL = s.getImageURL(r, newDevice.ID)
	}
	if newDevice.WsURL == "" {
		newDevice.WsURL = s.getWebsocketURL(r, newDevice.ID)
	}

	// Save to DB
	if err := gorm.G[data.Device](s.DB).Create(r.Context(), &newDevice); err != nil {
		slog.Error("Failed to save new device to DB", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Create device webp directory
	_, err = s.ensureDeviceImageDir(newDevice.ID)
	if err != nil {
		slog.Error("Failed to get or create device webp directory", "device_id", newDevice.ID, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Redirect to dashboard
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDeviceTV(w http.ResponseWriter, r *http.Request) {
    user := GetUser(r)
    localizer := s.getLocalizer(r)
    device := GetDevice(r) // Middleware provides this

    s.renderTemplate(w, r, "device_tv", TemplateData{
        User:      user,
        Localizer: localizer,
        Device:    device, 
    })
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

	// Check if specific firmware exists for this device
	availableVersions := s.GetAvailableFirmwareVersions()
	firmwareAvailable := len(availableVersions) > 0
	firmwareVersion := "Unknown"
	if firmwareAvailable {
		firmwareVersion = availableVersions[0]
	}

	// Check if URL contains localhost
	imgURL := device.ImgURL
	var urlWarning string
	if strings.Contains(imgURL, "localhost") || strings.Contains(imgURL, "127.0.0.1") {
		urlWarning = "localhost"
	}
	s.renderTemplate(w, r, "update", TemplateData{
		User:                      user,
		Device:                    device,
		DeviceTypeChoices:         s.getDeviceTypeChoices(localizer),
		ColorFilterOptions:        s.getColorFilterChoices(),
		AvailableLocales:          locales,
		DefaultImgURL:             s.getImageURL(r, device.ID),
		DefaultWsURL:              s.getWebsocketURL(r, device.ID),
		BrightnessUI:              bUI,
		NightBrightnessUI:         nbUI,
		DimBrightnessUI:           dbUI,
		FirmwareAvailable:         firmwareAvailable,
		FirmwareVersion:           firmwareVersion,
		AvailableFirmwareVersions: availableVersions,
		Localizer:                 localizer,
		URLWarning:                urlWarning,
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
	if dt, ok := data.StringToDeviceType[r.FormValue("device_type")]; ok {
		device.Type = dt
	} else {
		device.Type = data.DeviceOther
	}
	device.ImgURL = s.sanitizeURL(r.FormValue("img_url"))
	if device.ImgURL == "" {
		device.ImgURL = s.getImageURL(r, device.ID)
	}
	device.WsURL = s.sanitizeURL(r.FormValue("ws_url"))
	if device.WsURL == "" {
		device.WsURL = s.getWebsocketURL(r, device.ID)
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
	device.DimModeEnabled = r.FormValue("dim_mode_enabled") == "on"

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

	dimColorFilter := r.FormValue("dim_color_filter")
	if dimColorFilter != "none" {
		val := data.ColorFilter(dimColorFilter)
		device.DimColorFilter = &val
	} else {
		device.DimColorFilter = nil
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

	// 9. OTA
	device.SwapColors = r.FormValue("swap_colors") == "on"

	if err := s.DB.Omit("Apps").Save(device).Error; err != nil {
		slog.Error("Failed to update device", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	user := GetUser(r)
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

	// Clean up files
	deviceWebpDir, err := s.ensureDeviceImageDir(device.ID)
	if err != nil {
		slog.Error("Failed to get device webp directory for deletion", "device_id", device.ID, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if err := os.RemoveAll(deviceWebpDir); err != nil {
		slog.Error("Failed to remove device webp directory", "device_id", device.ID, "error", err)
	}

	// Cascading delete in transaction
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Delete Apps
		if _, err := gorm.G[data.App](tx).Where("device_id = ?", device.ID).Delete(r.Context()); err != nil {
			return err
		}
		// 2. Delete Device
		if _, err := gorm.G[data.Device](tx).Where("id = ?", device.ID).Delete(r.Context()); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		slog.Error("Failed to delete device", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	user := GetUser(r)
	s.notifyDashboard(user.Username, WSEvent{Type: "device_deleted", DeviceID: device.ID})

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
		s.flashAndRedirect(w, r, "File upload failed: invalid form data", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		slog.Error("Failed to get uploaded file for device import", "error", err)
		s.flashAndRedirect(w, r, "File upload failed", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
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
		s.flashAndRedirect(w, r, "Invalid JSON file", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
		return
	}

	// Clean up WebP files for this device to prevent orphans
	deviceWebpDir, err := s.ensureDeviceImageDir(device.ID)
	if err != nil {
		slog.Error("Failed to get device webp directory for cleanup during import", "device_id", device.ID, "error", err)
		s.flashAndRedirect(w, r, "Import failed: internal server error.", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
		return
	}

	if err := os.RemoveAll(deviceWebpDir); err != nil {
		slog.Error("Failed to clear device webp directory during import", "device_id", device.ID, "error", err)
	}
	// Re-create the directory immediately
	_, err = s.ensureDeviceImageDir(device.ID)
	if err != nil {
		slog.Error("Failed to re-create device webp directory during import", "device_id", device.ID, "error", err)
		s.flashAndRedirect(w, r, "Import failed: internal server error.", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
		return
	}

	// Regenerate URLs
	importedDevice.ImgURL = s.getImageURL(r, device.ID)
	importedDevice.WsURL = s.getWebsocketURL(r, device.ID)

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
		device.DimColorFilter = importedDevice.DimColorFilter
		device.SwapColors = importedDevice.SwapColors

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
		s.flashAndRedirect(w, r, "Import failed. See server logs for details.", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
		return
	}

	slog.Info("Device config imported successfully", "device_id", device.ID, "username", user.Username)
	s.flashAndRedirect(w, r, "Device configuration imported successfully", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
}

func (s *Server) handleUpdateBrightness(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

	brightnessStr := r.FormValue("brightness")
	bUI, err := strconv.Atoi(brightnessStr)
	if err != nil || bUI < 0 || bUI > 5 {
		slog.Warn("Invalid brightness value", "device", device.ID, "value", brightnessStr)
		http.Error(w, "Brightness must be between 0 and 5", http.StatusBadRequest)
		return
	}

	// Parse Scale
	var customScale map[int]int
	if device.CustomBrightnessScale != "" {
		customScale = data.ParseCustomBrightnessScale(device.CustomBrightnessScale)
	}

	device.Brightness = data.BrightnessFromUIScale(bUI, customScale)

	if err := s.DB.Omit("Apps").Save(device).Error; err != nil {
		slog.Error("Failed to update device brightness", "device", device.ID, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Notify Device (Websocket)
	s.Broadcaster.Notify(device.ID, nil)

	// Notify Dashboard
	user := GetUser(r)
	s.notifyDashboard(user.Username, WSEvent{
		Type:     "device_updated",
		DeviceID: device.ID,
		Payload:  map[string]any{"brightness": bUI},
	})

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleUpdateInterval(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

	intervalStr := r.FormValue("interval")
	interval, err := strconv.Atoi(intervalStr)
	if err != nil || interval < 1 {
		slog.Warn("Invalid interval value", "device", device.ID, "value", intervalStr)
		http.Error(w, "Interval must be 1 or greater", http.StatusBadRequest)
		return
	}

	device.DefaultInterval = interval

	if err := s.DB.Omit("Apps").Save(device).Error; err != nil {
		slog.Error("Failed to update device interval", "device", device.ID, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Notify Dashboard
	user := GetUser(r)
	s.notifyDashboard(user.Username, WSEvent{
		Type:     "device_updated",
		DeviceID: device.ID,
		Payload:  map[string]any{"interval": interval},
	})

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleImportNewDeviceConfig(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)

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
		s.flashAndRedirect(w, r, "Invalid JSON file", "/devices/create", http.StatusSeeOther)
		return
	}

	localizer := s.getLocalizer(r)

	// Validate imported device name for consistency with device creation form.
	if importedDevice.Name == "" {
		slog.Warn("Validation error: Imported device name empty during import")
		s.flashAndRedirect(w, r, localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Imported device name cannot be empty."}), "/devices/create", http.StatusSeeOther)
		return
	}

	for _, dev := range user.Devices {
		if dev.Name == importedDevice.Name {
			slog.Warn("Validation error: Imported device name already exists", "name", importedDevice.Name)
			s.flashAndRedirect(w, r, localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "A device with this name already exists."}), "/devices/create", http.StatusSeeOther)
			return
		}
	}

	// Determine Device ID
	var deviceID string
	if importedDevice.ID != "" {
		// Validate ID format
		if validDeviceIDRe.MatchString(importedDevice.ID) {
			// Check if exists
			exists, _ := gorm.G[data.Device](s.DB).Where("id = ?", importedDevice.ID).Count(r.Context(), "*")
			if exists == 0 {
				deviceID = importedDevice.ID
			}
		}
	}
	if deviceID == "" {
		var err error
		deviceID, err = generateSecureToken(8)
		if err != nil {
			slog.Error("Failed to generate device ID", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}

	// Determine API Key
	apiKey := importedDevice.APIKey
	if apiKey != "" {
		// Check for uniqueness
		exists, _ := gorm.G[data.Device](s.DB).Where("api_key = ?", apiKey).Count(r.Context(), "*")
		if exists > 0 {
			apiKey = "" // Collision, generate new
		} else {
			// Check User API keys too?
			exists, _ := gorm.G[data.User](s.DB).Where("api_key = ?", apiKey).Count(r.Context(), "*")
			if exists > 0 {
				apiKey = ""
			}
		}
	}
	if apiKey == "" {
		var err error
		apiKey, err = generateSecureToken(32)
		if err != nil {
			slog.Error("Failed to generate API key", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}

	// Prepare new device
	newDevice := importedDevice
	newDevice.ID = deviceID
	newDevice.Username = user.Username
	newDevice.APIKey = apiKey
	newDevice.Apps = nil // Clear apps for now, we'll insert them separately

	// Set default ImgURL and WsURL if empty (or if imported ones are specific to old device)
	// It's safer to regenerate them for the new ID
	newDevice.ImgURL = s.getImageURL(r, newDevice.ID)
	newDevice.WsURL = s.getWebsocketURL(r, newDevice.ID)

	// Save to DB in transaction
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&newDevice).Error; err != nil {
			return fmt.Errorf("failed to create device: %w", err)
		}

		// Create apps
		for _, app := range importedDevice.Apps {
			app.DeviceID = newDevice.ID
			app.ID = 0 // Reset ID to allow GORM to generate new one
			if err := tx.Create(&app).Error; err != nil {
				return fmt.Errorf("failed to create app '%s': %w", app.Name, err)
			}
		}
		return nil
	})

	if err != nil {
		slog.Error("New device import transaction failed", "error", err)
		http.Error(w, "Import failed", http.StatusInternalServerError)
		return
	}

	// Create device webp directory
	_, err = s.ensureDeviceImageDir(newDevice.ID)
	if err != nil {
		slog.Error("Failed to get or create device webp directory", "device_id", newDevice.ID, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	slog.Info("New device imported successfully", "device_id", newDevice.ID, "username", user.Username)
	s.flashAndRedirect(w, r, "New device imported successfully", "/", http.StatusSeeOther)
}

func (s *Server) handleRebootDevice(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

	if err := s.sendRebootCommand(device.ID); err != nil {
		slog.Error("Failed to send reboot command", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	localizer := s.getLocalizer(r)
	msg := localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Reboot command sent to device."})
	s.flashAndRedirect(w, r, msg, fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
}

func (s *Server) handleUpdateFirmwareSettings(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)
	payload := make(map[string]any)

	// Boolean fields
	boolFields := []string{"skip_display_version", "prefer_ipv6", "ap_mode", "swap_colors"}
	for _, field := range boolFields {
		if val := r.FormValue(field); val != "" {
			payload[field] = val == "true"
		}
	}

	// Integer fields
	if val := r.FormValue("wifi_power_save"); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			payload["wifi_power_save"] = i
		}
	}

	// String fields - Non-empty
	nonEmptyStringFields := []string{"image_url", "hostname"}
	for _, field := range nonEmptyStringFields {
		if val := r.FormValue(field); val != "" {
			payload[field] = val
		}
	}

	// String fields - Nullable (can be empty to clear)
	nullableStringFields := []string{"sntp_server", "syslog_addr"}
	for _, field := range nullableStringFields {
		val := r.FormValue(field)
		if _, ok := r.Form[field]; ok {
			payload[field] = val
		}
	}

	if len(payload) == 0 {
		http.Error(w, "No settings provided", http.StatusBadRequest)
		return
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to marshal firmware settings payload", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	s.Broadcaster.Notify(device.ID, DeviceCommandMessage{Payload: jsonPayload})

	w.WriteHeader(http.StatusOK)
}
