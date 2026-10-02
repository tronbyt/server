package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tronbyt-server/internal/connections"
	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// fakeDeviceProvider implements the RFC 8628 endpoints: it hands out a
// user code, reports authorization_pending for a configurable number of
// polls, then issues a token — the same shape GitHub presents.
type fakeDeviceProvider struct {
	server *httptest.Server

	pendingPolls int32 // polls answered with authorization_pending
	polls        atomic.Int32
	deviceHits   atomic.Int32

	// sentSecret records whether a client_secret reached the token
	// endpoint, so tests can assert public-client behavior.
	sentSecret atomic.Bool
	// terminalError, when set, is returned instead of a token.
	terminalError string
}

func newFakeDeviceProvider(t *testing.T, pendingPolls int32) *fakeDeviceProvider {
	t.Helper()
	fp := &fakeDeviceProvider{pendingPolls: pendingPolls}

	mux := http.NewServeMux()
	mux.HandleFunc("/device/code", func(w http.ResponseWriter, r *http.Request) {
		fp.deviceHits.Add(1)
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "device-code-xyz",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://example.test/login/device",
			"expires_in":       900,
			"interval":         1,
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		n := fp.polls.Add(1)
		_ = r.ParseForm()
		if r.PostForm.Get("client_secret") != "" {
			fp.sentSecret.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")

		if fp.terminalError != "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": fp.terminalError})
			return
		}
		if n <= fp.pendingPolls {
			// GitHub answers 200 with an RFC error body while pending.
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "device-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	fp.server = httptest.NewServer(mux)
	t.Cleanup(fp.server.Close)
	return fp
}

func (fp *fakeDeviceProvider) provider() *connections.Provider {
	return &connections.Provider{
		Name:                  "fakedevice",
		DisplayName:           "Fake Device",
		TokenURL:              fp.server.URL + "/token",
		DeviceAuthURL:         fp.server.URL + "/device/code",
		DeviceAuthNeedsSecret: false,
		AuthStyle:             oauth2.AuthStyleInParams,
		DefaultScopes:         []string{"read"},
		Identify: func(context.Context, string) (string, string, error) {
			return "99", "Octo Cat", nil
		},
	}
}

// makeServerWithDeviceProvider wires a server whose only provider is a
// device-flow one configured with a client id and NO secret.
func makeServerWithDeviceProvider(t *testing.T, fp *fakeDeviceProvider) (*Server, string) {
	t.Helper()
	s := newTestServer(t)

	registry := connections.NewRegistry(fp.provider())
	s.ConnectionsRegistry = registry
	s.Connections = &connections.Service{
		DB:       s.DB,
		Registry: registry,
		Secret:   "testsecret",
		GetCreds: func(provider string) (string, string) {
			if provider == "fakedevice" {
				return "public-client-id", "" // no secret: public client
			}
			return "", ""
		},
	}

	require.NoError(t, s.DB.Create(&data.User{Username: "alice", APIKey: "k"}).Error)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	session, err := s.Store.Get(r, "session-name")
	require.NoError(t, err)
	session.Values["username"] = "alice"
	require.NoError(t, session.Save(r, rec))

	return s, firstCookie(t, rec.Header().Values("Set-Cookie"))
}

// TestDeviceFlowEndToEnd walks the whole grant: start the flow, see the
// code on the page, let the user "approve", and confirm the connection
// is persisted and reported by the status endpoint.
func TestDeviceFlowEndToEnd(t *testing.T) {
	fp := newFakeDeviceProvider(t, 1) // pending once, then approved
	s, cookie := makeServerWithDeviceProvider(t, fp)

	req := httptest.NewRequest(http.MethodGet, "/connections/device/fakedevice?return_to=%2Fconnections", nil)
	req.Header.Set("Cookie", cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, "WDJB-MJHT", "the page must show the user code")
	assert.Contains(t, body, "example.test/login/device", "and where to enter it")
	assert.Equal(t, int32(1), fp.deviceHits.Load())

	flowID := onlyDeviceFlowID(t, s)

	// The poller runs in the background; wait for it to land.
	status := waitForDeviceFlow(t, s, flowID, deviceFlowConnected)
	assert.Equal(t, deviceFlowConnected, status)
	assert.False(t, fp.sentSecret.Load(), "a public client must not send a client_secret")

	var conn data.Connection
	require.NoError(t, s.DB.Where("user_id = ? AND provider = ?", "alice", "fakedevice").First(&conn).Error)
	assert.Equal(t, "99", conn.ExternalID)
	assert.Equal(t, "Octo Cat", conn.DisplayName)
	assert.NotContains(t, string(conn.AccessToken), "device-access-token", "token must be encrypted at rest")

	// And the status endpoint reports it to the waiting page.
	statusReq := httptest.NewRequest(http.MethodGet, "/connections/device/status/"+flowID, nil)
	statusReq.Header.Set("Cookie", cookie)
	statusRec := httptest.NewRecorder()
	s.ServeHTTP(statusRec, statusReq)

	require.Equal(t, http.StatusOK, statusRec.Code)
	var payload struct {
		Status string `json:"status"`
		Label  string `json:"label"`
	}
	require.NoError(t, json.NewDecoder(statusRec.Body).Decode(&payload))
	assert.Equal(t, "connected", payload.Status)
	assert.Equal(t, "Octo Cat", payload.Label)
}

func TestDeviceFlowDenied(t *testing.T) {
	fp := newFakeDeviceProvider(t, 0)
	fp.terminalError = "access_denied"
	s, cookie := makeServerWithDeviceProvider(t, fp)

	req := httptest.NewRequest(http.MethodGet, "/connections/device/fakedevice", nil)
	req.Header.Set("Cookie", cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	flowID := onlyDeviceFlowID(t, s)
	assert.Equal(t, deviceFlowDenied, waitForDeviceFlow(t, s, flowID, deviceFlowDenied))

	var count int64
	require.NoError(t, s.DB.Model(&data.Connection{}).Count(&count).Error)
	assert.Equal(t, int64(0), count, "a denied flow must not create a connection")
}

// TestDeviceFlowStatusIsOwnerOnly stops one user reading another's flow.
func TestDeviceFlowStatusIsOwnerOnly(t *testing.T) {
	fp := newFakeDeviceProvider(t, 100) // stays pending
	s, cookie := makeServerWithDeviceProvider(t, fp)

	req := httptest.NewRequest(http.MethodGet, "/connections/device/fakedevice", nil)
	req.Header.Set("Cookie", cookie)
	s.ServeHTTP(httptest.NewRecorder(), req)
	flowID := onlyDeviceFlowID(t, s)

	// A second user with a valid session must not see it.
	require.NoError(t, s.DB.Create(&data.User{Username: "mallory", APIKey: "k2"}).Error)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	session, err := s.Store.Get(r, "session-name")
	require.NoError(t, err)
	session.Values["username"] = "mallory"
	require.NoError(t, session.Save(r, rec))
	malloryCookie := firstCookie(t, rec.Header().Values("Set-Cookie"))

	statusReq := httptest.NewRequest(http.MethodGet, "/connections/device/status/"+flowID, nil)
	statusReq.Header.Set("Cookie", malloryCookie)
	statusRec := httptest.NewRecorder()
	s.ServeHTTP(statusRec, statusReq)

	assert.Equal(t, http.StatusNotFound, statusRec.Code)
}

func TestDeviceFlowUnconfiguredProvider(t *testing.T) {
	fp := newFakeDeviceProvider(t, 0)
	s, cookie := makeServerWithDeviceProvider(t, fp)
	s.Connections.GetCreds = func(string) (string, string) { return "", "" }

	req := httptest.NewRequest(http.MethodGet, "/connections/device/fakedevice", nil)
	req.Header.Set("Cookie", cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

// TestDeviceFlowAvailableWithoutSecret is the point of the public-client
// path: an admin enables GitHub with a client id alone.
func TestDeviceFlowAvailableWithoutSecret(t *testing.T) {
	fp := newFakeDeviceProvider(t, 0)
	s, _ := makeServerWithDeviceProvider(t, fp)

	provider, ok := s.ConnectionsRegistry.Get("fakedevice")
	require.True(t, ok)

	assert.True(t, s.Connections.DeviceFlowAvailable(provider))
	assert.False(t, s.Connections.CodeFlowAvailable(provider),
		"no secret and no authorize URL means no redirect flow")
	assert.True(t, s.anyConnectionProviderConfigured())
}

// TestAnnotateSchemaMarksDeviceFlowProvider covers an app config page
// whose schema declares a device-flow provider: it must read as
// configured on a server that set only a client id, and the UI must be
// told to offer the code flow rather than a redirect.
func TestAnnotateSchemaMarksDeviceFlowProvider(t *testing.T) {
	s := newTestServer(t)
	s.ConnectionsRegistry = connections.NewRegistry(connections.GitHub())
	s.Connections = &connections.Service{
		DB:       s.DB,
		Registry: s.ConnectionsRegistry,
		Secret:   "testsecret",
		GetCreds: func(provider string) (string, string) {
			if provider == "github" {
				return "public-client-id", "" // client id only, no secret
			}
			return "", ""
		},
	}
	require.NoError(t, s.DB.Create(&data.User{Username: "alice", APIKey: "k"}).Error)

	in := []byte(`{"schema":[
		{"type":"oauth2","id":"gh","authorization_endpoint":"https://github.com/login/oauth/authorize"}
	]}`)
	out := s.annotateSchemaForUI(context.Background(), in, "alice")

	var doc struct {
		Schema []map[string]any `json:"schema"`
	}
	require.NoError(t, json.Unmarshal(out, &doc))
	require.Len(t, doc.Schema, 1)

	assert.Equal(t, true, doc.Schema[0]["tronbyt_configured"],
		"a device-flow provider needs no secret to be usable")
	assert.Equal(t, true, doc.Schema[0]["tronbyt_device_flow"])
	assert.Equal(t, "github", doc.Schema[0]["tronbyt_provider"])
}

func TestClassifyDeviceFlowError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want deviceFlowStatus
	}{
		{"denied", &oauth2.RetrieveError{ErrorCode: "access_denied"}, deviceFlowDenied},
		{"microsoft denied", &oauth2.RetrieveError{ErrorCode: "authorization_declined"}, deviceFlowDenied},
		{"expired", &oauth2.RetrieveError{ErrorCode: "expired_token"}, deviceFlowExpired},
		{"deadline", fmt.Errorf("polling: %w", context.DeadlineExceeded), deviceFlowExpired},
		{"device flow off", &oauth2.RetrieveError{ErrorCode: "device_flow_disabled"}, deviceFlowFailed},
		{"unknown", fmt.Errorf("boom"), deviceFlowFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, msg := classifyDeviceFlowError(tc.err)
			assert.Equal(t, tc.want, got)
			assert.NotEmpty(t, msg)
		})
	}
}

// --- helpers ---

func onlyDeviceFlowID(t *testing.T, s *Server) string {
	t.Helper()
	s.deviceFlowsMu.RLock()
	defer s.deviceFlowsMu.RUnlock()
	require.Len(t, s.deviceFlows, 1)
	for id := range s.deviceFlows {
		return id
	}
	return ""
}

// waitForDeviceFlow polls the in-memory flow until it leaves the pending
// state or the test gives up.
func waitForDeviceFlow(t *testing.T, s *Server, id string, want deviceFlowStatus) deviceFlowStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		flow, ok := s.getDeviceFlow(id)
		require.True(t, ok)
		if status, _, _ := flow.snapshot(); status != deviceFlowPending {
			return status
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("device flow %s never reached %s", id, want)
	return ""
}

// TestDeviceCodePushedToDisplay asserts the code actually reaches the
// device as an ephemeral frame — the part that makes this feel magic.
func TestDeviceCodePushedToDisplay(t *testing.T) {
	fp := newFakeDeviceProvider(t, 100) // stays pending so the frame persists
	s, cookie := makeServerWithDeviceProvider(t, fp)

	device := data.Device{ID: "dev1", Username: "alice", Name: "Shelf"}
	require.NoError(t, s.DB.Create(&device).Error)

	req := httptest.NewRequest(http.MethodGet, "/connections/device/fakedevice", nil)
	req.Header.Set("Cookie", cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "also showing on your display")

	path, err := s.deviceCodeImagePath("dev1")
	require.NoError(t, err)
	assert.FileExists(t, path, "the user code should be queued as an ephemeral frame")
	assert.True(t, strings.HasPrefix(filepathBase(path), "__"),
		"ephemeral frames are consumed once and deleted by the rotation")
}

func filepathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
