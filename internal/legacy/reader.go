package legacy

import (
	"encoding/json"
	"time"
)

// The structure of the legacy database is a single table "json_data"
// with columns: key (TEXT PRIMARY KEY), value (TEXT)
// The user data is stored under key="user_data".

// --- Legacy Go Structs matching Python Pydantic Models ---

type LegacyUser struct {
	Username        string                  `json:"username"`
	Password        string                  `json:"password"`
	Email           string                  `json:"email"`
	Devices         map[string]LegacyDevice `json:"devices"`
	APIKey          string                  `json:"api_key"`
	ThemePreference string                  `json:"theme_preference"`
	SystemRepoURL   string                  `json:"system_repo_url"`
	AppRepoURL      string                  `json:"app_repo_url"`
}

type LegacyDevice struct {
	ID                    string                     `json:"id"`
	Name                  string                     `json:"name"`
	Type                  string                     `json:"type"`
	APIKey                string                     `json:"api_key"`
	ImgURL                string                     `json:"img_url"`
	WsURL                 string                     `json:"ws_url"`
	Notes                 string                     `json:"notes"`
	Brightness            any                        `json:"brightness"` // Can be int or object
	CustomBrightnessScale string                     `json:"custom_brightness_scale"`
	NightModeEnabled      bool                       `json:"night_mode_enabled"`
	NightModeApp          string                     `json:"night_mode_app"`
	NightStart            any                        `json:"night_start"` // Can be HH:MM or int
	NightEnd              any                        `json:"night_end"`
	NightBrightness       any                        `json:"night_brightness"`
	DimTime               *string                    `json:"dim_time"`
	DimBrightness         any                        `json:"dim_brightness"`
	DefaultInterval       int                        `json:"default_interval"`
	Timezone              *string                    `json:"timezone"`
	Locale                *string                    `json:"locale"`
	Location              *LegacyLocation            `json:"location"`
	Apps                  map[string]json.RawMessage `json:"apps"`
	LastAppIndex          int                        `json:"last_app_index"`
	PinnedApp             *string                    `json:"pinned_app"`
	InterstitialEnabled   bool                       `json:"interstitial_enabled"`
	InterstitialApp       *string                    `json:"interstitial_app"`
	LastSeen              *time.Time                 `json:"last_seen"`
	Info                  LegacyDeviceInfo           `json:"info"`
	ColorFilter           *string                    `json:"color_filter"`
	NightColorFilter      *string                    `json:"night_color_filter"`
	DimColorFilter        *string                    `json:"dim_color_filter"`
}

type LegacyLocation struct {
	Locality    string  `json:"locality"`
	Description string  `json:"description"`
	PlaceID     string  `json:"place_id"`
	Timezone    *string `json:"timezone"`
	Lat         any     `json:"lat"`
	Lng         any     `json:"lng"`
}

type LegacyDeviceInfo struct {
	FirmwareVersion *string `json:"firmware_version"`
	FirmwareType    *string `json:"firmware_type"`
	ProtocolVersion *int    `json:"protocol_version"`
	MacAddress      *string `json:"mac_address"`
	ProtocolType    *string `json:"protocol_type"`
}

type LegacyApp struct {
	ID                  *string        `json:"id"`
	Iname               string         `json:"iname"`
	Name                string         `json:"name"`
	UInterval           int            `json:"uinterval"`
	DisplayTime         int            `json:"display_time"`
	Notes               string         `json:"notes"`
	Enabled             any            `json:"enabled"`
	Pushed              bool           `json:"pushed"`
	Order               int            `json:"order"`
	LastRender          int64          `json:"last_render"`
	LastRenderDuration  any            `json:"last_render_duration"` // ISO8601 string, float seconds, or object
	Path                *string        `json:"path"`
	StartTime           any            `json:"start_time"` // HH:MM
	EndTime             any            `json:"end_time"`   // HH:MM
	Days                []string       `json:"days"`
	UseCustomRecurrence bool           `json:"use_custom_recurrence"`
	RecurrenceType      string         `json:"recurrence_type"`
	RecurrenceInterval  int            `json:"recurrence_interval"`
	RecurrencePattern   map[string]any `json:"recurrence_pattern"`
	RecurrenceStartDate *string        `json:"recurrence_start_date"`
	RecurrenceEndDate   *string        `json:"recurrence_end_date"`
	Config              map[string]any `json:"config"`
	EmptyLastRender     any            `json:"empty_last_render"` // Can be bool or int (0/1)
	RenderMessages      []string       `json:"render_messages"`
	AutoPin             bool           `json:"autopin"`
	ColorFilter         *string        `json:"color_filter"`
}

// UserDataBlob is a wrapper for the JSON blob structure in DB.
type UserDataBlob struct {
	Users map[string]LegacyUser `json:"users"`
}
