package connections

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// fakeProvider stands up an httptest server that mimics a token endpoint
// (and optionally an identify endpoint), then returns a Provider pointing
// at it.
type fakeProvider struct {
	server *httptest.Server

	// Tunable per-test:
	// nextAccess returns the access token to issue on the next exchange/refresh.
	nextAccess func() string
	// nextRefresh returns the refresh token to echo back. If empty string,
	// the response omits refresh_token (some real providers do this).
	nextRefresh func() string
	// expiresIn is the seconds-from-now used in token responses.
	expiresIn int

	// Hits records each call type, useful for asserting refresh happened.
	tokenHits    atomic.Int32
	identifyHits atomic.Int32
}

func newFakeProvider(t *testing.T, externalID string) (*fakeProvider, *Provider) {
	t.Helper()
	fp := &fakeProvider{
		expiresIn:   3600,
		nextAccess:  func() string { return "access-AAA" },
		nextRefresh: func() string { return "refresh-AAA" },
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		fp.tokenHits.Add(1)

		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Sanity-check standard params for both grant types.
		grant := r.PostForm.Get("grant_type")
		switch grant {
		case "authorization_code":
			if r.PostForm.Get("code") == "" {
				http.Error(w, "missing code", http.StatusBadRequest)
				return
			}
		case "refresh_token":
			if r.PostForm.Get("refresh_token") == "" {
				http.Error(w, "missing refresh_token", http.StatusBadRequest)
				return
			}
		default:
			http.Error(w, "unsupported grant", http.StatusBadRequest)
			return
		}

		body := map[string]any{
			"access_token": fp.nextAccess(),
			"token_type":   "Bearer",
			"expires_in":   fp.expiresIn,
		}
		if rt := fp.nextRefresh(); rt != "" {
			body["refresh_token"] = rt
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("/identify", func(w http.ResponseWriter, r *http.Request) {
		fp.identifyHits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": externalID})
	})

	fp.server = httptest.NewServer(mux)
	t.Cleanup(fp.server.Close)

	prov := &Provider{
		Name:          "fake",
		DisplayName:   "Fake",
		AuthorizeURL:  fp.server.URL + "/authorize",
		TokenURL:      fp.server.URL + "/token",
		DefaultScopes: []string{"read"},
		Identify: func(ctx context.Context, accessToken string) (string, string, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fp.server.URL+"/identify", nil)
			if err != nil {
				return "", "", err
			}
			req.Header.Set("Authorization", "Bearer "+accessToken)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return "", "", err
			}
			defer func() { _ = resp.Body.Close() }()
			var body struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				return "", "", err
			}
			return body.ID, "Fake User #" + body.ID, nil
		},
	}
	return fp, prov
}

func newTestService(t *testing.T, prov *Provider, secret string) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=private"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&data.User{}, &data.Connection{}))

	require.NoError(t, db.Create(&data.User{Username: "alice", APIKey: "k"}).Error)

	svc := &Service{
		DB:       db,
		Registry: NewRegistry(prov),
		Secret:   secret,
		GetCreds: func(provider string) (string, string) {
			if provider == prov.Name {
				return "client-id-stub", "client-secret-stub"
			}
			return "", ""
		},
	}
	return svc, db
}

func TestExchangeCodeCreatesEncryptedConnection(t *testing.T) {
	fp, prov := newFakeProvider(t, "42")
	svc, db := newTestService(t, prov, "test-secret")

	conn, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "auth-code", []string{"read"})
	require.NoError(t, err)
	require.NotNil(t, conn)
	assert.Equal(t, "alice", conn.UserID)
	assert.Equal(t, prov.Name, conn.Provider)
	assert.Equal(t, "42", conn.ExternalID)
	assert.Equal(t, "Fake User #42", conn.DisplayName)
	assert.Equal(t, int32(1), fp.tokenHits.Load())
	assert.Equal(t, int32(1), fp.identifyHits.Load())

	// Tokens must be encrypted in the DB row, not stored in cleartext.
	var row data.Connection
	require.NoError(t, db.First(&row, conn.ID).Error)
	assert.NotContains(t, string(row.AccessToken), "access-AAA")
	assert.NotContains(t, string(row.RefreshToken), "refresh-AAA")

	// And we can decrypt them back.
	access, err := Open(svc.Secret, row.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "access-AAA", string(access))
	refresh, err := Open(svc.Secret, row.RefreshToken)
	require.NoError(t, err)
	assert.Equal(t, "refresh-AAA", string(refresh))
}

func TestExchangeCodeUpsertsExisting(t *testing.T) {
	fp, prov := newFakeProvider(t, "42")
	svc, db := newTestService(t, prov, "test-secret")

	first, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code-1", nil)
	require.NoError(t, err)

	// Reconnecting (same user, same provider) updates the row in place.
	fp.nextAccess = func() string { return "access-BBB" }
	fp.nextRefresh = func() string { return "refresh-BBB" }
	second, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code-2", nil)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID, "should not create a duplicate row for the same (user, provider)")

	var rows []data.Connection
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
}

func TestAccessTokenForUserUsesFreshTokenWithoutRefresh(t *testing.T) {
	fp, prov := newFakeProvider(t, "42")
	svc, _ := newTestService(t, prov, "test-secret")

	_, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code", nil)
	require.NoError(t, err)
	require.Equal(t, int32(1), fp.tokenHits.Load())

	tok, err := svc.AccessTokenForUser(context.Background(), prov.Name, "alice")
	require.NoError(t, err)
	assert.Equal(t, "access-AAA", tok)

	// No additional token call; the fresh token was reused.
	assert.Equal(t, int32(1), fp.tokenHits.Load())
}

func TestAccessTokenForUserRefreshesExpired(t *testing.T) {
	fp, prov := newFakeProvider(t, "42")
	svc, db := newTestService(t, prov, "test-secret")

	_, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code", nil)
	require.NoError(t, err)

	// Force the stored access token to look already-expired.
	require.NoError(t, db.Model(&data.Connection{}).
		Where("user_id = ? AND provider = ?", "alice", prov.Name).
		Update("access_expires_at", time.Now().Add(-time.Hour)).Error)

	// Next call should trigger a refresh and produce the new token.
	fp.nextAccess = func() string { return "access-REFRESHED" }
	fp.nextRefresh = func() string { return "refresh-ROTATED" }
	tok, err := svc.AccessTokenForUser(context.Background(), prov.Name, "alice")
	require.NoError(t, err)
	assert.Equal(t, "access-REFRESHED", tok)
	assert.GreaterOrEqual(t, fp.tokenHits.Load(), int32(2))

	// The rotated refresh token must be persisted (encrypted).
	var row data.Connection
	require.NoError(t, db.Where("user_id = ? AND provider = ?", "alice", prov.Name).First(&row).Error)
	refreshed, err := Open(svc.Secret, row.RefreshToken)
	require.NoError(t, err)
	assert.Equal(t, "refresh-ROTATED", string(refreshed))
}

func TestAccessTokenForUserNotConnected(t *testing.T) {
	_, prov := newFakeProvider(t, "42")
	svc, _ := newTestService(t, prov, "test-secret")

	_, err := svc.AccessTokenForUser(context.Background(), prov.Name, "alice")
	assert.ErrorIs(t, err, ErrConnectionNotFound)
}

func TestProviderDisabledWhenNoCreds(t *testing.T) {
	_, prov := newFakeProvider(t, "42")
	svc, _ := newTestService(t, prov, "test-secret")
	svc.GetCreds = func(string) (string, string) { return "", "" }

	_, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code", nil)
	assert.ErrorIs(t, err, ErrProviderDisabled)
}

func TestDisconnectRemovesRow(t *testing.T) {
	_, prov := newFakeProvider(t, "42")
	svc, db := newTestService(t, prov, "test-secret")

	conn, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code", nil)
	require.NoError(t, err)

	require.NoError(t, svc.Disconnect(context.Background(), conn.ID, "alice"))

	var count int64
	require.NoError(t, db.Model(&data.Connection{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)

	// Disconnecting again is a not-found, not a server error.
	err = svc.Disconnect(context.Background(), conn.ID, "alice")
	assert.ErrorIs(t, err, ErrConnectionNotFound)
}

func TestDisconnectIgnoresOtherUsers(t *testing.T) {
	_, prov := newFakeProvider(t, "42")
	svc, db := newTestService(t, prov, "test-secret")
	require.NoError(t, db.Create(&data.User{Username: "mallory", APIKey: "m"}).Error)

	conn, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code", nil)
	require.NoError(t, err)

	err = svc.Disconnect(context.Background(), conn.ID, "mallory")
	assert.ErrorIs(t, err, ErrConnectionNotFound, "users must not be able to delete each other's connections")

	var count int64
	require.NoError(t, db.Model(&data.Connection{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

// Sanity check that the URL helpers still parse cleanly when the provider
// uses an Endpoint with absolute URLs (oauth2.Config requires scheme).
func TestProviderOAuth2Config(t *testing.T) {
	_, prov := newFakeProvider(t, "1")
	cfg := prov.OAuth2Config("cid", "csec", "https://x.example/oauth-callback", []string{"foo"})
	require.NotNil(t, cfg)
	assert.Equal(t, "cid", cfg.ClientID)
	assert.Equal(t, []string{"foo"}, cfg.Scopes)

	authURL, err := url.Parse(cfg.AuthCodeURL("state"))
	require.NoError(t, err)
	assert.Contains(t, authURL.Path, "/authorize")
}
