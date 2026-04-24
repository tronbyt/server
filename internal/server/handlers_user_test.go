package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
