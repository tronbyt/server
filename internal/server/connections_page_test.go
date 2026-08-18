package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsSafeReturnTo pins the open-redirect guard. Backslashes matter:
// url.Parse treats "\" as an ordinary path byte, but browsers normalize
// "/\evil.com" into the protocol-relative "//evil.com".
func TestIsSafeReturnTo(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"/", true},
		{"/devices/abc/123/config", true},
		{"/connections?tab=1", true},
		{"", false},
		{"https://evil.com", false},
		{"//evil.com", false},
		{"http://evil.com/path", false},
		{"evil.com", false},
		{`/\evil.com`, false},
		{`/\/evil.com`, false},
		{`\\evil.com`, false},
		{`/path\..\parent`, false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, isSafeReturnTo(tc.in))
		})
	}
}

func TestConnectionsPageShowsConfiguredProviders(t *testing.T) {
	s, _, cookie := makeServerWithFakeProvider(t)

	req := httptest.NewRequest(http.MethodGet, "/connections", nil)
	req.Header.Set("Cookie", cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, "Fake", "configured provider should be listed")
	assert.Contains(t, body, "/connections/start/fake", "should offer a Connect link")
	assert.NotContains(t, body, "Connected as")
}

func TestConnectionsPageShowsConnectedState(t *testing.T) {
	s, _, cookie := makeServerWithFakeProvider(t)

	conn := data.Connection{
		UserID:      "alice",
		Provider:    "fake",
		ExternalID:  "12345",
		DisplayName: "Test Athlete",
		Scopes:      "read",
	}
	require.NoError(t, s.DB.Create(&conn).Error)

	req := httptest.NewRequest(http.MethodGet, "/connections", nil)
	req.Header.Set("Cookie", cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, "Connected as")
	assert.Contains(t, body, "Test Athlete")
	assert.Contains(t, body, "/connections/1/disconnect")
}

// TestConnectionsPageHidesUnconfiguredProviders keeps providers the admin
// hasn't supplied credentials for off the page (and out of the nav).
func TestConnectionsPageHidesUnconfiguredProviders(t *testing.T) {
	s, _, cookie := makeServerWithFakeProvider(t)
	s.Connections.GetCreds = func(string) (string, string) { return "", "" }

	assert.False(t, s.anyConnectionProviderConfigured())

	req := httptest.NewRequest(http.MethodGet, "/connections", nil)
	req.Header.Set("Cookie", cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.NotContains(t, rr.Body.String(), "/connections/start/fake")
}
