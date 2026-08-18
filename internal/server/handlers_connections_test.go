package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"tronbyt-server/internal/connections"
	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// fakeProviderServer is a self-contained mock OAuth2 provider for the
// integration tests in this package. It records hits on /token so we can
// assert the handler called the expected endpoints.
type fakeProviderServer struct {
	server    *httptest.Server
	tokenHits atomic.Int32
	authzHits atomic.Int32

	athleteID string
}

func newFakeProviderServer(t *testing.T, athleteID string) *fakeProviderServer {
	t.Helper()
	fp := &fakeProviderServer{athleteID: athleteID}

	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, _ *http.Request) {
		fp.authzHits.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		fp.tokenHits.Add(1)
		_ = r.ParseForm()
		body := map[string]any{
			"access_token":  "fake-access-token",
			"refresh_token": "fake-refresh-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	fp.server = httptest.NewServer(mux)
	t.Cleanup(fp.server.Close)
	return fp
}

func (fp *fakeProviderServer) provider() *connections.Provider {
	return &connections.Provider{
		Name:          "fake",
		DisplayName:   "Fake",
		AuthorizeURL:  fp.server.URL + "/authorize",
		TokenURL:      fp.server.URL + "/token",
		DefaultScopes: []string{"read"},
		// Identify returns canned values without an HTTP call — production
		// providers like Strava do their own GET.
		Identify: func(_ context.Context, _ string) (string, string, error) {
			return fp.athleteID, "Test Athlete", nil
		},
	}
}

// makeServerWithFakeProvider builds a Server pre-loaded with a fake
// provider, a real DB user, and an active session cookie.
func makeServerWithFakeProvider(t *testing.T) (*Server, *fakeProviderServer, string /* sessionCookie */) {
	t.Helper()
	s := newTestServer(t)
	fp := newFakeProviderServer(t, "12345")

	registry := connections.NewRegistry(fp.provider())
	s.ConnectionsRegistry = registry
	s.Connections = &connections.Service{
		DB:       s.DB,
		Registry: registry,
		Secret:   "testsecret",
		GetCreds: func(provider string) (string, string) {
			if provider == "fake" {
				return "test-client-id", "test-client-secret"
			}
			return "", ""
		},
	}

	require.NoError(t, s.DB.Create(&data.User{Username: "alice", APIKey: "k"}).Error)

	// Forge a session cookie tied to the test secret.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	session, err := s.Store.Get(r, "session-name")
	require.NoError(t, err)
	session.Values["username"] = "alice"
	require.NoError(t, session.Save(r, rec))

	cookie := firstCookie(t, rec.Header().Values("Set-Cookie"))
	return s, fp, cookie
}

// firstCookie pulls just the "name=value" segment from the first Set-Cookie
// header so it can be sent back as a Cookie request header.
func firstCookie(t *testing.T, headers []string) string {
	t.Helper()
	require.NotEmpty(t, headers, "expected at least one Set-Cookie")
	c := headers[0]
	if i := strings.Index(c, ";"); i > 0 {
		c = c[:i]
	}
	return c
}

func TestConnectionStartRedirectsToProvider(t *testing.T) {
	s, fp, cookie := makeServerWithFakeProvider(t)

	req := httptest.NewRequest(http.MethodGet, "/connections/start/fake?return_to=%2Fdevices%2Fabc%2F123%2Fconfig", nil)
	req.Header.Set("Cookie", cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	loc := rr.Header().Get("Location")
	require.NotEmpty(t, loc)

	u, err := url.Parse(loc)
	require.NoError(t, err)
	assert.Equal(t, fp.server.URL+"/authorize", strings.Split(loc, "?")[0])
	assert.NotEmpty(t, u.Query().Get("state"))
	assert.Equal(t, "test-client-id", u.Query().Get("client_id"))
	assert.Contains(t, u.Query().Get("redirect_uri"), "/oauth-callback")
}

func TestConnectionStartUnknownProvider(t *testing.T) {
	s, _, cookie := makeServerWithFakeProvider(t)

	req := httptest.NewRequest(http.MethodGet, "/connections/start/nope", nil)
	req.Header.Set("Cookie", cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestConnectionStartUnconfiguredProvider(t *testing.T) {
	s, _, cookie := makeServerWithFakeProvider(t)
	s.Connections.GetCreds = func(string) (string, string) { return "", "" }

	req := httptest.NewRequest(http.MethodGet, "/connections/start/fake", nil)
	req.Header.Set("Cookie", cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

func TestConnectionCallbackHappyPath(t *testing.T) {
	s, fp, cookie := makeServerWithFakeProvider(t)

	// Step 1: kick off the flow so the session captures pending state.
	startReq := httptest.NewRequest(http.MethodGet, "/connections/start/fake?return_to=%2Fdevices%2Fabc%2F123%2Fconfig&scopes=read,activity:read", nil)
	startReq.Header.Set("Cookie", cookie)
	startRec := httptest.NewRecorder()
	s.ServeHTTP(startRec, startReq)
	require.Equal(t, http.StatusSeeOther, startRec.Code)

	cookie2 := firstCookie(t, startRec.Header().Values("Set-Cookie"))

	loc, err := url.Parse(startRec.Header().Get("Location"))
	require.NoError(t, err)
	state := loc.Query().Get("state")
	require.NotEmpty(t, state)

	// Step 2: simulate the provider redirecting back with code+state.
	cbReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/oauth-callback?code=auth-code-xyz&state=%s", url.QueryEscape(state)), nil)
	cbReq.Header.Set("Cookie", cookie2)
	cbRec := httptest.NewRecorder()
	s.ServeHTTP(cbRec, cbReq)

	require.Equal(t, http.StatusSeeOther, cbRec.Code)
	assert.Equal(t, "/devices/abc/123/config", cbRec.Header().Get("Location"))
	assert.GreaterOrEqual(t, fp.tokenHits.Load(), int32(1))

	// Step 3: a Connection row was created for alice.
	var conn data.Connection
	require.NoError(t, s.DB.Where("user_id = ? AND provider = ?", "alice", "fake").First(&conn).Error)
	assert.Equal(t, "12345", conn.ExternalID)
	assert.NotEmpty(t, conn.AccessToken)
	assert.NotEmpty(t, conn.RefreshToken)
	// Tokens are encrypted: ciphertext must not contain the plaintext.
	assert.NotContains(t, string(conn.AccessToken), "fake-access-token")
}

func TestConnectionCallbackStateMismatch(t *testing.T) {
	s, _, cookie := makeServerWithFakeProvider(t)

	// Start a flow first so the session has a pending state.
	startReq := httptest.NewRequest(http.MethodGet, "/connections/start/fake", nil)
	startReq.Header.Set("Cookie", cookie)
	startRec := httptest.NewRecorder()
	s.ServeHTTP(startRec, startReq)
	cookie2 := firstCookie(t, startRec.Header().Values("Set-Cookie"))

	cbReq := httptest.NewRequest(http.MethodGet, "/oauth-callback?code=x&state=tampered", nil)
	cbReq.Header.Set("Cookie", cookie2)
	cbRec := httptest.NewRecorder()
	s.ServeHTTP(cbRec, cbReq)

	// flashAndRedirect issues a 303 to / — we just want to confirm we did
	// NOT create a Connection row.
	assert.Equal(t, http.StatusSeeOther, cbRec.Code)
	var count int64
	require.NoError(t, s.DB.Model(&data.Connection{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestDisconnectRemovesConnection(t *testing.T) {
	s, _, cookie := makeServerWithFakeProvider(t)

	conn := data.Connection{UserID: "alice", Provider: "fake"}
	require.NoError(t, s.DB.Create(&conn).Error)

	form := url.Values{}
	form.Set("return_to", "/")
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/connections/%d/disconnect", conn.ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusSeeOther, rr.Code)

	err := s.DB.Where("id = ?", conn.ID).First(&data.Connection{}).Error
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}
