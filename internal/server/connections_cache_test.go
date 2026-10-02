package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeStarApp writes a minimal pixlet app whose schema declares one
// OAuth2 field pointing at authorizeURL, and returns its path.
func writeStarApp(t *testing.T, dir, authorizeURL string) string {
	t.Helper()
	src := fmt.Sprintf(`
load("render.star", "render")
load("schema.star", "schema")

def main(config):
    return render.Root(child = render.Text("hi"))

def oauth_handler(params):
    return ""

def get_schema():
    return schema.Schema(
        version = "1",
        fields = [
            schema.OAuth2(
                id = "auth",
                name = "Test Provider",
                desc = "Connect your account",
                icon = "link",
                handler = oauth_handler,
                client_id = "unused",
                authorization_endpoint = "%s",
                scopes = ["read"],
            ),
        ],
    )
`, authorizeURL)

	path := filepath.Join(dir, "app.star")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))
	return path
}

// plainStarApp writes an app with no OAuth2 field — the common case that
// must be cached negatively so renders don't re-evaluate starlark.
func plainStarApp(t *testing.T, path string) {
	t.Helper()
	src := `
load("render.star", "render")

def main(config):
    return render.Root(child = render.Text("plain"))
`
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))
}

func TestOAuth2FieldsForAppCachesAndInvalidates(t *testing.T) {
	s := newTestServer(t)
	path := writeStarApp(t, t.TempDir(), "https://www.strava.com/oauth/authorize")

	first := s.oauth2FieldsForApp(context.Background(), path)
	require.Len(t, first, 1)
	assert.Equal(t, "auth", first[0].ID)
	assert.Equal(t, "https://www.strava.com/oauth/authorize", first[0].AuthorizationEndpoint)

	// A second call must hit the cache: same backing array, no re-evaluation.
	second := s.oauth2FieldsForApp(context.Background(), path)
	require.Len(t, second, 1)
	assert.Same(t, &first[0], &second[0], "expected the cached slice, not a fresh parse")

	// Rewriting the app invalidates the entry (mtime and size both change).
	plainStarApp(t, path)
	assert.Empty(t, s.oauth2FieldsForApp(context.Background(), path),
		"cache must invalidate when the app file changes")
}

func TestOAuth2FieldsForAppMissingFile(t *testing.T) {
	s := newTestServer(t)
	assert.Nil(t, s.oauth2FieldsForApp(context.Background(), filepath.Join(t.TempDir(), "nope.star")))
}

// TestInjectConnectionTokensEndToEnd walks the whole path: a connected
// user, an app declaring an OAuth2 field for that provider, and the
// resulting render config carrying a live access token.
func TestInjectConnectionTokensEndToEnd(t *testing.T) {
	s, fp, cookie := makeServerWithFakeProvider(t)
	_ = cookie

	path := writeStarApp(t, t.TempDir(), fp.server.URL+"/authorize")

	// Connect alice to the fake provider.
	_, err := s.Connections.ExchangeCode(
		context.Background(), "fake", "alice",
		"http://localhost/oauth-callback", "auth-code", []string{"read"},
	)
	require.NoError(t, err)

	config := map[string]any{}
	injected := s.injectConnectionTokens(context.Background(), config, path, "alice")

	assert.True(t, injected["auth"], "the oauth2 field should be reported as injected")
	assert.Equal(t, "fake-access-token", config["auth"],
		"apps receive a plain access-token string, never the refresh token")
}

func TestInjectConnectionTokensSkipsUnconnectedUser(t *testing.T) {
	s, fp, _ := makeServerWithFakeProvider(t)
	path := writeStarApp(t, t.TempDir(), fp.server.URL+"/authorize")

	config := map[string]any{}
	injected := s.injectConnectionTokens(context.Background(), config, path, "alice")

	assert.Empty(t, injected)
	assert.NotContains(t, config, "auth",
		"an unconnected user leaves the field absent so the app can prompt")
}
