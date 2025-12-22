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
	"tronbyt-server/internal/legacy"

	"github.com/gorilla/sessions"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/stretchr/testify/assert"
	"golang.org/x/text/language"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestHandleImportUserConfig_Legacy(t *testing.T) {
	// Setup DB
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=private"), &gorm.Config{})
	assert.NoError(t, err)
	err = db.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.WebAuthnCredential{})
	assert.NoError(t, err)

	// Create existing user
	user := data.User{
		Username: "testuser",
		Email:    stringPtr("old@example.com"),
	}
	db.Create(&user)

	// Create Server
	store := sessions.NewCookieStore([]byte("secret-key"))
	s := &Server{
		DB:     db,
		Store:  store,
		Bundle: i18n.NewBundle(language.English),
	}

	// Create Legacy JSON payload
	legacyUser := legacy.LegacyUser{
		Username: "testuser",
		Email:    "new@example.com",
		Devices: map[string]legacy.LegacyDevice{
			"dev1": {
				ID:   "dev1",
				Name: "Legacy Device",
				Apps: map[string]json.RawMessage{
					"app1": json.RawMessage(`{"iname": "app1", "name": "Clock", "enabled": 1}`),
				},
			},
		},
	}
	jsonData, err := json.Marshal(legacyUser)
	assert.NoError(t, err)

	// Create Multipart Request
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "config.json")
	assert.NoError(t, err)
	_, err = part.Write(jsonData)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/import_user_config", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Manually inject user into context
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	// Execute
	s.handleImportUserConfig(w, req)

	// Assert
	assert.Equal(t, http.StatusSeeOther, w.Code)

	// Verify DB updates
	var updatedUser data.User
	result := db.Preload("Devices").Preload("Devices.Apps").First(&updatedUser, "username = ?", "testuser")
	assert.NoError(t, result.Error)

	assert.NotNil(t, updatedUser.Email)
	assert.Equal(t, "new@example.com", *updatedUser.Email)
	assert.Len(t, updatedUser.Devices, 1)
	assert.Equal(t, "Legacy Device", updatedUser.Devices[0].Name)
	assert.Len(t, updatedUser.Devices[0].Apps, 1)
	assert.Equal(t, "Clock", updatedUser.Devices[0].Apps[0].Name)
	assert.True(t, updatedUser.Devices[0].Apps[0].Enabled)
}

func TestHandleImportUserConfig_AppIDReset(t *testing.T) {
	// Setup DB
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=private"), &gorm.Config{})
	assert.NoError(t, err)
	err = db.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.WebAuthnCredential{})
	assert.NoError(t, err)

	// Create existing user
	user := data.User{
		Username: "testuser",
		Email:    stringPtr("test@example.com"),
	}
	db.Create(&user)

	// Create Server
	store := sessions.NewCookieStore([]byte("secret-key"))
	s := &Server{
		DB:     db,
		Store:  store,
		Bundle: i18n.NewBundle(language.English),
	}

	// Create JSON payload with explicit App ID
	// We use a high ID (e.g., 9999) to verify it gets reset
	importedUser := data.User{
		Username: "testuser",
		Email:    stringPtr("imported@example.com"),
		Devices: []data.Device{
			{
				ID:       "dev1",
				Username: "testuser",
				Name:     "Imported Device",
				Apps: []data.App{
					{
						ID:    9999, // Explicit ID that should be ignored/reset
						Iname: "app1",
						Name:  "Test App",
					},
				},
			},
		},
	}
	jsonData, err := json.Marshal(importedUser)
	assert.NoError(t, err)

	// Create Multipart Request
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "config.json")
	assert.NoError(t, err)
	_, err = part.Write(jsonData)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/import_user_config", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Manually inject user into context
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	// Execute
	s.handleImportUserConfig(w, req)

	// Assert
	assert.Equal(t, http.StatusSeeOther, w.Code)

	// Verify DB updates
	var updatedUser data.User
	result := db.Preload("Devices").Preload("Devices.Apps").First(&updatedUser, "username = ?", "testuser")
	assert.NoError(t, result.Error)

	assert.Len(t, updatedUser.Devices, 1)
	dev := updatedUser.Devices[0]
	assert.Len(t, dev.Apps, 1)
	app := dev.Apps[0]

	assert.Equal(t, "Test App", app.Name)
	// The ID should be reset to 1 in this fresh in-memory DB
	assert.Equal(t, uint(1), app.ID, "App ID should be reset to 1")
}
