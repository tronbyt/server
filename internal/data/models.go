package data

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// --- Enums & Value Types ---

const minOTAFirmwareVersion = "v1.4.6"
const minFirmwareFeaturesVersion = "v1.5.0"

type ThemePreference string

const (
	ThemeLight  ThemePreference = "light"
	ThemeDark   ThemePreference = "dark"
	ThemeSystem ThemePreference = "system"
)

type DeviceType int

const (
	DeviceUnknown DeviceType = iota
	DeviceTidbytGen1
	DeviceTidbytGen2
	DeviceTronbytS3
	DeviceTronbytS3Wide
	DeviceMatrixPortal
	DeviceMatrixPortalWS
	DevicePixoticker
	DeviceRaspberryPi
	DeviceRaspberryPiWide
	DeviceOther
)

var DeviceTypeToString = map[DeviceType]string{
	DeviceUnknown:         "unknown",
	DeviceTidbytGen1:      "tidbyt_gen1",
	DeviceTidbytGen2:      "tidbyt_gen2",
	DeviceTronbytS3:       "tronbyt_s3",
	DeviceTronbytS3Wide:   "tronbyt_s3_wide",
	DeviceMatrixPortal:    "matrixportal_s3",
	DeviceMatrixPortalWS:  "matrixportal_s3_waveshare",
	DevicePixoticker:      "pixoticker",
	DeviceRaspberryPi:     "raspberrypi",
	DeviceRaspberryPiWide: "raspberrypi_wide",
	DeviceOther:           "other",
}

var StringToDeviceType = func() map[string]DeviceType {
	m := make(map[string]DeviceType)
	for dt, s := range DeviceTypeToString {
		m[s] = dt
	}
	return m
}()

// String returns the human-readable display name for the DeviceType.
func (dt DeviceType) String() string {
	switch dt {
	case DeviceTidbytGen1:
		return "Tidbyt Gen1"
	case DeviceTidbytGen2:
		return "Tidbyt Gen2"
	case DevicePixoticker:
		return "Pixoticker"
	case DeviceRaspberryPi:
		return "Raspberry Pi"
	case DeviceRaspberryPiWide:
		return "Raspberry Pi Wide"
	case DeviceTronbytS3:
		return "Tronbyt S3"
	case DeviceTronbytS3Wide:
		return "Tronbyt S3 Wide"
	case DeviceMatrixPortal:
		return "MatrixPortal S3"
	case DeviceMatrixPortalWS:
		return "MatrixPortal S3 Waveshare"
	case DeviceOther:
		return "Other"
	default:
		return "Unknown"
	}
}

// Slug returns the URL-friendly slug for the DeviceType.
func (dt DeviceType) Slug() string {
	if s, ok := DeviceTypeToString[dt]; ok {
		return s
	}
	return "other"
}

func (dt DeviceType) Value() (driver.Value, error) {
	return dt.Slug(), nil
}

func (dt *DeviceType) Scan(value any) error {
	if value == nil {
		*dt = DeviceOther
		return nil
	}

	var s string
	switch v := value.(type) {
	case []byte:
		s = string(v)
	case string:
		s = v
	default:
		return errors.New("failed to scan DeviceType")
	}

	if val, ok := StringToDeviceType[s]; ok {
		*dt = val
	} else {
		*dt = DeviceOther
	}
	return nil
}

func (dt DeviceType) MarshalJSON() ([]byte, error) {
	return json.Marshal(dt.Slug())
}

func (dt *DeviceType) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if val, ok := StringToDeviceType[s]; ok {
		*dt = val
	} else {
		*dt = DeviceOther // Fallback for unknown strings
	}
	return nil
}

type ProtocolType string

const (
	ProtocolHTTP ProtocolType = "HTTP"
	ProtocolWS   ProtocolType = "WS"
)

func (p ProtocolType) String() string {
	return string(p)
}

func (p ProtocolType) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(p))
}

func (p *ProtocolType) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*p = ProtocolType(strings.ToUpper(s))

	return nil
}

type RecurrenceType string

const (
	RecurrenceDaily   RecurrenceType = "daily"
	RecurrenceWeekly  RecurrenceType = "weekly"
	RecurrenceMonthly RecurrenceType = "monthly"
	RecurrenceYearly  RecurrenceType = "yearly"
)

type Brightness int

// Value implements the driver.Valuer interface for database storage.
func (b Brightness) Value() (driver.Value, error) {
	return int64(b), nil
}

func (b *Brightness) Scan(value any) error {
	if value == nil {
		*b = 0
		return nil
	}
	switch v := value.(type) {
	case int64:
		*b = Brightness(v)
	case int:
		*b = Brightness(v)
	case int32:
		*b = Brightness(v)
	case float64:
		*b = Brightness(v)
	case []byte:
		i, err := strconv.Atoi(string(v))
		if err != nil {
			return err
		}
		*b = Brightness(i)
	case string:
		i, err := strconv.Atoi(v)
		if err != nil {
			return err
		}
		*b = Brightness(i)
	default:
		return errors.New("failed to scan Brightness")
	}
	return nil
}

// Percent returns brightness as 0.0-1.0.
func (b Brightness) Percent() float64 {
	if b < 0 {
		return 0.0
	}
	if b > 100 {
		return 1.0
	}
	return float64(b) / 100.0
}

// Uint8 returns brightness as 0-255.
func (b Brightness) Uint8() uint8 {
	return uint8(b.Percent() * 255.0)
}

// UIScale returns 0-5.
func (b Brightness) UIScale(customScale map[int]int) int {
	v := int(b)
	if customScale != nil {
		// Use custom scale
		// Sort keys to find brackets
		keys := make([]int, 0, len(customScale))
		for k := range customScale {
			keys = append(keys, k)
		}
		sort.Ints(keys)

		// Create pairs for sorting by percent value
		type scalePair struct {
			Level   int
			Percent int
		}
		pairs := make([]scalePair, 0, len(keys))
		for _, k := range keys {
			pairs = append(pairs, scalePair{k, customScale[k]})
		}
		// Sort by percent
		sort.Slice(pairs, func(i, j int) bool {
			return pairs[i].Percent < pairs[j].Percent
		})

		for i := range pairs {
			level := pairs[i].Level
			percent := pairs[i].Percent

			if i == len(pairs)-1 {
				return level
			}
			nextPercent := pairs[i+1].Percent
			midpoint := (percent + nextPercent) / 2
			if v <= midpoint {
				return level
			}
		}
		if len(pairs) > 0 {
			return pairs[len(pairs)-1].Level
		}
		return 5 // Fallback
	}

	// Default scale
	if v <= 0 {
		return 0
	}
	if v <= 3 {
		return 1
	}
	if v <= 5 {
		return 2
	}
	if v <= 12 {
		return 3
	}
	if v <= 35 {
		return 4
	}
	return 5
}

// BrightnessFromUIScale converts a UI scale value (0-5) to Brightness percentage.
func BrightnessFromUIScale(uiValue int, customScale map[int]int) Brightness {
	if customScale != nil {
		if val, ok := customScale[uiValue]; ok {
			return Brightness(val)
		}
		return Brightness(20) // Default if not found in custom scale
	}

	lookup := map[int]int{
		0: 0,
		1: 3,
		2: 5,
		3: 12,
		4: 35,
		5: 100,
	}
	if val, ok := lookup[uiValue]; ok {
		return Brightness(val)
	}
	return Brightness(20) // Default
}

// ParseCustomBrightnessScale parses a comma-separated string into a map.
func ParseCustomBrightnessScale(scaleStr string) map[int]int {
	if scaleStr == "" {
		return nil
	}
	parts := strings.Split(scaleStr, ",")
	if len(parts) != 6 {
		return nil
	}

	result := make(map[int]int)
	for i, p := range parts {
		val, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || val < 0 || val > 100 {
			return nil
		}
		result[i] = val
	}
	return result
}

type ColorFilter string

const (
	ColorFilterInherit    ColorFilter = "inherit"
	ColorFilterNone       ColorFilter = "none"
	ColorFilterDimmed     ColorFilter = "dimmed"
	ColorFilterRedshift   ColorFilter = "redshift"
	ColorFilterWarm       ColorFilter = "warm"
	ColorFilterSunset     ColorFilter = "sunset"
	ColorFilterSepia      ColorFilter = "sepia"
	ColorFilterVintage    ColorFilter = "vintage"
	ColorFilterDusk       ColorFilter = "dusk"
	ColorFilterCool       ColorFilter = "cool"
	ColorFilterBlackWhite ColorFilter = "bw"
	ColorFilterIce        ColorFilter = "ice"
	ColorFilterMoonlight  ColorFilter = "moonlight"
	ColorFilterNeon       ColorFilter = "neon"
	ColorFilterPastel     ColorFilter = "pastel"
)

// DeviceLocation stores lat/lng and timezone.
type DeviceLocation struct {
	Description string  `json:"description"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	Locality    string  `json:"locality"`
	PlaceID     string  `json:"place_id"`
	Timezone    string  `json:"timezone"`
}

func (l DeviceLocation) Value() (driver.Value, error) {
	return json.Marshal(l)
}

func (l *DeviceLocation) Scan(value any) error {
	if value == nil {
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("type assertion to []byte or string failed")
	}
	return json.Unmarshal(bytes, l)
}

// DeviceInfo stores firmware and protocol details.
type DeviceInfo struct {
	FirmwareVersion    string       `json:"firmware_version"`
	FirmwareType       string       `json:"firmware_type"`
	ProtocolVersion    *int         `json:"protocol_version"`
	MACAddress         string       `json:"mac_address"`
	ProtocolType       ProtocolType `json:"protocol_type"`
	SSID               *string      `json:"ssid"`
	WifiPowerSave      *int         `json:"wifi_power_save"`
	SkipDisplayVersion *bool        `json:"skip_display_version"`
	APMode             *bool        `json:"ap_mode"`
	PreferIPv6         *bool        `json:"prefer_ipv6"`
	SwapColors         *bool        `json:"swap_colors"`
	ImageURL           *string      `json:"image_url"`
	Hostname           *string      `json:"hostname"`
	SNTPServer         *string      `json:"sntp_server"`
	SyslogAddr         *string      `json:"syslog_addr"`
}

func (i DeviceInfo) Value() (driver.Value, error) {
	return json.Marshal(i)
}

func (i *DeviceInfo) Scan(value any) error {
	if value == nil {
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("type assertion to []byte or string failed")
	}
	return json.Unmarshal(bytes, i)
}

// JSONMap is a helper for storing arbitrary JSON in the DB.
type JSONMap map[string]any

func (j JSONMap) Value() (driver.Value, error) {
	return json.Marshal(j)
}

func (j *JSONMap) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("type assertion to []byte or string failed")
	}
	return json.Unmarshal(bytes, j)
}

// StringSlice is a helper for storing []string as JSON.
type StringSlice []string

func (s StringSlice) Value() (driver.Value, error) {
	return json.Marshal(s)
}

func (s *StringSlice) Scan(value any) error {
	if value == nil {
		*s = nil
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("type assertion to []byte or string failed")
	}
	return json.Unmarshal(bytes, s)
}

// --- Models ---

type User struct {
	Username        string          `gorm:"primaryKey"       json:"username"`
	Password        string          `json:"-"`
	Email           *string         `gorm:"uniqueIndex"      json:"email"`
	IsAdmin         bool            `gorm:"default:false"    json:"is_admin"`
	APIKey          string          `gorm:"uniqueIndex"      json:"api_key"`
	ThemePreference ThemePreference `gorm:"default:'system'" json:"theme_preference"`
	SystemRepoURL   string          `json:"system_repo_url"`
	AppRepoURL      string          `json:"app_repo_url"`

	Devices     []Device             `gorm:"foreignKey:Username;references:Username" json:"devices"`
	Credentials []WebAuthnCredential `gorm:"foreignKey:UserID;references:Username"   json:"credentials"`
}

type WebAuthnCredential struct {
	ID              string `gorm:"primaryKey"                            json:"id"`
	UserID          string `gorm:"index"                                 json:"user_id"`
	User            User   `gorm:"foreignKey:UserID;references:Username" json:"-"`
	Name            string `json:"name"` // User-friendly name
	PublicKey       []byte `json:"public_key"`
	AttestationType string `json:"attestation_type"`
	Transport       string `json:"transport"`     // Comma-separated
	Flags           string `json:"flags"`         // Comma-separated keywords
	Authenticator   string `json:"authenticator"` // AAGUID
	SignCount       uint32 `json:"sign_count"`
	CloneWarning    bool   `json:"clone_warning"`
	BackupEligible  bool   `json:"backup_eligible"`
	BackupState     bool   `json:"backup_state"`
}

type App struct {
	// Composite key might be better, but a surrogate ID is easier for GORM
	ID uint `gorm:"primaryKey" json:"id"`

	DeviceID string `gorm:"index:idx_device_order,priority:1;uniqueIndex:idx_device_iname,priority:1;type:string" json:"device_id"` // Foreign Key to Device
	Iname    string `gorm:"uniqueIndex:idx_device_iname,priority:2"                                               json:"iname"`     // Installation Name/ID (e.g. "123")

	Name                 string        `json:"name"`      // App Name (e.g. "Clock")
	UInterval            int           `json:"uinterval"` // Update Interval
	DisplayTime          int           `json:"display_time"`
	Notes                string        `json:"notes"`
	Enabled              bool          `json:"enabled"`
	Pushed               bool          `json:"pushed"`
	Order                int           `gorm:"index:idx_device_order,priority:2" json:"order"`
	LastRender           time.Time     `json:"last_render"`
	LastSuccessfulRender *time.Time    `json:"last_successful_render"`
	LastRenderDur        time.Duration `json:"last_render_dur"`
	Path                 *string       `json:"path"`
	StartTime            *string       `json:"start_time"`                                    // HH:MM
	EndTime              *string       `json:"end_time"`                                      // HH:MM
	Days                 StringSlice   `gorm:"type:text"                         json:"days"` // ["monday", "tuesday"]

	// Recurrence
	UseCustomRecurrence bool           `json:"use_custom_recurrence"`
	RecurrenceType      RecurrenceType `json:"recurrence_type"`
	RecurrenceInterval  int            `json:"recurrence_interval"`
	RecurrencePattern   JSONMap        `gorm:"type:text"             json:"recurrence_pattern"`
	RecurrenceStartDate *string        `json:"recurrence_start_date"` // YYYY-MM-DD
	RecurrenceEndDate   *string        `json:"recurrence_end_date"`   // YYYY-MM-DD

	Config          JSONMap      `gorm:"type:text"         json:"config"`
	EmptyLastRender bool         `json:"empty_last_render"`
	RenderMessages  StringSlice  `gorm:"type:text"         json:"render_messages"`
	AutoPin         bool         `json:"auto_pin"`
	ColorFilter     *ColorFilter `json:"color_filter"`
}

type Device struct {
	ID                    string      `gorm:"primaryKey"              json:"id"` // 8-char hex
	Username              string      `gorm:"index"                   json:"username"`
	Name                  string      `json:"name"`
	Type                  DeviceType  `gorm:"type:text"               json:"type"`
	APIKey                string      `gorm:"uniqueIndex"             json:"api_key"`
	ImgURL                string      `json:"img_url"`
	WsURL                 string      `json:"ws_url"`
	Notes                 string      `json:"notes"`
	Brightness            Brightness  `gorm:"default:20"              json:"brightness"` // 0-100
	CustomBrightnessScale string      `json:"custom_brightness_scale"`
	NightModeEnabled      bool        `json:"night_mode_enabled"`
	NightModeApp          string      `json:"night_mode_app"`
	NightStart            string      `json:"night_start"` // HH:MM
	NightEnd              string      `json:"night_end"`   // HH:MM
	NightBrightness       Brightness  `gorm:"default:0"               json:"night_brightness"`
	DimTime               *string     `json:"dim_time"`
	DimBrightness         *Brightness `json:"dim_brightness"`
	DefaultInterval       int         `gorm:"default:15"              json:"default_interval"`

	Timezone *string `json:"timezone"`
	Locale   *string `json:"locale"`

	// Location fields embedded directly or as JSON? Let's use embedded fields for queryability if needed,
	// but JSON is easier for migration. Let's use JSON for Location to match the complexity.
	Location DeviceLocation `gorm:"type:text" json:"location"` // Stores lat, lng, locality, etc.

	LastAppIndex        int        `json:"last_app_index"`
	DisplayingApp       *string    `json:"displaying_app"`
	PinnedApp           *string    `json:"pinned_app"`
	InterstitialEnabled bool       `json:"interstitial_enabled"`
	InterstitialApp     *string    `json:"interstitial_app"`
	LastSeen            *time.Time `json:"last_seen"`

	// DeviceInfo fields (FirmwareVersion etc)
	Info DeviceInfo `gorm:"type:text" json:"info"`

	ColorFilter      *ColorFilter `json:"color_filter"`
	NightColorFilter *ColorFilter `json:"night_color_filter"`

	// OTA
	SwapColors       bool   `json:"swap_colors"`
	PendingUpdateURL string `json:"pending_update_url,omitempty"`

	Apps []App `gorm:"foreignKey:DeviceID;references:ID" json:"apps"`
}

func (dt DeviceType) Supports2x() bool {
	switch dt {
	case DeviceRaspberryPiWide, DeviceTronbytS3Wide:
		return true
	default:
		return false
	}
}

func (dt DeviceType) SupportsFirmware() bool {
	switch dt {
	case DeviceTidbytGen1, DeviceTidbytGen2, DevicePixoticker, DeviceTronbytS3, DeviceTronbytS3Wide, DeviceMatrixPortal, DeviceMatrixPortalWS:
		return true
	default:
		return false
	}
}

func (dt DeviceType) SupportsOTA() bool {
	switch dt {
	// DevicePixoticker is intentionally omitted (not enough flash memory)
	case DeviceTidbytGen1, DeviceTidbytGen2, DeviceTronbytS3, DeviceTronbytS3Wide, DeviceMatrixPortal, DeviceMatrixPortalWS:
		return true
	default:
		return false
	}
}

func (dt DeviceType) FirmwareFilename(swapColors bool) string {
	switch dt {
	case DeviceTidbytGen1:
		if swapColors {
			return "tidbyt-gen1_swap.bin"
		}
		return "tidbyt-gen1.bin"
	case DeviceTidbytGen2:
		return "tidbyt-gen2.bin"
	case DevicePixoticker:
		return "pixoticker.bin"
	case DeviceTronbytS3:
		return "tronbyt-S3.bin"
	case DeviceTronbytS3Wide:
		return "tronbyt-s3-wide.bin"
	case DeviceMatrixPortal:
		return "matrixportal-s3.bin"
	case DeviceMatrixPortalWS:
		return "matrixportal-s3-waveshare.bin"
	default:
		return ""
	}
}

func (d *Device) GetTimezone() string {
	if d.Timezone != nil && *d.Timezone != "" {
		return *d.Timezone
	}
	if d.Location.Timezone != "" {
		return d.Location.Timezone
	}
	return "Local"
}

// GetNightModeIsActive checks if night mode is currently active for a device.
func (d Device) GetNightModeIsActive() bool {
	if !d.NightModeEnabled {
		return false
	}

	// Get Device Timezone
	loc := time.Local
	if d.Timezone != nil {
		if l, err := time.LoadLocation(*d.Timezone); err == nil {
			loc = l
		}
	} else if d.Location.Timezone != "" {
		if l, err := time.LoadLocation(d.Location.Timezone); err == nil {
			loc = l
		}
	}

	currentTime := time.Now().In(loc)
	currentHM := currentTime.Format("15:04")

	start := "22:00"
	if d.NightStart != "" {
		start = d.NightStart
	}
	end := "06:00"
	if d.NightEnd != "" {
		end = d.NightEnd
	}

	if start > end {
		return currentHM >= start || currentHM <= end
	}
	return currentHM >= start && currentHM <= end
}

// GetDimModeIsActive checks if dim mode is active (dimming without full night mode).
func (d Device) GetDimModeIsActive() bool {
	dimTime := d.DimTime
	if dimTime == nil || *dimTime == "" {
		return false
	}

	// Get Device Timezone
	loc := time.Local
	if d.Timezone != nil {
		if l, err := time.LoadLocation(*d.Timezone); err == nil {
			loc = l
		}
	} else if d.Location.Timezone != "" {
		if l, err := time.LoadLocation(d.Location.Timezone); err == nil {
			loc = l
		}
	}

	currentTime := time.Now().In(loc)
	currentHM := currentTime.Format("15:04")

	start := *dimTime
	end := "06:00" // Default
	if d.NightEnd != "" {
		end = d.NightEnd
	}

	if start > end {
		return currentHM >= start || currentHM <= end
	}
	return currentHM >= start && currentHM <= end
}

// GetEffectiveDwellTime returns the display duration for an app, falling back to the device default.
func (d *Device) GetEffectiveDwellTime(app *App) int {
	if app != nil && app.DisplayTime > 0 {
		return app.DisplayTime
	}
	return d.DefaultInterval
}

// GetEffectiveBrightness calculates the effective brightness of a device, accounting for night and dim modes.
func (d *Device) GetEffectiveBrightness() int {
	brightness := int(d.Brightness)
	if d.GetNightModeIsActive() {
		brightness = int(d.NightBrightness)
	} else if d.GetDimModeIsActive() && d.DimBrightness != nil {
		brightness = int(*d.DimBrightness)
	}
	return brightness
}

func (d *Device) OTACapable() bool {
	if !d.Type.SupportsOTA() {
		return false
	}
	v := d.Info.FirmwareVersion
	if v == "" {
		return false
	}
	if v == "dev" {
		return true
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}

	return semver.Compare(v, minOTAFirmwareVersion) >= 0
}

func (d *Device) SupportsFirmwareFeatures() bool {
	if d.Info.ProtocolType != ProtocolWS {
		return false
	}
	v := d.Info.FirmwareVersion
	if v == "" {
		return false
	}
	if v == "dev" {
		return true
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}

	return semver.Compare(v, minFirmwareFeaturesVersion) >= 0
}

// GetApp looks up an app by its iname (installation ID) in the device's Apps list.
func (d *Device) GetApp(iname string) *App {
	for i := range d.Apps {
		if d.Apps[i].Iname == iname {
			return &d.Apps[i]
		}
	}
	return nil
}

// BrightnessUIScale returns the current brightness level (0-5) for the UI.
func (d Device) BrightnessUIScale() int {
	var customScale map[int]int
	if d.CustomBrightnessScale != "" {
		customScale = ParseCustomBrightnessScale(d.CustomBrightnessScale)
	}
	return d.Brightness.UIScale(customScale)
}
