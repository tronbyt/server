package server

import (
	"context"
	"testing"
	"time"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvalidateDeviceAppRendersIfModeChanged(t *testing.T) {
	s := newTestServer(t)
	require.NotNil(t, s)
	ctx := context.Background()

	user := data.User{Username: "testuser"}
	require.NoError(t, s.DB.Create(&user).Error)

	warm := data.ColorFilterWarm
	dimmed := data.ColorFilterDimmed
	device := data.Device{
		ID:               "moderender",
		Username:         "testuser",
		Name:             "Mode Render",
		NightModeEnabled: true,
		NightStart:       "00:00",
		NightEnd:         "23:59",
		NightColorFilter: &warm,
	}
	require.NoError(t, s.DB.Create(&device).Error)

	lastRender := time.Now().Add(-time.Hour)
	app := data.App{
		DeviceID:   device.ID,
		Iname:      "clock",
		Name:       "Clock",
		Enabled:    true,
		LastRender: lastRender,
		UInterval:  60,
	}
	require.NoError(t, s.DB.Create(&app).Error)
	device.Apps = []*data.App{&app}

	before := snapshotDeviceMode(&device)
	device.NightColorFilter = &dimmed
	s.invalidateDeviceAppRendersIfModeChanged(ctx, &device, before)

	assert.True(t, app.LastRender.IsZero())

	var persisted data.App
	require.NoError(t, s.DB.First(&persisted, app.ID).Error)
	assert.True(t, persisted.LastRender.IsZero())

	s.invalidateDeviceAppRendersIfModeChanged(ctx, &device, snapshotDeviceMode(&device))
}

func TestFiltersChangedSinceLastRender(t *testing.T) {
	s := newTestServer(t)
	require.NotNil(t, s)

	dimmed := data.ColorFilterDimmed
	warm := data.ColorFilterWarm
	tz := "UTC"
	device := data.Device{
		NightModeEnabled: true,
		NightStart:       "22:00",
		NightEnd:         "06:00",
		NightColorFilter: &dimmed,
		Timezone:         &tz,
	}
	app := &data.App{
		ColorFilter: &warm,
		UInterval:   60,
	}

	noon := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	app.LastRender = noon
	assert.False(t, s.filtersChangedSinceLastRenderAt(&device, app, noon))

	night := time.Date(2026, 1, 15, 23, 0, 0, 0, time.UTC)
	app.LastRender = noon
	assert.True(t, s.filtersChangedSinceLastRenderAt(&device, app, night))

	app.LastRender = night
	assert.False(t, s.filtersChangedSinceLastRenderAt(&device, app, night))
}
