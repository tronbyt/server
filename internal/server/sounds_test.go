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

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

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

	// Verify pending sound cleared in DB after consumption
	dev, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	require.NoError(t, err)
	assert.Empty(t, dev.PendingSound)
}

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
	assert.Equal(t, "on_change", payload.NotificationSoundTrigger)

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

func TestNotificationSoundOptInAndTriggerLogic(t *testing.T) {
	s := newTestServerAPI(t)
	deviceID := "testdevice"
	fullCap := data.AudioCapFull
	_, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).Update(context.Background(), "audio_capability", fullCap)
	require.NoError(t, err)

	dev, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	require.NoError(t, err)

	baseURL := s.GetDeviceBaseURL(&dev)

	// 1. App without notification sound (default: opt-in, sound off)
	appOff := &data.App{
		ID:                       10,
		DeviceID:                 deviceID,
		NotificationSound:        nil,
		NotificationSoundTrigger: "",
		LastRenderHash:           "hash1",
	}
	// Trigger should not play when sound is nil
	assert.Nil(t, appOff.NotificationSound)

	// 2. App with sound but empty trigger -> should not play (off by default)
	soundName := "ding"
	appUnsetTrigger := &data.App{
		ID:                       11,
		DeviceID:                 deviceID,
		NotificationSound:        &soundName,
		NotificationSoundTrigger: "",
		LastRenderHash:           "hash1",
	}
	newHash := "hash2"
	contentChanged := (appUnsetTrigger.LastRenderHash != newHash)
	shouldPlay := false
	switch appUnsetTrigger.NotificationSoundTrigger {
	case "every_render":
		shouldPlay = true
	case "on_change":
		shouldPlay = contentChanged
	}
	assert.False(t, shouldPlay, "Unset trigger must be off by default")

	// 3. App with on_change trigger:
	appOnChange := &data.App{
		ID:                       12,
		DeviceID:                 deviceID,
		NotificationSound:        &soundName,
		NotificationSoundTrigger: "on_change",
		LastRenderHash:           "hash1",
	}

	// Same hash with on_change -> should NOT trigger
	contentChanged = (appOnChange.LastRenderHash != "hash1")
	assert.False(t, contentChanged)

	// Different hash with on_change -> triggers
	contentChanged = (appOnChange.LastRenderHash != newHash)
	assert.True(t, contentChanged)
	if contentChanged {
		require.NoError(t, s.TriggerDeviceSound(context.Background(), &dev, *appOnChange.NotificationSound, baseURL))
		appOnChange.LastRenderHash = newHash
	}
	devCheck, _ := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	assert.NotEmpty(t, devCheck.PendingSound)

	// Clear pending sound
	_, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).Update(context.Background(), "pending_sound", "")
	require.NoError(t, err)

	// Same hash again -> should not trigger
	contentChanged = (appOnChange.LastRenderHash != newHash)
	assert.False(t, contentChanged)
	devCheck, _ = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	assert.Empty(t, devCheck.PendingSound)

	// 4. every_render triggers even if hash is identical
	appEvery := &data.App{
		ID:                       13,
		DeviceID:                 deviceID,
		NotificationSound:        &soundName,
		NotificationSoundTrigger: "every_render",
		LastRenderHash:           newHash,
	}
	shouldPlayEvery := (appEvery.NotificationSoundTrigger == "every_render")
	assert.True(t, shouldPlayEvery)
	require.NoError(t, s.TriggerDeviceSound(context.Background(), &dev, *appEvery.NotificationSound, baseURL))
	devCheck, _ = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	assert.NotEmpty(t, devCheck.PendingSound)
}

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

func TestDeviceIsMuted(t *testing.T) {
	var nilDev *data.Device
	assert.False(t, nilDev.IsMuted())

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
