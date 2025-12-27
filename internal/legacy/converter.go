package legacy

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"tronbyt-server/internal/data"
)

var durationRegex = regexp.MustCompile(`^PT(?:(\d+(?:\.\d+)?)S)?$`)

// ToDataUser converts a LegacyUser to the modern data.User format.
func (lu *LegacyUser) ToDataUser() data.User {
	var emailPtr *string
	if lu.Email != "" && lu.Email != "none" {
		emailPtr = &lu.Email
	}

	user := data.User{
		Username:        lu.Username,
		Password:        lu.Password,
		Email:           emailPtr,
		APIKey:          lu.APIKey,
		ThemePreference: data.ThemePreference(lu.ThemePreference),
		SystemRepoURL:   lu.SystemRepoURL,
		AppRepoURL:      lu.AppRepoURL,
	}

	for _, ld := range lu.Devices {
		user.Devices = append(user.Devices, ld.ToDataDevice(lu.Username))
	}

	return user
}

// ToDataDevice converts a LegacyDevice to the modern data.Device format.
func (ld *LegacyDevice) ToDataDevice(username string) data.Device {
	deviceType, ok := data.StringToDeviceType[ld.Type]
	if !ok {
		deviceType = data.DeviceOther
	}

	nd := data.Device{
		ID:                    ld.ID,
		Username:              username,
		Name:                  ld.Name,
		Type:                  deviceType,
		APIKey:                ld.APIKey,
		ImgURL:                ld.ImgURL,
		WsURL:                 ld.WsURL,
		Notes:                 ld.Notes,
		CustomBrightnessScale: ld.CustomBrightnessScale,
		NightModeEnabled:      ld.NightModeEnabled,
		NightModeApp:          ld.NightModeApp,
		NightStart:            ParseTimeStr(ld.NightStart),
		NightEnd:              ParseTimeStr(ld.NightEnd),
		DefaultInterval:       ld.DefaultInterval,
		LastAppIndex:          ld.LastAppIndex,
		InterstitialEnabled:   ld.InterstitialEnabled,
	}

	// Brightness
	nd.Brightness = data.Brightness(ParseBrightness(ld.Brightness))
	nd.NightBrightness = data.Brightness(ParseBrightness(ld.NightBrightness))
	if ld.DimBrightness != nil {
		v := data.Brightness(ParseBrightness(ld.DimBrightness))
		nd.DimBrightness = &v
	}

	if ld.DimTime != nil {
		nd.DimTime = ld.DimTime
	}
	if ld.Timezone != nil {
		nd.Timezone = ld.Timezone
	}
	if ld.Locale != nil {
		nd.Locale = ld.Locale
	}
	if ld.PinnedApp != nil {
		nd.PinnedApp = ld.PinnedApp
	}
	if ld.InterstitialApp != nil {
		nd.InterstitialApp = ld.InterstitialApp
	}
	if ld.LastSeen != nil {
		nd.LastSeen = ld.LastSeen
	}

	// Location
	if ld.Location != nil {
		nd.Location = data.DeviceLocation{
			Locality:    ld.Location.Locality,
			Description: ld.Location.Description,
			PlaceID:     ld.Location.PlaceID,
			Lat:         ParseFloat(ld.Location.Lat),
			Lng:         ParseFloat(ld.Location.Lng),
		}
		if ld.Location.Timezone != nil {
			nd.Location.Timezone = *ld.Location.Timezone
		}
	}

	// Info
	if ld.Info.FirmwareVersion != nil {
		nd.Info.FirmwareVersion = *ld.Info.FirmwareVersion
	}
	if ld.Info.FirmwareType != nil {
		nd.Info.FirmwareType = *ld.Info.FirmwareType
	}
	if ld.Info.ProtocolVersion != nil {
		nd.Info.ProtocolVersion = ld.Info.ProtocolVersion
	}
	if ld.Info.MacAddress != nil {
		nd.Info.MACAddress = *ld.Info.MacAddress
	}
	if ld.Info.ProtocolType != nil {
		nd.Info.ProtocolType = data.ProtocolType(*ld.Info.ProtocolType)
	}

	// Color Filters
	if ld.ColorFilter != nil {
		cf := data.ColorFilter(*ld.ColorFilter)
		nd.ColorFilter = &cf
	}
	if ld.NightColorFilter != nil {
		cf := data.ColorFilter(*ld.NightColorFilter)
		nd.NightColorFilter = &cf
	}

	// Map Apps
	for _, rawApp := range ld.Apps {
		var la LegacyApp
		if err := json.Unmarshal(rawApp, &la); err != nil {
			slog.Warn("failed to unmarshal legacy app, skipping", "device_id", nd.ID, "error", err)
			continue
		}
		nd.Apps = append(nd.Apps, la.ToDataApp(nd.ID))
	}

	// Sort apps by order since map iteration is random
	sort.Slice(nd.Apps, func(i, j int) bool {
		return nd.Apps[i].Order < nd.Apps[j].Order
	})

	return nd
}

// ToDataApp converts a LegacyApp to the modern data.App format.
func (la *LegacyApp) ToDataApp(deviceID string) data.App {
	na := data.App{
		DeviceID:            deviceID,
		Iname:               la.Iname,
		Name:                la.Name,
		UInterval:           la.UInterval,
		DisplayTime:         la.DisplayTime,
		Notes:               la.Notes,
		Enabled:             ParseBool(la.Enabled),
		Pushed:              la.Pushed,
		Order:               la.Order,
		LastRender:          time.Unix(la.LastRender, 0),
		LastRenderDur:       time.Duration(ParseDuration(la.LastRenderDuration)),
		Days:                la.Days,
		UseCustomRecurrence: la.UseCustomRecurrence,
		RecurrenceType:      data.RecurrenceType(la.RecurrenceType),
		RecurrenceInterval:  la.RecurrenceInterval,
		RecurrencePattern:   la.RecurrencePattern,
		RecurrenceStartDate: la.RecurrenceStartDate,
		RecurrenceEndDate:   la.RecurrenceEndDate,
		Config:              la.Config,
		EmptyLastRender:     ParseBool(la.EmptyLastRender),
		RenderMessages:      la.RenderMessages,
		AutoPin:             la.AutoPin,
	}

	// Handle Path Normalization
	if la.Path != nil {
		p := *la.Path
		if idx := strings.Index(p, "system-apps/"); idx != -1 {
			p = p[idx:]
		} else if idx := strings.Index(p, "users/"); idx != -1 {
			p = p[idx:]
		}
		na.Path = &p
	}

	if la.StartTime != nil {
		st := ParseTimeStr(la.StartTime)
		na.StartTime = &st
	}
	if la.EndTime != nil {
		et := ParseTimeStr(la.EndTime)
		na.EndTime = &et
	}
	if la.ColorFilter != nil {
		cf := data.ColorFilter(*la.ColorFilter)
		na.ColorFilter = &cf
	}

	return na
}

// ParseBool handles boolean polymorphism (bool or int 0/1).
func ParseBool(val any) bool {
	if v, ok := val.(bool); ok {
		return v
	}
	if v, ok := val.(int); ok {
		return v != 0
	}
	if v, ok := val.(float64); ok {
		return int(v) != 0
	}

	return false
}

// ParseFloat handles float polymorphism (float or string).
func ParseFloat(val any) float64 {
	if v, ok := val.(float64); ok {
		return v
	}
	if s, ok := val.(string); ok {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return 0.0
}

// ParseBrightness is a helper to handle Brightness polymorphism (int or object with value).
func ParseBrightness(val any) int {
	if v, ok := val.(float64); ok {
		return int(v)
	}
	if v, ok := val.(int); ok {
		return v
	}
	if m, ok := val.(map[string]any); ok {
		if v, ok := m["value"].(float64); ok {
			return int(v)
		}
	}

	return 0
}

func ParseTimeStr(val any) string {
	if s, ok := val.(string); ok {
		return s
	}
	if f, ok := val.(float64); ok {
		return fmt.Sprintf("%02d:00", int(f))
	}
	return ""
}

// ParseDuration parses ISO8601 duration (PT1.5S) or numeric seconds into int64 nanoseconds.
func ParseDuration(val any) int64 {
	if val == nil {
		return 0
	}

	// Case 1: Numeric (seconds)
	if v, ok := val.(float64); ok {
		return int64(v * 1e9)
	}

	// Case 2: String (ISO8601)
	str, ok := val.(string)
	if !ok {
		return 0
	}

	// Simple regex for PT#S or PT#.#S
	// This covers the most common case output by Python's simple serialization
	matches := durationRegex.FindStringSubmatch(str)
	if len(matches) > 1 && matches[1] != "" {
		seconds, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			return int64(seconds * 1e9)
		}
	}

	return 0
}
