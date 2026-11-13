package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newBlockedCustomAppsRequest(t *testing.T, s *Server, username, method, target string, form url.Values) *http.Request {
	t.Helper()

	seedReq := httptest.NewRequest(http.MethodGet, "/", nil)
	seedRR := httptest.NewRecorder()
	session, _ := s.Store.Get(seedReq, "session-name")
	session.Values["username"] = username
	require.NoError(t, s.saveSession(seedRR, seedReq, session))

	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	for _, cookie := range seedRR.Result().Cookies() {
		req.AddCookie(cookie)
	}
	return req
}

func TestBlockCustomAppsRejectsUploadsAndRepos(t *testing.T) {
	s := newTestServer(t)
	s.Config.BlockCustomApps = true

	user := data.User{Username: "testuser", APIKey: "test-api-key"}
	require.NoError(t, s.DB.Create(&user).Error)
	require.NoError(t, s.DB.Create(&data.Device{ID: "testdevice", Username: user.Username}).Error)

	tests := []struct {
		name   string
		method string
		target string
		form   url.Values
	}{
		{"upload page", http.MethodGet, "/devices/testdevice/uploadapp", nil},
		{"upload post", http.MethodPost, "/devices/testdevice/uploadapp", url.Values{}},
		{"set repo", http.MethodPost, "/set_user_repo", url.Values{"app_repo_url": {"https://example.com/apps.git"}}},
		{"refresh repo", http.MethodPost, "/refresh_user_repo", url.Values{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newBlockedCustomAppsRequest(t, s, user.Username, tt.method, tt.target, tt.form)
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, req)
			assert.Equal(t, http.StatusForbidden, rr.Code)
		})
	}

	var updated data.User
	require.NoError(t, s.DB.First(&updated, "username = ?", user.Username).Error)
	assert.Empty(t, updated.AppRepoURL)
}

func TestBlockCustomAppsAllowsRepoRemoval(t *testing.T) {
	s := newTestServer(t)
	s.Config.BlockCustomApps = true

	user := data.User{Username: "testuser", APIKey: "test-api-key", AppRepoURL: "https://example.com/apps.git"}
	require.NoError(t, s.DB.Create(&user).Error)

	req := newBlockedCustomAppsRequest(t, s, user.Username, http.MethodPost, "/set_user_repo", url.Values{"app_repo_url": {""}})
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusSeeOther, rr.Code)
	var updated data.User
	require.NoError(t, s.DB.First(&updated, "username = ?", user.Username).Error)
	assert.Empty(t, updated.AppRepoURL)
}

func TestBlockCustomAppsHidesUI(t *testing.T) {
	s := newTestServer(t)
	s.Config.BlockCustomApps = true

	user := data.User{Username: "testuser", APIKey: "test-api-key"}
	require.NoError(t, s.DB.Create(&user).Error)
	require.NoError(t, s.DB.Create(&data.Device{ID: "testdevice", Username: user.Username}).Error)

	req := newBlockedCustomAppsRequest(t, s, user.Username, http.MethodGet, "/settings/content", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.NotContains(t, rr.Body.String(), `id="user_repo_url"`)
	assert.Contains(t, rr.Body.String(), "Custom apps are disabled on this server.")

	req = newBlockedCustomAppsRequest(t, s, user.Username, http.MethodGet, "/devices/testdevice/addapp", nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.NotContains(t, rr.Body.String(), "/devices/testdevice/uploadapp")
}
