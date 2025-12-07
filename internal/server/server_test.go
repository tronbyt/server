package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestServer(t *testing.T) *Server {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}

	if err := db.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}); err != nil {
		t.Fatalf("Failed to migrate DB: %v", err)
	}

	cfg := &config.Settings{
		SecretKey: "testsecret",
	}

	s := NewServer(db, cfg)
	s.DataDir = t.TempDir()
	return s
}

func TestHealthCheck(t *testing.T) {
	s := newTestServer(t)

	req, _ := http.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusOK)
	}

	expected := "OK"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v",
			rr.Body.String(), expected)
	}
}

func TestLoginRedirectToRegisterIfNoUsers(t *testing.T) {
	s := newTestServer(t)

	// Ensure no users
	var count int64
	s.DB.Model(&data.User{}).Count(&count)
	if count != 0 {
		t.Fatalf("Expected 0 users, got %d", count)
	}

	req, _ := http.NewRequest("GET", "/auth/login", nil)
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
