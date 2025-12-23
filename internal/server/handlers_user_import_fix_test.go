package server

import (
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleImportUserConfig_VerifyIDs(t *testing.T) {
	s := newTestServer(t)

	// Create initial user
	user := data.User{
		Username: "testuser",
		APIKey:   "apikey",
	}
	err := s.DB.Create(&user).Error
	require.NoError(t, err, "Failed to create initial user")

	// Create import payload (User with Device with App having ID=999 in JSON)
	importedUser := data.User{
		Username: "testuser",
		APIKey:   "newapikey",
		Devices: []data.Device{
			{
				ID:   "dev1",
				Name: "Device 1",
				Apps: []data.App{
					{
						ID:    999, // Should be ignored/reset
						Iname: "100",
						Name:  "Test App",
					},
				},
			},
		},
	}
	jsonData, err := json.Marshal(importedUser)
	require.NoError(t, err, "Failed to marshal imported user")

	// Create Request
	body := new(strings.Builder)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "config.json")
	require.NoError(t, err, "Failed to create form file")

	_, err = part.Write(jsonData)
	require.NoError(t, err, "Failed to write to part")

	err = writer.Close()
	require.NoError(t, err, "Failed to close writer")

	req := httptest.NewRequest(http.MethodPost, "/import_user_config", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Inject User into Context (mimic RequireLogin)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	req = req.WithContext(ctx)

	// Execute
	w := httptest.NewRecorder()
	s.handleImportUserConfig(w, req)

	// Check Response
	assert.Equal(t, http.StatusSeeOther, w.Code, "Expected redirect status")

	// Check DB for App ID
	var app data.App
	err = s.DB.First(&app, "iname = ?", "100").Error
	require.NoError(t, err, "Failed to find imported app")

	assert.NotEqual(t, uint(999), app.ID, "App ID was not reset! Still 999")
	assert.NotEqual(t, uint(0), app.ID, "App ID is 0, expected valid auto-increment ID")

	// Verify Device Link
	assert.Equal(t, "dev1", app.DeviceID, "App linked to wrong device")
}
