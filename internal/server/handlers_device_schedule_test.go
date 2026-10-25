package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postDeviceForm(t *testing.T, s *Server, user *data.User, device *data.Device, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/devices/"+device.ID+"/update", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), userContextKey, user)
	ctx = context.WithValue(ctx, deviceContextKey, device)
	rr := httptest.NewRecorder()
	s.handleUpdateDevicePost(rr, req.WithContext(ctx))
	return rr
}

// The settings form saves sunrise/sunset boundaries and shows them again.
func TestDeviceFormSunSchedules(t *testing.T) {
	s := newTestServer(t)
	user := data.User{Username: "testuser"}
	require.NoError(t, s.DB.Create(&user).Error)
	device := data.Device{
		ID: "sundevice", Username: "testuser", Name: "Window", APIKey: "sunkey",
		Location: data.DeviceLocation{Lat: 42.36, Lng: -71.06, Timezone: "America/New_York"},
	}
	require.NoError(t, s.DB.Create(&device).Error)

	form := url.Values{
		"name": {"Window"}, "device_type": {"tidbyt_gen2"}, "brightness": {"3"}, "default_interval": {"10"},
		"night_mode_enabled": {"on"},
		"night_start_kind":   {"sunset"}, "night_start": {"22:00"}, "night_start_offset": {"30"},
		"night_end_kind": {"time"}, "night_end": {"06:30"},
		"night_weekend_end_kind": {"time"}, "night_weekend_end_time": {"08:30"},
		"dim_mode_enabled": {"on"},
		"dim_start_kind":   {"sunset"}, "dim_time": {"18:00"}, "dim_start_offset": {"-60"}, "dim_start_not_before": {"17:00"},
		"dim_end_kind": {"time"}, "dim_end_time": {"23:00"},
		"dim_weekend_end_kind": {"night_end"},
	}
	require.Equal(t, http.StatusSeeOther, postDeviceForm(t, s, &user, &device, form).Code)

	var saved data.Device
	require.NoError(t, s.DB.First(&saved, "id = ?", device.ID).Error)
	assert.Equal(t, &data.ScheduleTrigger{Kind: data.TriggerSunset, Offset: 30}, saved.NightSchedule.Start)
	assert.Nil(t, saved.NightSchedule.End, "a fixed end stays in NightEnd")
	assert.Equal(t, "06:30", saved.NightEnd)
	assert.Equal(t, &data.ScheduleTrigger{Kind: data.TriggerTime, Time: "08:30"}, saved.NightSchedule.WeekendEnd)
	assert.Equal(t, &data.ScheduleTrigger{Kind: data.TriggerSunset, Offset: -60, NotBefore: "17:00"}, saved.DimSchedule.Start)
	assert.Equal(t, &data.ScheduleTrigger{Kind: data.TriggerTime, Time: "23:00"}, saved.DimSchedule.End)
	assert.Equal(t, &data.ScheduleTrigger{Kind: data.TriggerNightEnd}, saved.DimSchedule.WeekendEnd)

	req := httptest.NewRequest(http.MethodGet, "/devices/"+device.ID+"/update", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &saved)
	rr := httptest.NewRecorder()
	s.handleUpdateDeviceGet(rr, req.WithContext(ctx))
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, `name="night_start_offset"`)
	assert.Contains(t, body, `<label for="night_weekend_end_kind">`)
	assert.Contains(t, body, `id="dim_time"`)
	assert.Regexp(t, `name="night_start_kind"[^>]*>\s*<option value="time"\s*>[^<]*</option>\s*<option value="sunset"\s*selected`, body)

	// Clearing the dim start time turns dim mode's schedule off, as before.
	form.Set("dim_start_kind", "time")
	form.Set("dim_time", "")
	require.Equal(t, http.StatusSeeOther, postDeviceForm(t, s, &user, &saved, form).Code)
	var cleared data.Device
	require.NoError(t, s.DB.First(&cleared, "id = ?", device.ID).Error)
	assert.Nil(t, cleared.DimTime)
	assert.Nil(t, cleared.DimSchedule.Start)

	// Without a location, sunrise and sunset can't be chosen.
	nowhere := data.Device{ID: "nowhere1", Username: "testuser", Name: "Nowhere", APIKey: "nowherekey"}
	require.NoError(t, s.DB.Create(&nowhere).Error)
	require.Equal(t, http.StatusSeeOther, postDeviceForm(t, s, &user, &nowhere, form).Code)
	var unsaved data.Device
	require.NoError(t, s.DB.First(&unsaved, "id = ?", nowhere.ID).Error)
	assert.Nil(t, unsaved.NightSchedule.Start, "nothing is saved")
}
