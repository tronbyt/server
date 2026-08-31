package server

import (
	"testing"
	"time"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
)

func TestGetEffectiveFilters_ModeFiltersOverrideApp(t *testing.T) {
	s := newTestServer(t)
	warm := data.ColorFilterWarm
	dimmed := data.ColorFilterDimmed
	redshift := data.ColorFilterRedshift
	dimActive := true
	dimUntil := time.Now().Add(time.Hour)

	tests := []struct {
		name     string
		device   data.Device
		app      *data.App
		expected []string
	}{
		{
			name: "dim mode filter overrides app filter",
			device: data.Device{
				DimModeEnabled:       true,
				DimTime:              new("22:00"),
				DimColorFilter:       &dimmed,
				DimModeOverride:      &dimActive,
				DimModeOverrideUntil: &dimUntil,
			},
			app: &data.App{
				ColorFilter: &warm,
			},
			expected: []string{"dimmed"},
		},
		{
			name: "night mode filter overrides app filter",
			device: data.Device{
				NightModeEnabled: true,
				NightStart:       "00:00",
				NightEnd:         "23:59",
				NightColorFilter: &redshift,
			},
			app: &data.App{
				ColorFilter: &warm,
			},
			expected: []string{"redshift"},
		},
		{
			name: "night mode filter takes precedence over dim mode filter",
			device: data.Device{
				NightModeEnabled: true,
				NightStart:       "00:00",
				NightEnd:         "23:59",
				NightColorFilter: &redshift,
				DimModeEnabled:   true,
				DimTime:          new("00:00"),
				DimColorFilter:   &dimmed,
			},
			app: &data.App{
				ColorFilter: &warm,
			},
			expected: []string{"redshift"},
		},
		{
			name: "app filter used when mode filter not configured",
			device: data.Device{
				DimModeEnabled: true,
				DimTime:        new("00:00"),
			},
			app: &data.App{
				ColorFilter: &warm,
			},
			expected: []string{"warm"},
		},
		{
			name: "mode filter none falls through to app filter",
			device: data.Device{
				DimModeEnabled:       true,
				DimTime:              new("22:00"),
				DimColorFilter:       new(data.ColorFilterNone),
				DimModeOverride:      &dimActive,
				DimModeOverrideUntil: &dimUntil,
			},
			app: &data.App{
				ColorFilter: &warm,
			},
			expected: []string{"warm"},
		},
		{
			name: "app inherit uses device filter outside mode",
			device: data.Device{
				ColorFilter: &redshift,
			},
			app: &data.App{
				ColorFilter: new(data.ColorFilterInherit),
			},
			expected: []string{"redshift"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filters := s.getEffectiveFilters(&tt.device, tt.app)
			assert.Equal(t, tt.expected, filters)
		})
	}
}

func TestGetEffectiveFilters_ModeOverride(t *testing.T) {
	s := newTestServer(t)
	warm := data.ColorFilterWarm
	dimmed := data.ColorFilterDimmed

	active := true
	until := time.Now().Add(time.Hour)
	device := data.Device{
		NightModeEnabled:       true,
		NightStart:             "22:00",
		NightEnd:               "06:00",
		NightColorFilter:       &dimmed,
		NightModeOverride:      &active,
		NightModeOverrideUntil: &until,
	}
	app := &data.App{ColorFilter: &warm}

	filters := s.getEffectiveFilters(&device, app)
	assert.Equal(t, []string{"dimmed"}, filters)
}
