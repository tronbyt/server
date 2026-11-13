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

func withSystemAppsAutoRefresh(value bool) option {
	return func(s *config.Settings) {
		s.SystemAppsAutoRefresh = value
	}
}

func newTestServer(t *testing.T, opts ...option) *Server {
	dbName := fmt.Sprintf("file:%s?mode=memory&cache=private&_busy_timeout=5000", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}

	if err := db.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.WebAuthnCredential{}, &data.OIDCIdentity{}, &data.Setting{}); err != nil {
		t.Fatalf("Failed to migrate DB: %v", err)
	}

	// Pre-seed settings to avoid "record not found" logs during NewServer
	db.Create(&data.Setting{Key: "secret_key", Value: "testsecret"})
	db.Create(&data.Setting{Key: secretKeyVersionSettingKey, Value: secretKeyVersion})
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
	// Let the startup thumbnail preheat finish before tests register mock
	// providers; otherwise it can pick one up and wipe the test's renders.
	s.backgroundRenders.Wait()
	t.Cleanup(func() {
		s.backgroundRenders.Wait()
	})
	return s
}

func TestNewServerRotatesSecretKeyOnUpgrade(t *testing.T) {
	s := newTestServer(t)
	key, err := s.getSetting("secret_key")
	require.NoError(t, err)
	require.Equal(t, "testsecret", key, "a current key must not be rotated")

	// Installs from before key versioning have no version setting.
	require.NoError(t, s.DB.Where("key = ?", secretKeyVersionSettingKey).Delete(&data.Setting{}).Error)

	cfg := &config.Settings{DataDir: t.TempDir()}
	upgraded := NewServer(s.DB, cfg)
	t.Cleanup(upgraded.backgroundRenders.Wait)

	rotated, err := upgraded.getSetting("secret_key")
	require.NoError(t, err)
	assert.NotEqual(t, "testsecret", rotated)
	assert.NotEmpty(t, rotated)
	version, err := upgraded.getSetting(secretKeyVersionSettingKey)
	require.NoError(t, err)
	assert.Equal(t, secretKeyVersion, version)

	restarted := NewServer(s.DB, cfg)
	t.Cleanup(restarted.backgroundRenders.Wait)
	again, err := restarted.getSetting("secret_key")
	require.NoError(t, err)
	assert.Equal(t, rotated, again, "the key must only rotate once")
}

func TestSystemAppsAutoRefreshUsesEnvironmentDefault(t *testing.T) {
	s := newTestServer(t, withSystemAppsAutoRefresh(true))

	require.True(t, s.isSystemAppsAutoRefreshEnabled())
}

func TestSavedSystemAppsAutoRefreshOverridesEnvironmentDefault(t *testing.T) {
	s := newTestServer(t)
	require.NoError(t, s.setSetting(systemAppsAutoRefreshSettingKey, "false"))

	cfg := &config.Settings{
		DataDir:               t.TempDir(),
		Production:            false,
		EnableUpdateChecks:    false,
		SystemAppsAutoRefresh: true,
	}
	restarted := NewServer(s.DB, cfg)

	require.False(t, restarted.isSystemAppsAutoRefreshEnabled())
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
