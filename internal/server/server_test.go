package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestServer(t *testing.T) *Server {
	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}

	if err := db.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.WebAuthnCredential{}, &data.Setting{}); err != nil {
		t.Fatalf("Failed to migrate DB: %v", err)
	}

	// Pre-seed settings to avoid "record not found" logs during NewServer
	db.Create(&data.Setting{Key: "secret_key", Value: "testsecret"})
	db.Create(&data.Setting{Key: "system_apps_repo", Value: ""})

	cfg := &config.Settings{}

	s := NewServer(db, cfg)
	s.DataDir = t.TempDir()
	return s
}

func TestLoginRedirectToRegisterIfNoUsers(t *testing.T) {
	s := newTestServer(t)

	// Ensure no users
	var count int64
	s.DB.Model(&data.User{}).Count(&count)
	if count != 0 {
		t.Fatalf("Expected 0 users, got %d", count)
	}

	req, _ := http.NewRequest(http.MethodGet, "/auth/login", nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusSeeOther)
	}

	location, _ := rr.Result().Location()
	if location.Path != "/auth/register" {
		t.Errorf("handler returned wrong redirect location: got %v want %v",
			location.Path, "/auth/register")
	}
}
