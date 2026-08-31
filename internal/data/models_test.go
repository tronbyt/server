package data

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeviceGetScheduledNightModeIsActiveAt(t *testing.T) {
	device := Device{
		NightModeEnabled: true,
		NightStart:       "22:00",
		NightEnd:         "06:00",
	}
	now := time.Date(2026, time.April, 24, 23, 30, 0, 0, time.UTC)

	assert.True(t, device.GetScheduledNightModeIsActiveAt(now))
}

func TestDeviceGetNightModeNextChangeAt(t *testing.T) {
	device := Device{
		NightModeEnabled: true,
		NightStart:       "22:00",
		NightEnd:         "06:00",
	}
	now := time.Date(2026, time.April, 24, 23, 30, 0, 0, time.UTC)

	nextChange := device.GetNightModeNextChangeAt(now)
	require.NotNil(t, nextChange)
	assert.Equal(t, time.Date(2026, time.April, 25, 6, 0, 0, 0, time.UTC), *nextChange)
}

func TestDeviceGetNightModeIsActiveUsesManualOverride(t *testing.T) {
	override := false
	overrideUntil := time.Now().Add(30 * time.Minute)
	device := Device{
		NightModeEnabled:       true,
		NightStart:             "00:00",
		NightEnd:               "23:59",
		NightModeOverride:      &override,
		NightModeOverrideUntil: &overrideUntil,
	}

	assert.False(t, device.GetNightModeIsActive())
}

func TestDeviceGetDimModeIsActiveUsesManualOverride(t *testing.T) {
	override := true
	overrideUntil := time.Now().Add(30 * time.Minute)
	dimTime := "18:00"
	device := Device{
		DimModeEnabled:       true,
		DimTime:              &dimTime,
		DimModeOverride:      &override,
		DimModeOverrideUntil: &overrideUntil,
	}

	assert.True(t, device.GetDimModeIsActive())
}

func TestDeviceHasTimezone(t *testing.T) {
	tz := "America/New_York"

	assert.False(t, (*Device)(nil).HasTimezone())

	assert.False(t, (&Device{}).HasTimezone())

	device := &Device{
		Location: DeviceLocation{Timezone: tz},
	}
	assert.True(t, device.HasTimezone())

	device = &Device{
		Location: DeviceLocation{Description: "Somewhere"},
	}
	assert.False(t, device.HasTimezone())

	device = &Device{
		Timezone: &tz,
	}
	assert.True(t, device.HasTimezone())

	empty := ""
	device = &Device{Timezone: &empty}
	assert.False(t, device.HasTimezone())
}

func TestDeviceSupportsHTTPFirmwareCommands(t *testing.T) {
	httpDevice := Device{
		Type: DeviceTidbytGen1,
		Info: DeviceInfo{ProtocolType: ProtocolHTTP},
	}
	wsDevice := Device{
		Type: DeviceTidbytGen1,
		Info: DeviceInfo{ProtocolType: ProtocolWS},
	}
	otherDevice := Device{
		Type: DeviceOther,
		Info: DeviceInfo{ProtocolType: ProtocolHTTP},
	}

	assert.True(t, httpDevice.SupportsHTTPFirmwareCommands())
	assert.False(t, wsDevice.SupportsHTTPFirmwareCommands())
	assert.False(t, otherDevice.SupportsHTTPFirmwareCommands())
}

func TestDeviceTypeCanvasAndDisplaySize(t *testing.T) {
	tests := []struct {
		name                        string
		deviceType                  DeviceType
		canvasWidth, canvasHeight   int
		displayWidth, displayHeight int
	}{
		{"classic", DeviceRaspberryPi, 64, 32, 64, 32},
		{"tidbyt", DeviceTidbytGen1, 64, 32, 64, 32},
		{"wide renders 2x", DeviceRaspberryPiWide, 64, 32, 128, 64},
		{"square", DeviceRaspberryPiSquare, 64, 64, 64, 64},
		{"square s3", DeviceMatrixPortalSquare, 64, 64, 64, 64},
		{"unknown falls back", DeviceOther, 64, 32, 64, 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width, height := tt.deviceType.CanvasSize()
			assert.Equal(t, tt.canvasWidth, width)
			assert.Equal(t, tt.canvasHeight, height)

			width, height = tt.deviceType.DisplaySize()
			assert.Equal(t, tt.displayWidth, width)
			assert.Equal(t, tt.displayHeight, height)
		})
	}
}

// The square MatrixPortal is a firmware device, so it needs its own binaries
// rather than silently inheriting the 64x32 ones: flashing those would light
// the panel at the wrong geometry.
func TestDeviceTypeSquareMatrixPortalHasItsOwnFirmware(t *testing.T) {
	assert.True(t, DeviceMatrixPortalSquare.SupportsFirmware())
	assert.True(t, DeviceMatrixPortalSquare.SupportsOTA())

	firmware := DeviceMatrixPortalSquare.FirmwareFilename(false)
	merged := DeviceMatrixPortalSquare.MergedFilename(false)
	assert.Equal(t, "matrixportal-s3-square.bin", firmware)
	assert.Equal(t, "matrixportal-s3-square_merged.bin", merged)
	assert.NotEqual(t, DeviceMatrixPortal.FirmwareFilename(false), firmware)
	assert.NotEqual(t, DeviceMatrixPortal.MergedFilename(false), merged)
}

func TestDeviceTypeSquareRoundTripsAsSlug(t *testing.T) {
	assert.Equal(t, "raspberrypi_square", DeviceRaspberryPiSquare.Slug())
	assert.Equal(t, DeviceRaspberryPiSquare, StringToDeviceType["raspberrypi_square"])
	assert.Equal(t, "matrixportal_s3_square", DeviceMatrixPortalSquare.Slug())
	assert.Equal(t, DeviceMatrixPortalSquare, StringToDeviceType["matrixportal_s3_square"])

	// Persistence and the API both go through the slug, so an unrecognised
	// value must not silently become a square panel.
	var scanned DeviceType
	require.NoError(t, scanned.Scan("raspberrypi_square"))
	assert.Equal(t, DeviceRaspberryPiSquare, scanned)
	require.NoError(t, scanned.Scan("nonsense"))
	assert.Equal(t, DeviceOther, scanned)
}

// A device type that offers firmware but names no binary would fail only at
// the point someone tries to flash it, so check the pairing directly. Merged
// images are deliberately not required: Pixoticker ships OTA-only.
func TestEveryFirmwareDeviceTypeNamesABinary(t *testing.T) {
	for deviceType, slug := range DeviceTypeToString {
		if !deviceType.SupportsFirmware() {
			continue
		}
		assert.NotEmptyf(t, deviceType.FirmwareFilename(false),
			"%s claims firmware support but names no binary", slug)
	}
}
