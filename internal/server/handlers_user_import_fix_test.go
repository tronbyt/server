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
)

func TestHandleImportUserConfig_VerifyIDs(t *testing.T) {
	s := newTestServer(t)

	// Create initial user
	user := data.User{
		Username: "testuser",
		APIKey:   "apikey",
	}
	s.DB.Create(&user)

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
	jsonData, _ := json.Marshal(importedUser)

	// Create Request
	body := new(strings.Builder)
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "config.json")
	if _, err := part.Write(jsonData); err != nil {
		t.Fatalf("Failed to write to part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Failed to close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/import_user_config", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Inject User into Context (mimic RequireLogin)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	req = req.WithContext(ctx)

	// Execute
	w := httptest.NewRecorder()
	s.handleImportUserConfig(w, req)

	// Check Response
	if w.Code != http.StatusSeeOther {
		t.Errorf("Expected redirect, got %d. Body: %s", w.Code, w.Body.String())
	}

	// Check DB for App ID
	var app data.App
	if err := s.DB.First(&app, "iname = ?", "100").Error; err != nil {
		t.Fatalf("Failed to find imported app: %v", err)
	}

	if app.ID == 999 {
		t.Errorf("App ID was not reset! Still 999")
	}
	if app.ID == 0 {
		t.Errorf("App ID is 0, expected valid auto-increment ID")
	}

	// Verify Device Link
	if app.DeviceID != "dev1" {
		t.Errorf("App linked to wrong device: %s", app.DeviceID)
	}
}
