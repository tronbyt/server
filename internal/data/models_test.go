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

func TestDeviceSupportsColorOrder(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    bool
	}{
		{"no version", "", false},
		{"dev", "dev", true},
		{"below minimum", "v1.6.9", false},
		{"exactly minimum", "v1.7.0", true},
		{"above minimum", "v1.7.3", true},
		{"no v prefix", "1.8.0", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Device{Info: DeviceInfo{FirmwareVersion: tc.version}}
			assert.Equal(t, tc.want, d.SupportsColorOrder())
		})
	}
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
		{"wide s3 renders 2x", DeviceMatrixPortalWide, 64, 32, 128, 64},
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

// The wide MatrixPortal drives the same 128x64 panel as the Tronbyt S3 Wide,
// but the boards are not interchangeable: the Tronbyt S3 Wide image enables
// octal PSRAM, which claims GPIO 35/36/37, and on a MatrixPortal S3 those are
// the HUB75 D/B/B2 lines. It has to get its own binaries, never the Tronbyt S3
// Wide ones or the 64x32 MatrixPortal ones.
func TestDeviceTypeWideMatrixPortalHasItsOwnFirmware(t *testing.T) {
	assert.True(t, DeviceMatrixPortalWide.SupportsFirmware())
	assert.True(t, DeviceMatrixPortalWide.SupportsOTA())

	firmware := DeviceMatrixPortalWide.FirmwareFilename(false)
	merged := DeviceMatrixPortalWide.MergedFilename(false)
	assert.Equal(t, "matrixportal-s3-wide.bin", firmware)
	assert.Equal(t, "matrixportal-s3-wide_merged.bin", merged)
	for _, other := range []DeviceType{DeviceTronbytS3Wide, DeviceMatrixPortal} {
		assert.NotEqual(t, other.FirmwareFilename(false), firmware)
		assert.NotEqual(t, other.MergedFilename(false), merged)
	}
}

func TestDeviceTypeWideMatrixPortalRoundTripsAsSlug(t *testing.T) {
	assert.Equal(t, "matrixportal_s3_wide", DeviceMatrixPortalWide.Slug())
	assert.Equal(t, DeviceMatrixPortalWide, StringToDeviceType["matrixportal_s3_wide"])
	assert.Equal(t, "MatrixPortal S3 Wide", DeviceMatrixPortalWide.String())

	var scanned DeviceType
	require.NoError(t, scanned.Scan("matrixportal_s3_wide"))
	assert.Equal(t, DeviceMatrixPortalWide, scanned)
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

// TestRaspberryPiSupportsOTAAndNamesTronberry verifies the expected behavior under test.
func TestRaspberryPiSupportsOTAAndNamesTronberry(t *testing.T) {
	piTypes := []DeviceType{DeviceRaspberryPi, DeviceRaspberryPiWide, DeviceRaspberryPiSquare}
	for _, dt := range piTypes {
		assert.True(t, dt.SupportsOTA(), "%s should support OTA", dt.Slug())
		assert.True(t, dt.IsRaspberryPi(), "%s should be recognized as Raspberry Pi", dt.Slug())
		assert.Equal(t, "tronberry", dt.FirmwareFilename(false))
		assert.Equal(t, "tronberry", dt.FirmwareFilename(true))

		d := &Device{
			Type: dt,
			Info: DeviceInfo{
				FirmwareVersion: "v0.1.0",
			},
		}
		assert.True(t, d.OTACapable(), "%s with version v0.1.0 should be OTA capable", dt.Slug())

		dDev := &Device{
			Type: dt,
			Info: DeviceInfo{
				FirmwareVersion: "dev",
			},
		}
		assert.True(t, dDev.OTACapable(), "%s with version dev should be OTA capable", dt.Slug())

		dNoVer := &Device{
			Type: dt,
			Info: DeviceInfo{
				FirmwareVersion: "",
			},
		}
		assert.False(t, dNoVer.OTACapable(), "%s with empty version should not be OTA capable", dt.Slug())
	}
}

// TestDeviceType_SupportsAudio verifies the expected behavior under test.
func TestDeviceType_SupportsAudio(t *testing.T) {
	assert.False(t, DeviceTidbytGen1.SupportsAudio())
	assert.False(t, DevicePixoticker.SupportsAudio())
	assert.True(t, DeviceTidbytGen2.SupportsAudio())
	assert.True(t, DeviceRaspberryPi.SupportsAudio())
	assert.True(t, DeviceRaspberryPiWide.SupportsAudio())
	assert.True(t, DeviceRaspberryPiSquare.SupportsAudio())
	assert.True(t, DeviceTronbytS3.SupportsAudio())
	assert.True(t, DeviceTronbytS3Wide.SupportsAudio())
	assert.True(t, DeviceMatrixPortal.SupportsAudio())
	assert.True(t, DeviceMatrixPortalWS.SupportsAudio())
	assert.True(t, DeviceMatrixPortalSquare.SupportsAudio())
	assert.True(t, DeviceMatrixPortalWide.SupportsAudio())
	assert.True(t, DeviceWaveshareS3.SupportsAudio())
	assert.True(t, DeviceOther.SupportsAudio())

	gen1Dev := &Device{
		Type: DeviceTidbytGen1,
	}
	assert.False(t, gen1Dev.SupportsAudio())
	assert.Equal(t, AudioCapNone, gen1Dev.GetAudioCapability())
	assert.False(t, gen1Dev.HasSpeaker())

	gen2Dev := &Device{
		Type: DeviceTidbytGen2,
	}
	assert.True(t, gen2Dev.SupportsAudio())
	assert.Equal(t, AudioCapPiezo, gen2Dev.GetAudioCapability())
	assert.True(t, gen2Dev.HasSpeaker())
}
