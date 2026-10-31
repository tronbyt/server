package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/require"
)

func TestHandleSetThemePreference(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser", ThemePreference: "light"}
	s.DB.Create(&user)

	form := url.Values{}
	form.Add("theme", "dark")

	req, _ := http.NewRequest(http.MethodPost, "/set_theme_preference", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleSetThemePreference)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	var updatedUser data.User
	s.DB.First(&updatedUser, "username = ?", "testuser")
	if updatedUser.ThemePreference != "dark" {
		t.Errorf("Theme preference not updated")
	}
}

func TestHandleRefreshSystemRepoReturnsJSONError(t *testing.T) {
	s := newTestServer(t)
	s.Config.SystemAppsRepo = "file:///path/that/does/not/exist"
	admin := data.User{Username: "admin", IsAdmin: true}

	req := httptest.NewRequest(http.MethodPost, "/refresh_system_repo", nil)
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &admin))
	rr := httptest.NewRecorder()

	s.handleRefreshSystemRepo(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Contains(t, rr.Body.String(), "Failed to refresh system repository")
}

func TestHandleSetSystemAppsAutoRefresh(t *testing.T) {
	s := newTestServer(t)
	admin := data.User{Username: "admin", IsAdmin: true}

	form := url.Values{"system_apps_auto_refresh": {"1"}}
	req := httptest.NewRequest(http.MethodPost, "/settings/system-apps-auto-refresh", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &admin))
	rr := httptest.NewRecorder()

	s.handleSetSystemAppsAutoRefresh(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.JSONEq(t, `{"enabled":true}`, rr.Body.String())
	require.True(t, s.isSystemAppsAutoRefreshEnabled())
	stored, err := s.getSetting(systemAppsAutoRefreshSettingKey)
	require.NoError(t, err)
	require.Equal(t, "true", stored)

	req = httptest.NewRequest(http.MethodPost, "/settings/system-apps-auto-refresh", nil)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &admin))
	rr = httptest.NewRecorder()
	s.handleSetSystemAppsAutoRefresh(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.False(t, s.isSystemAppsAutoRefreshEnabled())
	stored, err = s.getSetting(systemAppsAutoRefreshSettingKey)
	require.NoError(t, err)
	require.Equal(t, "false", stored)
}

func TestSetSystemAppsAutoRefreshSerializesPreferenceTransitions(t *testing.T) {
	s := newTestServer(t)
	s.systemAppsAutoRefreshPreferenceMutex.Lock()
	started := make(chan struct{})
	result := make(chan error, 1)

	go func() {
		close(started)
		result <- s.setSystemAppsAutoRefresh(true)
	}()

	<-started
	select {
	case err := <-result:
		s.systemAppsAutoRefreshPreferenceMutex.Unlock()
		require.NoError(t, err)
		t.Fatal("preference transition completed while its serialization mutex was held")
	case <-time.After(100 * time.Millisecond):
	}

	s.systemAppsAutoRefreshPreferenceMutex.Unlock()
	require.NoError(t, <-result)
	require.True(t, s.isSystemAppsAutoRefreshEnabled())
	stored, err := s.getSetting(systemAppsAutoRefreshSettingKey)
	require.NoError(t, err)
	require.Equal(t, "true", stored)
}

func TestHandleSetSystemAppsAutoRefreshRequiresAdmin(t *testing.T) {
	s := newTestServer(t)
	user := data.User{Username: "user"}

	form := url.Values{"system_apps_auto_refresh": {"1"}}
	req := httptest.NewRequest(http.MethodPost, "/settings/system-apps-auto-refresh", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &user))
	rr := httptest.NewRecorder()

	s.handleSetSystemAppsAutoRefresh(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
	require.False(t, s.isSystemAppsAutoRefreshEnabled())
	stored, err := s.getSetting(systemAppsAutoRefreshSettingKey)
	require.NoError(t, err)
	require.Empty(t, stored)
}

func TestSettingsContentDataIncludesSystemAppsAutoRefresh(t *testing.T) {
	s := newTestServer(t, withSystemAppsAutoRefresh(true))
	admin := data.User{Username: "admin", IsAdmin: true}

	pageData := s.getSettingsContentData(&admin)

	require.True(t, pageData.SystemAppsAutoRefresh)
}

func TestSettingsContentRendersSystemAppsAutoRefreshControl(t *testing.T) {
	s := newTestServer(t, withSystemAppsAutoRefresh(true))
	admin := data.User{Username: "admin", IsAdmin: true, APIKey: "admin-api-key"}
	require.NoError(t, s.DB.Create(&admin).Error)

	seedReq := httptest.NewRequest(http.MethodGet, "/settings/content", nil)
	seedRR := httptest.NewRecorder()
	session, _ := s.Store.Get(seedReq, "session-name")
	session.Values["username"] = admin.Username
	require.NoError(t, s.saveSession(seedRR, seedReq, session))

	req := httptest.NewRequest(http.MethodGet, "/settings/content", nil)
	for _, cookie := range seedRR.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `id="system_apps_auto_refresh"`)
	require.Contains(t, rr.Body.String(), "Automatically update system apps every 12 hours")
	require.Contains(t, rr.Body.String(), "Automatic updates may change or break apps already installed on your devices.")
	require.Contains(t, rr.Body.String(), "checked")
	require.Contains(t, rr.Body.String(), `onchange="saveSystemAppsAutoRefresh(this)"`)
	require.Contains(t, rr.Body.String(), `id="system-apps-auto-refresh-status"`)
	require.Contains(t, rr.Body.String(), `action="/refresh_system_repo"`)
	require.Contains(t, rr.Body.String(), `action="/clear_system_apps_repo"`)
	require.Contains(t, rr.Body.String(), `aria-label="Delete downloaded repo"`)
}

func TestHandleSetSystemRepoRevertsURLWhenRefreshFails(t *testing.T) {
	s := newTestServer(t)
	admin := data.User{Username: "admin", IsAdmin: true}

	previousURL := "https://github.com/tronbyt/apps.git"
	s.Config.SystemAppsRepo = previousURL
	require.NoError(t, s.setSetting("system_apps_repo", previousURL))

	form := url.Values{"app_repo_url": {"file:///path/that/does/not/exist"}}
	req := httptest.NewRequest(http.MethodPost, "/set_system_repo", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &admin))
	rr := httptest.NewRecorder()

	s.handleSetSystemRepo(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	require.Equal(t, previousURL, s.Config.SystemAppsRepo)
	stored, err := s.getSetting("system_apps_repo")
	require.NoError(t, err)
	require.Equal(t, previousURL, stored)
}

func TestHandleClearSystemAppsRepo(t *testing.T) {
	s := newTestServer(t)
	admin := data.User{Username: "admin", IsAdmin: true}

	appDir := filepath.Join(s.DataDir, "system-apps", "apps", "cleartest")
	require.NoError(t, os.MkdirAll(appDir, 0o755))
	s.RefreshSystemAppsCache()
	require.Len(t, s.ListSystemApps(), 1)

	req := httptest.NewRequest(http.MethodPost, "/clear_system_apps_repo", nil)
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &admin))
	rr := httptest.NewRecorder()

	s.handleClearSystemAppsRepo(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.JSONEq(t, `{"success":true}`, rr.Body.String())
	_, err := os.Stat(filepath.Join(s.DataDir, "system-apps"))
	require.True(t, os.IsNotExist(err))
	require.Empty(t, s.ListSystemApps())
}

func TestHandleClearSystemAppsRepoRequiresAdmin(t *testing.T) {
	s := newTestServer(t)
	user := data.User{Username: "user", IsAdmin: false}

	appDir := filepath.Join(s.DataDir, "system-apps", "apps", "cleartest")
	require.NoError(t, os.MkdirAll(appDir, 0o755))

	req := httptest.NewRequest(http.MethodPost, "/clear_system_apps_repo", nil)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &user))
	rr := httptest.NewRecorder()

	s.handleClearSystemAppsRepo(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
	require.DirExists(t, filepath.Join(s.DataDir, "system-apps"))
}

func TestSettingsContentScreenyProvider(t *testing.T) {
	s := newTestServer(t)
	admin := data.User{Username: "admin", IsAdmin: true, APIKey: "admin-api-key"}
	require.NoError(t, s.DB.Create(&admin).Error)

	seedReq := httptest.NewRequest(http.MethodGet, "/settings/content", nil)
	seedRR := httptest.NewRecorder()
	session, _ := s.Store.Get(seedReq, "session-name")
	session.Values["username"] = admin.Username
	require.NoError(t, s.saveSession(seedRR, seedReq, session))

	// 1. Initial GET shows Screeny controls
	req := httptest.NewRequest(http.MethodGet, "/settings/content", nil)
	for _, cookie := range seedRR.Result().Cookies() {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), "Screeny Studio")
	require.Contains(t, rr.Body.String(), `name="screeny_studio_url"`)
	require.Contains(t, rr.Body.String(), `name="screeny_scale_filter"`)

	// 2. POST update screeny config
	form := url.Values{}
	form.Add("screeny_studio_url", "http://localhost:8787")
	form.Add("screeny_scale_filter", "scale2x")
	postReq := httptest.NewRequest(http.MethodPost, "/settings/screeny", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range seedRR.Result().Cookies() {
		postReq.AddCookie(cookie)
	}
	postRR := httptest.NewRecorder()
	s.ServeHTTP(postRR, postReq)

	require.Equal(t, http.StatusSeeOther, postRR.Code)
	require.Equal(t, "http://localhost:8787", s.Config.ScreenyStudioURL)
	require.Equal(t, "scale2x", s.Config.ScreenyScaleFilter)
	require.NotNil(t, s.GetExternalProvider("screeny"))

	// Check DB setting
	val, err := s.getSetting("screeny_studio_url")
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8787", val)
}

func TestHandleEditUserPostUpdatesEmail(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser", Password: "hashed", APIKey: "user-api-key"}
	require.NoError(t, s.DB.Create(&user).Error)

	seedReq := httptest.NewRequest(http.MethodGet, "/settings/account", nil)
	seedRR := httptest.NewRecorder()
	session, _ := s.Store.Get(seedReq, "session-name")
	session.Values["username"] = user.Username
	require.NoError(t, s.saveSession(seedRR, seedReq, session))

	form := url.Values{}
	form.Add("email", "test@example.com")
	req := httptest.NewRequest(http.MethodPost, "/settings/account", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range seedRR.Result().Cookies() {
		req.AddCookie(cookie)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleEditUserPost)
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)

	var updatedUser data.User
	require.NoError(t, s.DB.First(&updatedUser, "username = ?", "testuser").Error)
	require.NotNil(t, updatedUser.Email)
	require.Equal(t, "test@example.com", *updatedUser.Email)
}

func TestHandleAdminUpdateUserEmail(t *testing.T) {
	s := newTestServer(t)

	admin := data.User{Username: "admin", IsAdmin: true, APIKey: "admin-api-key"}
	target := data.User{Username: "testuser", APIKey: "target-api-key"}
	require.NoError(t, s.DB.Create(&admin).Error)
	require.NoError(t, s.DB.Create(&target).Error)

	form := url.Values{}
	form.Add("email", "updated@example.com")
	req := httptest.NewRequest(http.MethodPost, "/settings/admin/users/testuser/email", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("username", "testuser")

	ctx := context.WithValue(req.Context(), userContextKey, &admin)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleAdminUpdateUserEmail)
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)

	var updatedUser data.User
	require.NoError(t, s.DB.First(&updatedUser, "username = ?", "testuser").Error)
	require.NotNil(t, updatedUser.Email)
	require.Equal(t, "updated@example.com", *updatedUser.Email)
}

// TestHandleIndexRendersDeviceCard verifies the expected behavior under test.
func TestHandleIndexRendersDeviceCard(t *testing.T) {
	s := newTestServer(t)

	user := data.User{
		Username: "carduser",
		APIKey:   "user-key-1",
		Devices: []data.Device{
			{
				ID:     "dev-pi",
				Name:   "Raspberry Pi Wide",
				Type:   data.DeviceRaspberryPiWide,
				APIKey: "dev-key-1",
				Muted:  false,
			},
			{
				ID:     "dev-gen1",
				Name:   "Tidbyt Gen 1",
				Type:   data.DeviceTidbytGen1,
				APIKey: "dev-key-2",
				Muted:  true,
			},
		},
	}
	require.NoError(t, s.DB.Create(&user).Error)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleIndex)
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	require.Contains(t, body, "device-card-dev-pi")
	require.Contains(t, body, "device-card-dev-gen1")
}
