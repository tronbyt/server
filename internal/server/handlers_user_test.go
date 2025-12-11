package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"tronbyt-server/internal/data"
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
