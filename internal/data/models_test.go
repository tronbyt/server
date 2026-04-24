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
