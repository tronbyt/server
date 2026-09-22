package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHandleCreateDevicePost(t *testing.T) {
	s := newTestServer(t)

	// Create user
	user := data.User{Username: "testuser"}
	s.DB.Create(&user)

	// Prepare form data
	form := url.Values{}
	form.Add("name", "New Device")
	form.Add("device_type", "tidbyt_gen1")
	form.Add("brightness", "2")

	req, _ := http.NewRequest(http.MethodPost, "/devices/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Inject user into context (simulating RequireLogin)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleCreateDevicePost)
	handler.ServeHTTP(rr, req)

	// Check redirect to dashboard
	if rr.Code != http.StatusSeeOther {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusSeeOther)
	}

	// Verify DB
	var device data.Device
	if err := s.DB.First(&device, "name = ?", "New Device").Error; err != nil {
		t.Fatalf("Device not created in DB")
	}
	if device.Username != "testuser" {
		t.Errorf("Device username mismatch")
	}
}

func TestHandleUpdateDevicePost(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{
		ID:         "testdevice",
		Username:   "testuser",
		Name:       "Old Name",
		Brightness: data.Brightness(20),
	}
	s.DB.Create(&device)

	form := url.Values{}
	form.Add("name", "New Name")
	form.Add("device_type", "tidbyt_gen2")
	form.Add("brightness", "5")
	form.Add("default_interval", "10")
	form.Add("color_filter", "redshift")

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/update", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleUpdateDevicePost)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusSeeOther)
	}

	var updatedDevice data.Device
	s.DB.First(&updatedDevice, "id = ?", "testdevice")
	if updatedDevice.Name != "New Name" {
		t.Errorf("Name not updated")
	}
	if *updatedDevice.ColorFilter != "redshift" {
		t.Errorf("Color filter not updated")
	}
}

func TestHandleDeleteDevice(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)
	app := data.App{DeviceID: "testdevice", Name: "TestApp", Iname: "100"}
	s.DB.Create(&app)

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/delete", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleDeleteDevice)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusSeeOther)
	}

	count, _ := gorm.G[data.Device](s.DB).Where("id = ?", "testdevice").Count(context.Background(), "*")
	if count != 0 {
		t.Errorf("Device not deleted")
	}
	count, _ = gorm.G[data.App](s.DB).Where("device_id = ?", "testdevice").Count(context.Background(), "*")
	if count != 0 {
		t.Errorf("App not deleted (cascade failed)")
	}
}

func TestHandleUpdateBrightness(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{
		ID:         "testdevice",
		Username:   "testuser",
		Name:       "Test Device",
		Brightness: data.Brightness(20),
	}
	s.DB.Create(&device)

	bUI := 4 // Define bUI here

	form := url.Values{}
	form.Add("brightness", strconv.Itoa(bUI))

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/update_brightness", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleUpdateBrightness)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	var updatedDevice data.Device
	s.DB.First(&updatedDevice, "id = ?", "testdevice")

	expectedBrightness := data.BrightnessFromUIScale(bUI, nil) // For UI 4, this should be 35
	if updatedDevice.Brightness != expectedBrightness {
		t.Errorf("Brightness not updated correctly: got %v want %v", updatedDevice.Brightness, expectedBrightness)
	}
}

func TestHandleUpdateBrightness_Invalid(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{
		ID:         "testdevice",
		Username:   "testuser",
		Name:       "Test Device",
		Brightness: data.Brightness(20),
	}
	s.DB.Create(&device)

	testCases := []string{"-1", "6", "abc"}

	for _, tc := range testCases {
		form := url.Values{}
		form.Add("brightness", tc)

		req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/update_brightness", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		ctx := context.WithValue(req.Context(), userContextKey, &user)
		ctx = context.WithValue(ctx, deviceContextKey, &device)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(s.handleUpdateBrightness)
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("handler returned wrong status code for input %s: got %v want %v", tc, rr.Code, http.StatusBadRequest)
		}
	}
}

func TestHandleUpdateInterval(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{
		ID:              "testdevice",
		Username:        "testuser",
		DefaultInterval: 15,
	}
	s.DB.Create(&device)

	form := url.Values{}
	form.Add("interval", "30")

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/update_interval", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleUpdateInterval)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	var updatedDevice data.Device
	s.DB.First(&updatedDevice, "id = ?", "testdevice")
	if updatedDevice.DefaultInterval != 30 {
		t.Errorf("Interval not updated, got %d", updatedDevice.DefaultInterval)
	}
}

func TestHandleUpdateInterval_Invalid(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{
		ID:              "testdevice",
		Username:        "testuser",
		DefaultInterval: 15,
	}
	s.DB.Create(&device)

	testCases := []string{"-1", "0", "abc"}

	for _, tc := range testCases {
		form := url.Values{}
		form.Add("interval", tc)

		req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/update_interval", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		ctx := context.WithValue(req.Context(), userContextKey, &user)
		ctx = context.WithValue(ctx, deviceContextKey, &device)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(s.handleUpdateInterval)
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("handler returned wrong status code for input %s: got %v want %v", tc, rr.Code, http.StatusBadRequest)
		}
		if !strings.Contains(rr.Body.String(), "Interval must be 1 or greater") {
			t.Errorf("handler returned wrong error message for input %s: got %s want %s", tc, rr.Body.String(), "Interval must be 1 or greater")
		}
	}
}

func TestHandleSetNightModeOverride(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{
		ID:               "testdevice",
		Username:         "testuser",
		Name:             "Test Device",
		NightModeEnabled: true,
		NightStart:       "22:00",
		NightEnd:         "06:00",
	}
	s.DB.Create(&device)

	form := url.Values{}
	form.Add("active", "true")

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/set_night_mode_override", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleSetNightModeOverride)
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var updatedDevice data.Device
	require.NoError(t, s.DB.First(&updatedDevice, "id = ?", "testdevice").Error)
	require.NotNil(t, updatedDevice.NightModeOverride)
	require.NotNil(t, updatedDevice.NightModeOverrideUntil)
	assert.True(t, *updatedDevice.NightModeOverride)
	assert.True(t, updatedDevice.NightModeOverrideUntil.After(time.Now().Add(-time.Minute)))
}

func TestHandleSetDimModeOverride(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	dimTime := "18:00"
	device := data.Device{
		ID:             "testdevice",
		Username:       "testuser",
		Name:           "Test Device",
		DimModeEnabled: true,
		DimTime:        &dimTime,
	}
	s.DB.Create(&device)

	form := url.Values{}
	form.Add("active", "true")

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/set_dim_mode_override", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleSetDimModeOverride)
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var updatedDevice data.Device
	require.NoError(t, s.DB.First(&updatedDevice, "id = ?", "testdevice").Error)
	require.NotNil(t, updatedDevice.DimModeOverride)
	require.NotNil(t, updatedDevice.DimModeOverrideUntil)
	assert.True(t, *updatedDevice.DimModeOverride)
	assert.True(t, updatedDevice.DimModeOverrideUntil.After(time.Now().Add(-time.Minute)))
}

func TestHandleUpdateFirmwareSettings_ColorOrder(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	user := data.User{Username: "testuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))
	device := data.Device{
		ID:       "testdevice",
		Username: "testuser",
		Name:     "Test Device",
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	post := func(value string) *httptest.ResponseRecorder {
		form := url.Values{}
		form.Add("color_order", value)

		req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/update_firmware_settings", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		reqCtx := context.WithValue(req.Context(), userContextKey, &user)
		reqCtx = context.WithValue(reqCtx, deviceContextKey, &device)
		req = req.WithContext(reqCtx)

		rr := httptest.NewRecorder()
		http.HandlerFunc(s.handleUpdateFirmwareSettings).ServeHTTP(rr, req)
		return rr
	}

	// An unknown order is rejected here rather than sent to the device.
	for _, tc := range []string{"xyz", "rgbb", "rg"} {
		assert.Equal(t, http.StatusBadRequest, post(tc).Code, "input %q", tc)
	}

	// The firmware compares case-insensitively, so any case is accepted and the
	// canonical lower-case form is what gets sent.
	ch := s.Broadcaster.Subscribe(device.ID)
	defer s.Broadcaster.Unsubscribe(device.ID, ch)

	rr := post("BGR")
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	select {
	case msg := <-ch:
		cmdMsg, ok := msg.(DeviceCommandMessage)
		require.True(t, ok, "unexpected message type from broadcaster: %T", msg)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(cmdMsg.Payload, &payload))
		assert.Equal(t, "bgr", payload["color_order"])
	case <-time.After(1 * time.Second):
		require.Fail(t, "timed out waiting for broadcaster notification")
	}
}

func TestHandleUpdateFirmwareSettings_TouchBeep(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	user := data.User{Username: "testuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))
	device := data.Device{
		ID:       "testdevice",
		Username: "testuser",
		Name:     "Test Device",
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	ch := s.Broadcaster.Subscribe(device.ID)
	defer s.Broadcaster.Unsubscribe(device.ID, ch)

	// The checkbox posts "true" or "false"; both must reach the device.
	for _, value := range []bool{true, false} {
		form := url.Values{}
		form.Add("touch_beep", strconv.FormatBool(value))

		req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/update_firmware_settings", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		reqCtx := context.WithValue(req.Context(), userContextKey, &user)
		reqCtx = context.WithValue(reqCtx, deviceContextKey, &device)
		req = req.WithContext(reqCtx)

		rr := httptest.NewRecorder()
		http.HandlerFunc(s.handleUpdateFirmwareSettings).ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

		select {
		case msg := <-ch:
			cmdMsg, ok := msg.(DeviceCommandMessage)
			require.True(t, ok, "unexpected message type from broadcaster: %T", msg)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(cmdMsg.Payload, &payload))
			assert.Len(t, payload, 1)
			assert.Equal(t, value, payload["touch_beep"])
		case <-time.After(1 * time.Second):
			require.Fail(t, "timed out waiting for broadcaster notification")
		}
	}
}

// The Beep On Touch checkbox is only offered for a Tidbyt Gen2 whose firmware
// reported touch_beep in client_info; older firmware never reports it.
func TestHandleUpdateDeviceGet_TouchBeepVisibility(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	user := data.User{Username: "testuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	for _, tc := range []struct {
		name        string
		deviceType  data.DeviceType
		touchBeep   *bool
		wantShown   bool
		wantChecked bool
	}{
		{"gen2 not reported", data.DeviceTidbytGen2, nil, false, false},
		{"gen2 reported off", data.DeviceTidbytGen2, new(false), true, false},
		{"gen2 reported on", data.DeviceTidbytGen2, new(true), true, true},
		{"gen1 reported on", data.DeviceTidbytGen1, new(true), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := gorm.G[data.Device](s.DB).Where("id = ?", "testdevice").Delete(ctx)
			require.NoError(t, err)

			device := data.Device{
				ID:       "testdevice",
				Username: "testuser",
				Name:     "Test Device",
				Type:     tc.deviceType,
			}
			device.Info.ProtocolType = data.ProtocolWS
			device.Info.FirmwareType = "ESP32"
			device.Info.FirmwareVersion = "dev"
			device.Info.TouchBeep = tc.touchBeep
			require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

			req, _ := http.NewRequest(http.MethodGet, "/devices/testdevice/update", nil)
			reqCtx := context.WithValue(req.Context(), userContextKey, &user)
			reqCtx = context.WithValue(reqCtx, deviceContextKey, &device)
			req = req.WithContext(reqCtx)

			rr := httptest.NewRecorder()
			http.HandlerFunc(s.handleUpdateDeviceGet).ServeHTTP(rr, req)
			require.Equal(t, http.StatusOK, rr.Code)

			body := rr.Body.String()
			_, after, shown := strings.Cut(body, `id="touch_beep"`)
			assert.Equal(t, tc.wantShown, shown)
			if shown {
				// Stop before the onchange handler, which mentions this.checked.
				attrs, _, _ := strings.Cut(after, "onchange=")
				assert.Equal(t, tc.wantChecked, strings.Contains(attrs, "checked"))
			}
		})
	}
}
