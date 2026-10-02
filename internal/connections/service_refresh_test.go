package connections

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAccessTokenForUserNonExpiringToken covers providers that omit
// expires_in: the token never expires, so it must be served from storage
// rather than triggering a refresh on every call (which would fail
// permanently when such a provider also issues no refresh token).
func TestAccessTokenForUserNonExpiringToken(t *testing.T) {
	fp, prov := newFakeProvider(t, "42")
	fp.expiresIn = 0                             // provider omits expires_in
	fp.nextRefresh = func() string { return "" } // ...and issues no refresh token
	svc, _ := newTestService(t, prov, "test-secret")

	_, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code", nil)
	require.NoError(t, err)
	require.Equal(t, int32(1), fp.tokenHits.Load())

	for range 3 {
		tok, err := svc.AccessTokenForUser(context.Background(), prov.Name, "alice")
		require.NoError(t, err)
		assert.Equal(t, "access-AAA", tok)
	}

	assert.Equal(t, int32(1), fp.tokenHits.Load(),
		"a token with no expiry must not be refreshed on every call")
}

// TestRefreshIsSingleFlighted guards providers that rotate refresh tokens
// (Strava invalidates the old one immediately): concurrent renders must
// not each spend the stored refresh token.
func TestRefreshIsSingleFlighted(t *testing.T) {
	fp, prov := newFakeProvider(t, "42")
	svc, db := newTestService(t, prov, "test-secret")

	// The shared in-memory DB is per-connection, so pin the pool to one
	// connection before running concurrent callers against it.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	_, err = svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code", nil)
	require.NoError(t, err)
	require.Equal(t, int32(1), fp.tokenHits.Load())

	// Age the stored token past expiry so every caller sees it as stale.
	require.NoError(t, db.Model(&data.Connection{}).
		Where("user_id = ?", "alice").
		Update("access_expires_at", time.Now().Add(-time.Hour)).Error)

	fp.nextAccess = func() string { return "access-BBB" }
	fp.nextRefresh = func() string { return "refresh-BBB" }

	const callers = 8
	var wg sync.WaitGroup
	tokens := make([]string, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Go(func() {
			tokens[i], errs[i] = svc.AccessTokenForUser(context.Background(), prov.Name, "alice")
		})
	}
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		assert.Equal(t, "access-BBB", tokens[i])
	}
	assert.Equal(t, int32(2), fp.tokenHits.Load(),
		"exactly one refresh should follow the initial exchange")
}

// TestRefreshWorksForSecretlessPublicClient covers a device-flow provider
// like GitHub, which the admin enables with a client id alone. New GitHub
// OAuth apps issue expiring tokens by default, so the refresh path has to
// work without a client secret or the connection dies after 8 hours.
func TestRefreshWorksForSecretlessPublicClient(t *testing.T) {
	fp, prov := newFakeProvider(t, "42")
	prov.DeviceAuthURL = fp.server.URL + "/device/code" // marks it a device-flow provider
	prov.DeviceAuthNeedsSecret = false

	svc, db := newTestService(t, prov, "test-secret")

	_, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code", nil)
	require.NoError(t, err)

	require.NoError(t, db.Model(&data.Connection{}).
		Where("user_id = ?", "alice").
		Update("access_expires_at", time.Now().Add(-time.Hour)).Error)

	// As an admin who enabled this provider with a client id alone would
	// have it: no secret available at refresh time.
	svc.GetCreds = func(string) (string, string) { return "public-client-id", "" }
	fp.nextAccess = func() string { return "access-REFRESHED" }
	tok, err := svc.AccessTokenForUser(context.Background(), prov.Name, "alice")
	require.NoError(t, err, "a secretless public client must still be able to refresh")
	assert.Equal(t, "access-REFRESHED", tok)
}

// TestRefreshRequiresSecretForConfidentialClient is the other half: a
// redirect-flow provider with no secret stays disabled.
func TestRefreshRequiresSecretForConfidentialClient(t *testing.T) {
	fp, prov := newFakeProvider(t, "42")
	svc, db := newTestService(t, prov, "test-secret")

	_, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code", nil)
	require.NoError(t, err)
	require.NoError(t, db.Model(&data.Connection{}).
		Where("user_id = ?", "alice").
		Update("access_expires_at", time.Now().Add(-time.Hour)).Error)

	svc.GetCreds = func(string) (string, string) { return "id-only", "" }
	fp.nextAccess = func() string { return "should-not-be-reached" }

	_, err = svc.AccessTokenForUser(context.Background(), prov.Name, "alice")
	assert.ErrorIs(t, err, ErrProviderDisabled)
}

// TestExchangeCodeKeepsIdentityWhenIdentifyFails covers re-connecting when
// the provider's identify call fails: the previously captured athlete id
// and display name must survive rather than being blanked by the upsert.
func TestExchangeCodeKeepsIdentityWhenIdentifyFails(t *testing.T) {
	fp, prov := newFakeProvider(t, "42")
	svc, _ := newTestService(t, prov, "test-secret")

	first, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code-1", []string{"read"})
	require.NoError(t, err)
	require.Equal(t, "42", first.ExternalID)
	require.Equal(t, "Fake User #42", first.DisplayName)

	prov.Identify = func(context.Context, string) (string, string, error) {
		return "", "", errors.New("identify unavailable")
	}
	fp.nextAccess = func() string { return "access-BBB" }

	second, err := svc.ExchangeCode(context.Background(), prov.Name, "alice", "http://localhost/oauth-callback", "code-2", nil)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, "42", second.ExternalID, "identity must survive a failed re-identify")
	assert.Equal(t, "Fake User #42", second.DisplayName)
	assert.Equal(t, "read", second.Scopes, "granted scopes must survive a re-connect that reports none")
}
