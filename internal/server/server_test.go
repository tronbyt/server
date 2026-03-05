package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type option func(s *config.Settings)

func withPprof(value bool) option {
	return func(s *config.Settings) {
		s.EnablePprof = value
	}
}

func newTestServer(t *testing.T, opts ...option) *Server {
	dbName := fmt.Sprintf("file:%s?mode=memory&cache=private&_busy_timeout=5000", t.Name())
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

	cfg := &config.Settings{
		DataDir:            t.TempDir(),
		Production:         false,
		EnableUpdateChecks: false,
	}

	for _, opt := range opts {
		opt(cfg)
	}

	s := NewServer(db, cfg)
	return s
}

func TestLoginRedirectToRegisterIfNoUsers(t *testing.T) {
	s := newTestServer(t)

	// Ensure no users
	count, _ := gorm.G[data.User](s.DB).Count(context.Background(), "*")
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

func TestPprofRoutesDisabledByDefault(t *testing.T) {
	s := newTestServer(t)

	req, err := http.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	require.NoError(t, err)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusSeeOther, rr.Code)

	location, err := rr.Result().Location()
	require.NoError(t, err)
	assert.Equal(t, "/auth/login", location.Path)
}

func TestPprofRoutesEnabled(t *testing.T) {
	s := newTestServer(t, withPprof(true))

	req, err := http.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	require.NoError(t, err)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}
