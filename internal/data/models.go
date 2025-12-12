package data

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// --- Enums & Value Types ---

type ThemePreference string

const (
	ThemeLight  ThemePreference = "light"
	ThemeDark   ThemePreference = "dark"
	ThemeSystem ThemePreference = "system"
)

type DeviceType string

const (
	DeviceTidbytGen1      DeviceType = "tidbyt_gen1"
	DeviceTidbytGen2      DeviceType = "tidbyt_gen2"
	DevicePixoticker      DeviceType = "pixoticker"
	DeviceRaspberryPi     DeviceType = "raspberrypi"
	DeviceRaspberryPiWide DeviceType = "raspberrypi_wide"
	DeviceTronbytS3       DeviceType = "tronbyt_s3"
	DeviceTronbytS3Wide   DeviceType = "tronbyt_s3_wide"
	DeviceMatrixPortal    DeviceType = "matrixportal_s3"
	DeviceMatrixPortalWS  DeviceType = "matrixportal_s3_waveshare"
	DeviceOther           DeviceType = "other"
)

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
		return string(dt) // Fallback to raw string value
	}
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
	if val, ok := value.(int64); ok {
		*b = Brightness(val)

		return nil
	}

	return errors.New("failed to scan Brightness")
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
		var keys []int
		for k := range customScale {
			keys = append(keys, k)
		}
		sort.Ints(keys)

		// Create pairs for sorting by percent value
		type scalePair struct {
			Level   int
			Percent int
		}
		var pairs []scalePair
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
	bytes, ok := value.([]byte)
	if !ok {
		if s, ok := value.(string); ok {
			bytes = []byte(s)
		} else {
			return errors.New("type assertion to []byte failed")
		}
	}
	return json.Unmarshal(bytes, l)
}

// DeviceInfo stores firmware and protocol details.
type DeviceInfo struct {
	FirmwareVersion string       `json:"firmware_version"`
	FirmwareType    string       `json:"firmware_type"`
	ProtocolVersion *int         `json:"protocol_version"`
	MACAddress      string       `json:"mac_address"`
	ProtocolType    ProtocolType `json:"protocol_type"`
}

func (i DeviceInfo) Value() (driver.Value, error) {
	return json.Marshal(i)
}

func (i *DeviceInfo) Scan(value any) error {
	if value == nil {
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		if s, ok := value.(string); ok {
			bytes = []byte(s)
		} else {
			return errors.New("type assertion to []byte failed")
		}
	}
	return json.Unmarshal(bytes, i)
}

// JSONMap is a helper for storing arbitrary JSON in the DB.
type JSONMap map[string]any

func (j JSONMap) Value() (driver.Value, error) {
	return json.Marshal(j)
}

func (j *JSONMap) Scan(value any) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, j)
}

// StringSlice is a helper for storing []string as JSON.
type StringSlice []string

func (s StringSlice) Value() (driver.Value, error) {
	return json.Marshal(s)
}

func (s *StringSlice) Scan(value any) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, s)
}

// --- Models ---

type User struct {
	Username        string `gorm:"primaryKey"`
	Password        string
	Email           string
	IsAdmin         bool `gorm:"default:false"`
	APIKey          string
	ThemePreference ThemePreference `gorm:"default:'system'"`
	SystemRepoURL   string
	AppRepoURL      string

	Devices     []Device             `gorm:"foreignKey:Username;references:Username"`
	Credentials []WebAuthnCredential `gorm:"foreignKey:UserID;references:Username"`
}

type WebAuthnCredential struct {
	ID              string `gorm:"primaryKey"`
	UserID          string `gorm:"index"`
	User            User   `gorm:"foreignKey:UserID;references:Username"`
	PublicKey       []byte
	AttestationType string
	Transport       string // Comma-separated
	Flags           string // Comma-separated keywords
	Authenticator   string // AAGUID
	SignCount       uint32
	CloneWarning    bool
	BackupEligible  bool
	BackupState     bool
}

type Device struct {
	ID                    string `gorm:"primaryKey"` // 8-char hex
	Username              string `gorm:"index"`
	Name                  string
	Type                  DeviceType `gorm:"default:'tidbyt_gen1'"`
	APIKey                string
	ImgURL                string
	WsURL                 string
	Notes                 string
	Brightness            Brightness `gorm:"default:20"` // 0-100
	CustomBrightnessScale string
	NightModeEnabled      bool
	NightModeApp          string
	NightStart            string     // HH:MM
	NightEnd              string     // HH:MM
	NightBrightness       Brightness `gorm:"default:0"`
	DimTime               *string
	DimBrightness         *Brightness
	DefaultInterval       int `gorm:"default:15"`

	Timezone *string
	Locale   *string

	// Location fields embedded directly or as JSON? Let's use embedded fields for queryability if needed,
	// but JSON is easier for migration. Let's use JSON for Location to match the complexity.
	Location DeviceLocation `gorm:"type:text"` // Stores lat, lng, locality, etc.

	LastAppIndex        int
	PinnedApp           *string
	InterstitialEnabled bool
	InterstitialApp     *string
	LastSeen            *time.Time

	// DeviceInfo fields (FirmwareVersion etc)
	Info DeviceInfo `gorm:"type:text"`

	ColorFilter      *ColorFilter
	NightColorFilter *ColorFilter

	Apps []App `gorm:"foreignKey:DeviceID;references:ID"`
}

func (dt DeviceType) Supports2x() bool {
	switch dt {
	case DeviceRaspberryPiWide, DeviceTronbytS3Wide:
		return true
	default:
		return false
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

type App struct {
	// Composite key might be better, but a surrogate ID is easier for GORM
	ID uint `gorm:"primaryKey"`

	DeviceID string `gorm:"index;type:string"` // Foreign Key to Device
	Iname    string `gorm:"index"`             // Installation Name/ID (e.g. "123")

	Name          string // App Name (e.g. "Clock")
	UInterval     int    // Update Interval
	DisplayTime   int
	Notes         string
	Enabled       bool
	Pushed        bool
	Order         int
	LastRender    time.Time
	LastRenderDur time.Duration
	Path          *string
	StartTime     *string     // HH:MM
	EndTime       *string     // HH:MM
	Days          StringSlice `gorm:"type:text"` // ["monday", "tuesday"]

	// Recurrence
	UseCustomRecurrence bool
	RecurrenceType      RecurrenceType
	RecurrenceInterval  int
	RecurrencePattern   JSONMap `gorm:"type:text"`
	RecurrenceStartDate *string // YYYY-MM-DD
	RecurrenceEndDate   *string // YYYY-MM-DD

	Config          JSONMap `gorm:"type:text"`
	EmptyLastRender bool
	RenderMessages  StringSlice `gorm:"type:text"`
	AutoPin         bool
	ColorFilter     *ColorFilter
}

// GetNightModeIsActive checks if night mode is currently active for a device.
func (d *Device) GetNightModeIsActive() bool {
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
func (d *Device) GetDimModeIsActive() bool {
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
