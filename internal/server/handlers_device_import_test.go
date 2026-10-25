package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postJSONFile posts v as the multipart "file" field to handler.
func postJSONFile(t *testing.T, handler http.HandlerFunc, path string, user *data.User, device *data.Device, v any) {
	t.Helper()
	payload, err := json.Marshal(v)
	require.NoError(t, err)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "config.json")
	require.NoError(t, err)
	_, err = part.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx := context.WithValue(req.Context(), userContextKey, user)
	if device != nil {
		ctx = context.WithValue(ctx, deviceContextKey, device)
	}
	rr := httptest.NewRecorder()
	handler(rr, req.WithContext(ctx))
	require.Equal(t, http.StatusSeeOther, rr.Code)
}

func importDeviceConfig(t *testing.T, s *Server, user *data.User, device *data.Device, imported data.Device) {
	t.Helper()
	postJSONFile(t, s.handleImportDeviceConfig, "/devices/"+device.ID+"/import_config", user, device, imported)
}

var invalidSchedule = data.ModeSchedule{End: &data.ScheduleTrigger{Kind: data.TriggerNightEnd}} // night can't end at night_end

func TestImportDeviceConfigCopiesSchedules(t *testing.T) {
	s := newTestServer(t)
	user := data.User{Username: "testuser"}
	require.NoError(t, s.DB.Create(&user).Error)
	device := data.Device{
		ID:            "importdev",
		Username:      "testuser",
		APIKey:        "importkey",
		NightSchedule: data.ModeSchedule{End: &data.ScheduleTrigger{Kind: data.TriggerTime, Time: "07:00"}},
	}
	require.NoError(t, s.DB.Create(&device).Error)

	imported := data.Device{
		Name: "window",
		DimSchedule: data.ModeSchedule{
			Start: &data.ScheduleTrigger{Kind: data.TriggerSunset, Offset: 30, NotBefore: "17:00"},
			End:   &data.ScheduleTrigger{Kind: data.TriggerNightEnd},
		},
	}
	importDeviceConfig(t, s, &user, &device, imported)

	var saved data.Device
	require.NoError(t, s.DB.First(&saved, "id = ?", device.ID).Error)
	assert.Equal(t, imported.DimSchedule, saved.DimSchedule)
	assert.Equal(t, data.ModeSchedule{}, saved.NightSchedule, "the target's old schedule is replaced, not kept")
}

func TestImportDeviceConfigRejectsInvalidSchedule(t *testing.T) {
	s := newTestServer(t)
	user := data.User{Username: "testuser"}
	require.NoError(t, s.DB.Create(&user).Error)
	device := data.Device{ID: "importdev", Username: "testuser", APIKey: "importkey", Name: "before"}
	require.NoError(t, s.DB.Create(&device).Error)

	imported := data.Device{Name: "after", NightSchedule: invalidSchedule}
	importDeviceConfig(t, s, &user, &device, imported)

	var saved data.Device
	require.NoError(t, s.DB.First(&saved, "id = ?", device.ID).Error)
	assert.Equal(t, "before", saved.Name, "nothing is imported")
}
