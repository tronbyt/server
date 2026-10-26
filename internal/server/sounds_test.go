package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestResolveSoundPayload verifies the expected behavior under test.
func TestResolveSoundPayload(t *testing.T) {
	s := newTestServerAPI(t)
	baseURL := "http://localhost:8000"

	// Ensure sounds dir exists and create a test custom sound file
	require.NoError(t, s.EnsureSoundsDir())
	customSoundPath := filepath.Join(s.SoundsDir(), "fanfare.mp3")
	require.NoError(t, os.WriteFile(customSoundPath, []byte("fake-mp3-data"), 0644))

	// Case 1: Device with AudioCapNone
	noneCap := data.AudioCapNone
	devNone := &data.Device{
		ID:              "dev-none",
		AudioCapability: &noneCap,
	}
	assert.Empty(t, s.ResolveSoundPayload(devNone, baseURL, "chime"))
	assert.Empty(t, s.ResolveSoundPayload(devNone, baseURL, "https://example.com/sound.mp3"))
	assert.Empty(t, s.ResolveSoundPayload(devNone, baseURL, "523:120,659:120"))

	// Case 2: Device with AudioCapPiezo
	piezoCap := data.AudioCapPiezo
	devPiezo := &data.Device{
		ID:              "dev-piezo",
		AudioCapability: &piezoCap,
	}
	assert.Equal(t, "523:120,659:120,784:220", s.ResolveSoundPayload(devPiezo, baseURL, "chime"))
	assert.Equal(t, "880:350", s.ResolveSoundPayload(devPiezo, baseURL, "ding"))
	assert.Equal(t, "587:180,440:320", s.ResolveSoundPayload(devPiezo, baseURL, "bell"))
	assert.Equal(t, "600:40,850:60", s.ResolveSoundPayload(devPiezo, baseURL, "pop"))
	assert.Equal(t, "784:100,0:50,784:150", s.ResolveSoundPayload(devPiezo, baseURL, "alert"))
	assert.Equal(t, "1047:40,0:15,1319:40,0:15,1568:40,0:15,2093:60,0:20,1568:50,0:15,2093:150", s.ResolveSoundPayload(devPiezo, baseURL, "tron"))
	// Raw tone format passes through
	assert.Equal(t, "440:100,880:200", s.ResolveSoundPayload(devPiezo, baseURL, "440:100,880:200"))
	// External URLs and custom files are not playable on piezo
	assert.Empty(t, s.ResolveSoundPayload(devPiezo, baseURL, "https://example.com/sound.mp3"))
	assert.Empty(t, s.ResolveSoundPayload(devPiezo, baseURL, "fanfare.mp3"))

	// Case 3: Device with AudioCapFull
	fullCap := data.AudioCapFull
	devFull := &data.Device{
		ID:              "dev-full",
		AudioCapability: &fullCap,
	}
	chimePayload := s.ResolveSoundPayload(devFull, baseURL, "chime")
	assert.Equal(t, "http://localhost:8000/static/sounds/chime.wav", chimePayload)

	customPayload := s.ResolveSoundPayload(devFull, baseURL, "fanfare.mp3")
	assert.Equal(t, "http://localhost:8000/static/custom_sounds/fanfare.mp3", customPayload)

	extURL := "https://example.com/alert.ogg"
	assert.Equal(t, extURL, s.ResolveSoundPayload(devFull, baseURL, extURL))

	// Reject CRLF injection
	assert.Empty(t, s.ResolveSoundPayload(devFull, baseURL, "https://example.com/alert.ogg\r\nX-Evil: true"))
	assert.Empty(t, s.ResolveSoundPayload(devFull, baseURL, "chime\nInjected: true"))
	assert.Empty(t, s.ResolveSoundPayload(devPiezo, baseURL, "523:120\r\nEvil: 1"))

	// Reject invalid scheme
	assert.Empty(t, s.ResolveSoundPayload(devFull, baseURL, "ftp://example.com/alert.ogg"))

	// Raw buzzer tones are not valid stream URLs
	assert.Empty(t, s.ResolveSoundPayload(devFull, baseURL, "523:120,659:120"))
}

// TestGetDefaultAndCustomSounds verifies the expected behavior under test.
func TestGetDefaultAndCustomSounds(t *testing.T) {
	s := newTestServerAPI(t)

	// Defaults
	defaults := GetDefaultSounds()
	assert.Len(t, defaults, 6)
	names := make([]string, len(defaults))
	for i, d := range defaults {
		names[i] = d.Name
	}
	assert.Contains(t, names, "Chime")
	assert.Contains(t, names, "Ding")
	assert.Contains(t, names, "Bell")
	assert.Contains(t, names, "Pop")
	assert.Contains(t, names, "Alert")
	assert.Contains(t, names, "Tron Bit")

	// Custom sounds from directory
	soundsDir := s.SoundsDir()
	require.NoError(t, os.MkdirAll(soundsDir, 0755))

	custom1 := filepath.Join(soundsDir, "my_chime.wav")
	require.NoError(t, os.WriteFile(custom1, []byte("fake-wav"), 0644))
	custom2 := filepath.Join(soundsDir, "ping.mp3")
	require.NoError(t, os.WriteFile(custom2, []byte("fake-mp3"), 0644))
	nonSound := filepath.Join(soundsDir, "notes.txt")
	require.NoError(t, os.WriteFile(nonSound, []byte("ignore me"), 0644))

	customs, err := s.GetCustomSounds()
	require.NoError(t, err)
	assert.Len(t, customs, 2)
	customFilenames := []string{customs[0].Name, customs[1].Name}
	assert.Contains(t, customFilenames, "my_chime.wav")
	assert.Contains(t, customFilenames, "ping.mp3")
	assert.NotContains(t, customFilenames, "notes.txt")
}

// TestAdminSoundUploadAndDelete verifies the expected behavior under test.
func TestAdminSoundUploadAndDelete(t *testing.T) {
	s := newTestServerAPI(t)
	var adminUser data.User
	require.NoError(t, s.DB.First(&adminUser, "username = ?", "admin").Error)

	// 1. Successful upload
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("sound_file", "test_sound.wav")
	require.NoError(t, err)
	_, err = io.Copy(part, strings.NewReader("RIFF....WAVEfmt "))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/settings/admin/sounds/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx := context.WithValue(req.Context(), userContextKey, &adminUser)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	s.handleAdminUploadSound(rr, req)
	assert.Equal(t, http.StatusSeeOther, rr.Code)

	uploadedFile := filepath.Join(s.SoundsDir(), "test_sound.wav")
	assert.FileExists(t, uploadedFile)

	// 2. Reject unsupported extension
	body = &bytes.Buffer{}
	writer = multipart.NewWriter(body)
	part, err = writer.CreateFormFile("sound_file", "bad_script.sh")
	require.NoError(t, err)
	_, err = io.Copy(part, strings.NewReader("#!/bin/sh\necho hi"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req = httptest.NewRequest(http.MethodPost, "/settings/admin/sounds/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = req.WithContext(ctx)

	rr = httptest.NewRecorder()
	s.handleAdminUploadSound(rr, req)
	assert.Equal(t, http.StatusSeeOther, rr.Code)
	assert.NoFileExists(t, filepath.Join(s.SoundsDir(), "bad_script.sh"))

	// 3. Reject empty/dot filename
	body = &bytes.Buffer{}
	writer = multipart.NewWriter(body)
	part, err = writer.CreateFormFile("sound_file", "")
	require.NoError(t, err)
	_, err = io.Copy(part, strings.NewReader("dummy"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req = httptest.NewRequest(http.MethodPost, "/settings/admin/sounds/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = req.WithContext(ctx)

	rr = httptest.NewRecorder()
	s.handleAdminUploadSound(rr, req)
	assert.Equal(t, http.StatusSeeOther, rr.Code)

	// 4. Delete sound
	req = httptest.NewRequest(http.MethodPost, "/settings/admin/sounds/test_sound.wav/delete", nil)
	req.SetPathValue("filename", "test_sound.wav")
	req = req.WithContext(ctx)

	rr = httptest.NewRecorder()
	s.handleAdminDeleteSound(rr, req)
	assert.Equal(t, http.StatusSeeOther, rr.Code)
	assert.NoFileExists(t, uploadedFile)
}

// TestStaticCustomSoundsDirectoryBrowsingDisabled verifies the expected behavior under test.
func TestStaticCustomSoundsDirectoryBrowsingDisabled(t *testing.T) {
	s := newTestServerAPI(t)
	require.NoError(t, s.EnsureSoundsDir())

	testFile := filepath.Join(s.SoundsDir(), "sample.wav")
	require.NoError(t, os.WriteFile(testFile, []byte("sample-wav-bytes"), 0644))

	// Request to directory root -> 404 Not Found (no directory listing)
	req := httptest.NewRequest(http.MethodGet, "/static/custom_sounds/", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)

	// Request to valid file -> 200 OK
	req = httptest.NewRequest(http.MethodGet, "/static/custom_sounds/sample.wav", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "sample-wav-bytes", rr.Body.String())
}

// TestTriggerSoundAPI verifies the expected behavior under test.
func TestTriggerSoundAPI(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
	deviceID := "testdevice"

	// Configure device audio capability to full
	fullCap := data.AudioCapFull
	_, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).Update(context.Background(), "audio_capability", fullCap)
	require.NoError(t, err)

	// 1. Missing auth
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v0/devices/%s/sound", deviceID), strings.NewReader(`{"sound":"chime"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// 2. Valid request with Bearer token
	req = newAPIRequest(http.MethodPost, fmt.Sprintf("/v0/devices/%s/sound", deviceID), apiKey, []byte(`{"sound":"chime"}`))
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"status":"ok"`)

	// Verify device pending sound updated in DB
	dev, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, dev.PendingSound)
	assert.True(t, strings.HasSuffix(dev.PendingSound, "/static/sounds/chime.wav"))

	// Test in-memory pointer update in TriggerDeviceSound (Fix 1 verification)
	devMem := dev
	devMem.PendingSound = ""
	require.NoError(t, s.TriggerDeviceSound(context.Background(), &devMem, "chime", "http://localhost:8000"))
	assert.NotEmpty(t, devMem.PendingSound, "in-memory device pointer must have PendingSound updated immediately")

	// 3. Valid request using ?key= query parameter
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v0/devices/%s/sound?key=%s", deviceID, apiKey), strings.NewReader(`{"url":"https://example.com/bell.mp3"}`))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	dev, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/bell.mp3", dev.PendingSound)

	// 4. Pending sound is consumed on device next poll
	pollReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/%s/next", deviceID), nil)
	pollRr := httptest.NewRecorder()
	s.ServeHTTP(pollRr, pollReq)
	assert.Equal(t, "https://example.com/bell.mp3", pollRr.Header().Get("Tronbyt-Sound"))
	assert.Equal(t, "100", pollRr.Header().Get("Tronbyt-Volume"))

	// Verify pending sound cleared in DB after consumption
	dev, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	require.NoError(t, err)
	assert.Empty(t, dev.PendingSound)
}

// TestPatchAndGetInstallationNotificationSound verifies the expected behavior under test.
func TestPatchAndGetInstallationNotificationSound(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"
	installID := "sound_app"

	app := data.App{
		DeviceID:    deviceID,
		Iname:       installID,
		Name:        "Sound App",
		UInterval:   10,
		DisplayTime: 10,
		Enabled:     true,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(context.Background(), &app))

	sound := "ding"
	trigger := "on_change"
	update := InstallationUpdate{
		NotificationSound:        &sound,
		NotificationSoundTrigger: &trigger,
	}
	body, err := json.Marshal(update)
	require.NoError(t, err)

	req := newAPIRequest("PATCH", fmt.Sprintf("/v0/devices/%s/installations/%s", deviceID, installID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	// Verify in DB
	updated, err := gorm.G[data.App](s.DB).Where("iname = ?", installID).First(context.Background())
	require.NoError(t, err)
	if assert.NotNil(t, updated.NotificationSound) {
		assert.Equal(t, "ding", *updated.NotificationSound)
	}
	assert.Equal(t, "on_change", updated.NotificationSoundTrigger)

	// Verify GET response
	req = newAPIRequest("GET", fmt.Sprintf("/v0/devices/%s/installations/%s", deviceID, installID), apiKey, nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var payload AppPayload
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&payload))
	if assert.NotNil(t, payload.NotificationSound) {
		assert.Equal(t, "ding", *payload.NotificationSound)
	}
	// Update with every_display trigger
	everyDisplayTrigger := "every_display"
	updateReqEveryDisplay := InstallationUpdate{
		NotificationSoundTrigger: &everyDisplayTrigger,
	}
	body, err = json.Marshal(updateReqEveryDisplay)
	require.NoError(t, err)

	req = newAPIRequest("PATCH", fmt.Sprintf("/v0/devices/%s/installations/%s", deviceID, installID), apiKey, body)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	updatedDisplay, err := gorm.G[data.App](s.DB).Where("iname = ?", installID).First(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "every_display", updatedDisplay.NotificationSoundTrigger)

	// Test clearing notification sound (sound is opt-in, so clearing also turns off trigger)
	emptySound := ""
	clearUpdate := InstallationUpdate{
		NotificationSound: &emptySound,
	}
	body, err = json.Marshal(clearUpdate)
	require.NoError(t, err)

	req = newAPIRequest("PATCH", fmt.Sprintf("/v0/devices/%s/installations/%s", deviceID, installID), apiKey, body)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	cleared, err := gorm.G[data.App](s.DB).Where("iname = ?", installID).First(context.Background())
	require.NoError(t, err)
	assert.Nil(t, cleared.NotificationSound)
	assert.Empty(t, cleared.NotificationSoundTrigger)
}

// TestNotificationSoundOptInAndTriggerLogic verifies the expected behavior under test.
func TestNotificationSoundOptInAndTriggerLogic(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()
	deviceID := "testdevice"
	fullCap := data.AudioCapFull
	_, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).Update(ctx, "audio_capability", fullCap)
	require.NoError(t, err)

	dev, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(ctx)
	require.NoError(t, err)
	user, err := gorm.G[data.User](s.DB).Where("username = ?", dev.Username).First(ctx)
	require.NoError(t, err)

	appRelPath := filepath.Join("system-apps", "apps", "optinapp", "optinapp.star")
	appFullPath := filepath.Join(s.DataDir, appRelPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(appFullPath), 0755))
	starV1 := `
load("render.star", "render")
def main(config):
    return render.Root(child=render.Box(width=64, height=32, color="#00ff00"))
`
	require.NoError(t, os.WriteFile(appFullPath, []byte(starV1), 0644))

	// 1. App without notification sound (default: opt-in, sound off)
	appOff := data.App{
		Iname:                    "optin-1",
		Name:                     "optinapp",
		DeviceID:                 deviceID,
		Path:                     &appRelPath,
		NotificationSound:        nil,
		NotificationSoundTrigger: "",
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &appOff))
	require.True(t, s.possiblyRender(ctx, &appOff, &dev, &user))
	devCheck, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(ctx)
	require.NoError(t, err)
	assert.Empty(t, devCheck.PendingSound)
	assert.Nil(t, appOff.LastSoundAt)

	// 2. App with sound but empty trigger -> should not play (off by default)
	soundName := "ding"
	appUnsetTrigger := data.App{
		Iname:                    "optin-2",
		Name:                     "optinapp",
		DeviceID:                 deviceID,
		Path:                     &appRelPath,
		NotificationSound:        &soundName,
		NotificationSoundTrigger: "",
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &appUnsetTrigger))
	require.True(t, s.possiblyRender(ctx, &appUnsetTrigger, &dev, &user))
	devCheck, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(ctx)
	require.NoError(t, err)
	assert.Empty(t, devCheck.PendingSound, "Unset trigger must be off by default")
	assert.Nil(t, appUnsetTrigger.LastSoundAt)

	// 3. App with on_change trigger:
	appOnChange := data.App{
		Iname:                    "optin-3",
		Name:                     "optinapp",
		DeviceID:                 deviceID,
		Path:                     &appRelPath,
		NotificationSound:        &soundName,
		NotificationSoundTrigger: "on_change",
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &appOnChange))

	// First render (hash changes from empty to rendered hash) -> triggers sound
	require.True(t, s.possiblyRender(ctx, &appOnChange, &dev, &user))
	devCheck, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, devCheck.PendingSound)
	assert.NotNil(t, appOnChange.LastSoundAt)

	// Clear pending sound and cooldown, then re-render identical content -> should NOT trigger
	_, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).Update(ctx, "pending_sound", "")
	require.NoError(t, err)
	dev.PendingSound = ""
	appOnChange.LastSoundAt = nil
	appOnChange.LastRender = time.Time{}

	require.True(t, s.possiblyRender(ctx, &appOnChange, &dev, &user))
	devCheck, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(ctx)
	require.NoError(t, err)
	assert.Empty(t, devCheck.PendingSound, "Same render hash with on_change must not trigger sound")

	// 4. every_display triggers on device display, not during background render
	appDisplay := data.App{
		Iname:                    "optin-4",
		Name:                     "optinapp",
		DeviceID:                 deviceID,
		Path:                     &appRelPath,
		NotificationSound:        &soundName,
		NotificationSoundTrigger: "every_display",
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &appDisplay))
	require.True(t, s.possiblyRender(ctx, &appDisplay, &dev, &user))
	devCheck, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(ctx)
	require.NoError(t, err)
	assert.Empty(t, devCheck.PendingSound, "every_display must not trigger in render loop")
}

// TestGetNextAppImage_EveryDisplaySoundNotification verifies the expected behavior under test.
func TestGetNextAppImage_EveryDisplaySoundNotification(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	user := data.User{Username: "displayuser"}
	require.NoError(t, s.DB.Create(&user).Error)

	sound := "chime"
	pushedPath := "pushed:clock1"
	device := data.Device{
		ID:       "devdisp1",
		Username: "displayuser",
		Type:     data.DeviceRaspberryPiWide,
		Muted:    false,
		Info: data.DeviceInfo{
			ProtocolType: data.ProtocolHTTP,
		},
		Apps: []*data.App{
			{
				Iname:                    "clock1",
				DeviceID:                 "devdisp1",
				Enabled:                  true,
				Pushed:                   true,
				Path:                     &pushedPath,
				NotificationSound:        &sound,
				NotificationSoundTrigger: "every_display",
				Order:                    1,
			},
		},
	}
	require.NoError(t, s.DB.Create(&device).Error)

	pushedDir := filepath.Join(s.DataDir, "webp", device.ID, "pushed")
	require.NoError(t, os.MkdirAll(pushedDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pushedDir, "clock1.webp"), []byte("fake-webp"), 0644))

	// Call GetNextAppImage on HTTP device
	_, app, err := s.GetNextAppImage(ctx, &device, &user, "http://localhost:8000")
	require.NoError(t, err)
	require.NotNil(t, app)
	assert.Equal(t, "clock1", app.Iname)

	// Verify pending_sound was queued for HTTP device
	reloaded, err := gorm.G[data.Device](s.DB).Where("id = ?", "devdisp1").First(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, reloaded.PendingSound, "every_display must set pending_sound on display for HTTP devices")
	assert.Contains(t, reloaded.PendingSound, "chime")
}

// TestUnifiedSoundRoute verifies the expected behavior under test.
func TestUnifiedSoundRoute(t *testing.T) {
	s := newTestServerAPI(t)

	// 1. Built-in sound
	req := httptest.NewRequest(http.MethodGet, "/sounds/default/chime.wav", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.NotEmpty(t, rr.Body.Bytes())

	req = httptest.NewRequest(http.MethodGet, "/api/sounds/alert.wav", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	// 2. Custom uploaded sound
	require.NoError(t, s.EnsureSoundsDir())
	customSoundPath := filepath.Join(s.SoundsDir(), "custom_bell.wav")
	require.NoError(t, os.WriteFile(customSoundPath, []byte("custom-bell-bytes"), 0644))

	req = httptest.NewRequest(http.MethodGet, "/sounds/custom/custom_bell.wav", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "custom-bell-bytes", rr.Body.String())

	// 3. App sound
	appDir := filepath.Join(s.DataDir, "system-apps", "apps", "test_sports")
	require.NoError(t, os.MkdirAll(appDir, 0755))
	appSoundPath := filepath.Join(appDir, "touchdown.mp3")
	require.NoError(t, os.WriteFile(appSoundPath, []byte("touchdown-audio-bytes"), 0644))

	// Fetch via /sounds/{appId}/{filename}
	req = httptest.NewRequest(http.MethodGet, "/sounds/test_sports/touchdown.mp3", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "touchdown-audio-bytes", rr.Body.String())

	// Fetch via query param ?appId=...
	req = httptest.NewRequest(http.MethodGet, "/sounds/app/touchdown.mp3?appId=test_sports", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "touchdown-audio-bytes", rr.Body.String())

	// Fetch via /api/sounds/touchdown.mp3?appId=test_sports
	req = httptest.NewRequest(http.MethodGet, "/api/sounds/touchdown.mp3?appId=test_sports", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "touchdown-audio-bytes", rr.Body.String())

	// 4. Security checks: Directory traversal attempts
	req = httptest.NewRequest(http.MethodGet, "/sounds/test_sports/..%2F..%2Fetc%2Fpasswd", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.NotEqual(t, http.StatusOK, rr.Code)

	// Non-audio extension
	secretTxt := filepath.Join(appDir, "secret.txt")
	require.NoError(t, os.WriteFile(secretTxt, []byte("secret"), 0644))
	req = httptest.NewRequest(http.MethodGet, "/sounds/test_sports/secret.txt", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)

	// Non-existent file
	req = httptest.NewRequest(http.MethodGet, "/sounds/test_sports/missing.wav", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// TestResolveSoundPayload_AppSounds verifies the expected behavior under test.
func TestResolveSoundPayload_AppSounds(t *testing.T) {
	s := newTestServerAPI(t)
	baseURL := "http://localhost:8000"

	piezoCap := data.AudioCapPiezo
	devPiezo := &data.Device{ID: "piezo-dev", AudioCapability: &piezoCap}

	fullCap := data.AudioCapFull
	devFull := &data.Device{ID: "full-dev", AudioCapability: &fullCap}

	noneCap := data.AudioCapNone
	devNone := &data.Device{ID: "none-dev", AudioCapability: &noneCap}

	// 1. Full capability returns streaming URL
	assert.Equal(t, "http://localhost:8000/sounds/nfl/touchdown.mp3", s.ResolveSoundPayload(devFull, baseURL, "app:nfl:touchdown.mp3"))

	// 2. Piezo capability returns Option A keywords or pleasant 2-tone fallback
	assert.Equal(t, "880:350", s.ResolveSoundPayload(devPiezo, baseURL, "app:nfl:ding.mp3"))
	assert.Equal(t, "523:120,659:120,784:220", s.ResolveSoundPayload(devPiezo, baseURL, "app:nfl:goal_chime.wav"))
	assert.Equal(t, "587:180,440:320", s.ResolveSoundPayload(devPiezo, baseURL, "app:nfl:last_bell.mp3"))
	assert.Equal(t, "600:40,850:60", s.ResolveSoundPayload(devPiezo, baseURL, "app:nfl:pop.ogg"))
	assert.Equal(t, "784:100,0:50,784:150", s.ResolveSoundPayload(devPiezo, baseURL, "app:nfl:alert.mp3"))
	// Unknown keyword falls back to 2-tone chime
	assert.Equal(t, "659:120,880:180", s.ResolveSoundPayload(devPiezo, baseURL, "app:nfl:touchdown.mp3"))

	// 3. None capability returns empty string
	assert.Empty(t, s.ResolveSoundPayload(devNone, baseURL, "app:nfl:touchdown.mp3"))
}

// TestExtractAppSounds verifies the expected behavior under test.
func TestExtractAppSounds(t *testing.T) {
	schemaJSON := []byte(`{
		"version": "1",
		"schema": [
			{
				"type": "notification",
				"id": "notif1",
				"sounds": [
					{"id": "snd1", "title": "Touchdown Horn", "path": "horn.mp3"}
				]
			}
		],
		"notifications": [
			{
				"id": "notif2",
				"name": "Goal",
				"sounds": [
					{"id": "snd2", "title": "Goal Chime", "path": "sounds/chime.wav"}
				]
			}
		]
	}`)

	sounds := extractAppSounds(schemaJSON, "sports_app")
	require.Len(t, sounds, 2)

	assert.Equal(t, "app:sports_app:horn.mp3", sounds[0].ID)
	assert.Equal(t, "Touchdown Horn", sounds[0].Name)
	assert.Equal(t, "/sounds/sports_app/horn.mp3", sounds[0].URL)
	assert.Equal(t, "659:120,880:180", sounds[0].Tone)

	assert.Equal(t, "app:sports_app:chime.wav", sounds[1].ID)
	assert.Equal(t, "Goal Chime", sounds[1].Name)
	assert.Equal(t, "/sounds/sports_app/chime.wav", sounds[1].URL)
	assert.Equal(t, "523:120,659:120,784:220", sounds[1].Tone)
}

// TestPushAppWithSound verifies the expected behavior under test.
func TestPushAppWithSound(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
	deviceID := "testdevice"

	fullCap := data.AudioCapFull
	_, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).Update(context.Background(), "audio_capability", fullCap)
	require.NoError(t, err)

	// Test handleTriggerSoundAPI with app_id
	body := `{"app_id":"nfl","sound":"touchdown.mp3"}`
	req := newAPIRequest(http.MethodPost, fmt.Sprintf("/v0/devices/%s/sound", deviceID), apiKey, []byte(body))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	dev, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	require.NoError(t, err)
	assert.Contains(t, dev.PendingSound, "/sounds/nfl/touchdown.mp3")
}

// TestTriggerSoundAPI_MutedAndUnplayable verifies the expected behavior under test.
func TestTriggerSoundAPI_MutedAndUnplayable(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
	deviceID := "testdevice"

	// 1. Muted device returns 409 Conflict
	_, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).Updates(context.Background(), data.Device{
		Muted:           true,
		AudioCapability: func(c data.AudioCapability) *data.AudioCapability { return &c }(data.AudioCapFull),
	})
	require.NoError(t, err)

	body := `{"sound":"default:chime"}`
	req := newAPIRequest(http.MethodPost, fmt.Sprintf("/v0/devices/%s/sound", deviceID), apiKey, []byte(body))
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusConflict, rr.Code)
	assert.Contains(t, rr.Body.String(), "Device is muted")

	// 2. Unmute, but set audio capability to none -> returns 400 Bad Request
	require.NoError(t, s.DB.Model(&data.Device{ID: deviceID}).Update("muted", false).Error)
	noneCap := data.AudioCapNone
	require.NoError(t, s.DB.Model(&data.Device{ID: deviceID}).Update("audio_capability", &noneCap).Error)

	req = newAPIRequest(http.MethodPost, fmt.Sprintf("/v0/devices/%s/sound", deviceID), apiKey, []byte(body))
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "Device audio capability is disabled or none")

	// 3. Audio capability piezo, but sound is an external audio URL that cannot play on piezo -> returns 400 Bad Request
	piezoCap := data.AudioCapPiezo
	require.NoError(t, s.DB.Model(&data.Device{ID: deviceID}).Update("audio_capability", &piezoCap).Error)

	urlBody := `{"sound":"https://example.com/audio.mp3"}`
	req = newAPIRequest(http.MethodPost, fmt.Sprintf("/v0/devices/%s/sound", deviceID), apiKey, []byte(urlBody))
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "Sound not found or not playable on this device")
}

// TestDeviceIsMuted verifies the expected behavior under test.
func TestDeviceIsMuted(t *testing.T) {
	var emptyDev data.Device
	assert.False(t, emptyDev.IsMuted())

	dev := &data.Device{
		Muted: false,
	}
	assert.False(t, dev.IsMuted())

	dev.Muted = true
	assert.True(t, dev.IsMuted())

	// Unmuted manual, test NightModeMute
	dev.Muted = false
	dev.NightModeMute = true
	dev.NightModeEnabled = false
	assert.False(t, dev.IsMuted())

	// Night mode enabled and active (00:00 to 23:59 covers all day)
	dev.NightModeEnabled = true
	dev.NightStart = "00:00"
	dev.NightEnd = "23:59"
	assert.True(t, dev.IsMuted())
}

// TestTriggerDeviceSoundMuted verifies the expected behavior under test.
func TestTriggerDeviceSoundMuted(t *testing.T) {
	s := newTestServerAPI(t)
	deviceID := "testdevice"

	piezoCap := data.AudioCapPiezo
	err := s.DB.Model(&data.Device{ID: deviceID}).Updates(map[string]any{
		"audio_capability": piezoCap,
		"muted":            true,
	}).Error
	require.NoError(t, err)

	dev, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	require.NoError(t, err)

	err = s.TriggerDeviceSound(context.Background(), &dev, "chime", "http://localhost:8000")
	require.NoError(t, err)

	// PendingSound should NOT be updated because device is muted
	reloaded, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	require.NoError(t, err)
	assert.Empty(t, reloaded.PendingSound)
}

// TestDeviceVolume verifies the expected behavior under test.
func TestDeviceVolume(t *testing.T) {
	var emptyDev data.Device
	assert.Equal(t, 0, emptyDev.GetVolume())

	devZero := &data.Device{Volume: 0}
	assert.Equal(t, 0, devZero.GetVolume())

	devCustom := &data.Device{Volume: 65}
	assert.Equal(t, 65, devCustom.GetVolume())

	devNegative := &data.Device{Volume: -10}
	assert.Equal(t, 0, devNegative.GetVolume())

	devHigh := &data.Device{Volume: 150}
	assert.Equal(t, 100, devHigh.GetVolume())
}

// TestDeviceEffectiveVolume_NightAndDimModes verifies the expected behavior under test.
func TestDeviceEffectiveVolume_NightAndDimModes(t *testing.T) {
	trueVal := true
	future := time.Now().Add(1 * time.Hour)
	nmVol := 35
	dmVol := 55

	// Case 1: Normal mode (neither night nor dim mode active)
	devNormal := &data.Device{Volume: 80}
	assert.Equal(t, 80, devNormal.GetEffectiveVolume())
	assert.Equal(t, 80, devNormal.GetNightModeVolume())
	assert.Equal(t, 80, devNormal.GetDimModeVolume())
	assert.False(t, devNormal.IsMuted())

	// Case 2: Night mode active with custom volume
	devNightVol := &data.Device{
		Volume:                 80,
		NightModeEnabled:       true,
		NightModeOverride:      &trueVal,
		NightModeOverrideUntil: &future,
		NightModeVolume:        &nmVol,
	}
	assert.True(t, devNightVol.GetNightModeIsActive())
	assert.Equal(t, 35, devNightVol.GetEffectiveVolume())
	assert.Equal(t, 35, devNightVol.GetNightModeVolume())
	assert.False(t, devNightVol.IsMuted())

	// Case 3: Night mode active with NightModeMute
	devNightMute := &data.Device{
		Volume:                 80,
		NightModeEnabled:       true,
		NightModeOverride:      &trueVal,
		NightModeOverrideUntil: &future,
		NightModeMute:          true,
	}
	assert.True(t, devNightMute.GetNightModeIsActive())
	assert.True(t, devNightMute.IsMuted())

	// Case 4: Dim mode active with custom volume
	devDimVol := &data.Device{
		Volume:               80,
		DimModeEnabled:       true,
		DimModeOverride:      &trueVal,
		DimModeOverrideUntil: &future,
		DimModeVolume:        &dmVol,
	}
	assert.True(t, devDimVol.GetDimModeIsActive())
	assert.Equal(t, 55, devDimVol.GetEffectiveVolume())
	assert.Equal(t, 55, devDimVol.GetDimModeVolume())
	assert.False(t, devDimVol.IsMuted())

	// Case 5: Dim mode active with DimModeMute
	devDimMute := &data.Device{
		Volume:               80,
		DimModeEnabled:       true,
		DimModeOverride:      &trueVal,
		DimModeOverrideUntil: &future,
		DimModeMute:          true,
	}
	assert.True(t, devDimMute.GetDimModeIsActive())
	assert.True(t, devDimMute.IsMuted())

	// Case 6: Fallback when mode volumes are nil
	devNilModes := &data.Device{
		Volume:                 70,
		NightModeEnabled:       true,
		NightModeOverride:      &trueVal,
		NightModeOverrideUntil: &future,
		NightModeVolume:        nil,
		DimModeVolume:          nil,
	}
	assert.Equal(t, 70, devNilModes.GetEffectiveVolume())
	assert.Equal(t, 70, devNilModes.GetNightModeVolume())
	assert.Equal(t, 70, devNilModes.GetDimModeVolume())
}

// TestSoundDebounceCooldown verifies the expected behavior under test.
func TestSoundDebounceCooldown(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()
	deviceID := "testdevice"
	fullCap := data.AudioCapFull
	_, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).Update(ctx, "audio_capability", fullCap)
	require.NoError(t, err)

	dev, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(ctx)
	require.NoError(t, err)
	user, err := gorm.G[data.User](s.DB).Where("username = ?", dev.Username).First(ctx)
	require.NoError(t, err)

	appRelPath := filepath.Join("system-apps", "apps", "cooldownapp", "cooldownapp.star")
	appFullPath := filepath.Join(s.DataDir, appRelPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(appFullPath), 0755))

	writeStar := func(color string) {
		script := fmt.Sprintf(`
load("render.star", "render")
def main(config):
    return render.Root(child=render.Box(width=64, height=32, color="%s"))
`, color)
		require.NoError(t, os.WriteFile(appFullPath, []byte(script), 0644))
	}
	writeStar("#ff0000")

	sound := "default:chime"
	app := data.App{
		Iname:                    "cooldown-1",
		Name:                     "cooldownapp",
		DeviceID:                 deviceID,
		Path:                     &appRelPath,
		NotificationSound:        &sound,
		NotificationSoundTrigger: "on_change",
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))

	// Case 1: First render triggers sound and sets LastSoundAt
	require.True(t, s.possiblyRender(ctx, &app, &dev, &user))
	require.NotNil(t, app.LastSoundAt)
	devCheck, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, devCheck.PendingSound)

	// Clear pending sound and simulate a render 5 seconds after LastSoundAt (within 30s cooldown)
	_, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).Update(ctx, "pending_sound", "")
	require.NoError(t, err)
	dev.PendingSound = ""
	writeStar("#0000ff")
	recentSoundAt := time.Now().Add(-5 * time.Second)
	app.LastSoundAt = &recentSoundAt
	app.LastRender = time.Time{}

	// Case 2: Content changed within 30s cooldown -> suppressed by possiblyRender
	require.True(t, s.possiblyRender(ctx, &app, &dev, &user))
	devCheck, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(ctx)
	require.NoError(t, err)
	assert.Empty(t, devCheck.PendingSound, "Sound should be suppressed within 30s cooldown")
	assert.Equal(t, recentSoundAt, *app.LastSoundAt)

	// Case 3: Simulate a render 35 seconds after LastSoundAt (beyond 30s cooldown)
	writeStar("#00ff00")
	expiredSoundAt := time.Now().Add(-35 * time.Second)
	app.LastSoundAt = &expiredSoundAt
	app.LastRender = time.Time{}

	require.True(t, s.possiblyRender(ctx, &app, &dev, &user))
	devCheck, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, devCheck.PendingSound, "Sound should play after 30s cooldown has elapsed")
	assert.True(t, app.LastSoundAt.After(expiredSoundAt))
}

// TestCustomSoundsSecurityAndDirectoryTraversal verifies the expected behavior under test.
func TestCustomSoundsSecurityAndDirectoryTraversal(t *testing.T) {
	s := newTestServerAPI(t)
	require.NoError(t, s.EnsureSoundsDir())

	// 1. Valid custom sound file
	customWav := filepath.Join(s.SoundsDir(), "sample.wav")
	require.NoError(t, os.WriteFile(customWav, []byte("valid-wav-content"), 0644))

	req := httptest.NewRequest(http.MethodGet, "/static/custom_sounds/sample.wav", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "valid-wav-content", rr.Body.String())

	// 2. Non-audio file in soundsDir must be rejected
	nonAudioFile := filepath.Join(s.SoundsDir(), "secret.txt")
	require.NoError(t, os.WriteFile(nonAudioFile, []byte("sensitive-data"), 0644))

	req = httptest.NewRequest(http.MethodGet, "/static/custom_sounds/secret.txt", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)

	// 3. Subdirectory in soundsDir must not be readable as a file or directory listing
	subDir := filepath.Join(s.SoundsDir(), "subfolder")
	require.NoError(t, os.MkdirAll(subDir, 0755))

	req = httptest.NewRequest(http.MethodGet, "/static/custom_sounds/subfolder", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)

	req = httptest.NewRequest(http.MethodGet, "/static/custom_sounds/subfolder/", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)

	// 4. Directory traversal attempts in /static/custom_sounds/
	for _, path := range []string{
		"/static/custom_sounds/..%2f..%2fetc%2fpasswd",
		"/static/custom_sounds/%2e%2e%2fsample.wav",
		"/static/custom_sounds/..%2f..%2f..%2fetc%2fshadow",
	} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		rr = httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code, "Path %s must return 404", path)
	}

	// 5. Multi-user directory traversal protection:
	// A file in a user's directory must not be accessible via appId="users/victim"
	victimDir := filepath.Join(s.DataDir, "users", "victim")
	require.NoError(t, os.MkdirAll(victimDir, 0755))
	victimSound := filepath.Join(victimDir, "victim_sound.wav")
	require.NoError(t, os.WriteFile(victimSound, []byte("victim-audio"), 0644))

	// Attempting to resolve via appId="users/victim" directly
	req = httptest.NewRequest(http.MethodGet, "/sounds/app/victim_sound.wav?appId=users/victim", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code, "Probing user folder directly via appId must return 404")

	// Attempting to resolve via path traversal appId="../users/victim"
	req = httptest.NewRequest(http.MethodGet, "/sounds/app/victim_sound.wav?appId=../users/victim", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code, "Traversal via appId must return 404")

	// 6. Cross-user app isolation: unauthenticated or other-user requests must not read another user's private app sounds
	victimAppDir := filepath.Join(s.DataDir, "users", "victim", "apps", "private_app")
	require.NoError(t, os.MkdirAll(victimAppDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(victimAppDir, "secret.wav"), []byte("private-audio"), 0644))

	// Unauthenticated request by unqualified app basename must return 404
	req = httptest.NewRequest(http.MethodGet, "/sounds/private_app/secret.wav", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code, "Unauthenticated request to private user app sound must return 404")

	// Authenticated request with a different user's API key must return 404
	req = httptest.NewRequest(http.MethodGet, "/sounds/private_app/secret.wav?key=test_api_key", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code, "Different user must not access victim user app sound")

	// Authenticated request by the actual owner succeeds
	victimUser := data.User{Username: "victim", APIKey: "victim_api_key"}
	require.NoError(t, s.DB.Create(&victimUser).Error)
	req = httptest.NewRequest(http.MethodGet, "/sounds/private_app/secret.wav?key=victim_api_key", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code, "Owner should be able to access their own app sound")
	assert.Equal(t, "private-audio", rr.Body.String())
}
