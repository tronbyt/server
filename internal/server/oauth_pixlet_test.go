package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestOAuthWithPixletApp runs the sign-in against a real app: pixlet reads
// the schema, and the rendered app calls an API with the token it was given.
func TestOAuthWithPixletApp(t *testing.T) {
	env := newOAuthTestEnv(t)
	env.s.schemaLoader = nil

	var mu sync.Mutex
	var seenAuth []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"login":"octocat"}`))
	}))
	defer api.Close()

	app := `
load("http.star", "http")
load("render.star", "render")
load("schema.star", "schema")

def main(config):
    token = config.get("auth")
    if not token:
        return render.Root(child = render.Text("sign in"))
    res = http.get("` + api.URL + `/user", headers = {"Authorization": "Bearer " + token})
    return render.Root(child = render.Text(res.json()["login"]))

def get_schema():
    return schema.Schema(
        version = "1",
        fields = [
            schema.OAuth2(
                id = "auth",
                name = "GitHub",
                desc = "Connect",
                icon = "github",
                authorization_endpoint = "` + env.provider.authURL() + `",
                token_endpoint = "` + env.provider.tokenURL() + `",
                authorization_params = {"prompt": "consent"},
                scopes = ["read:user"],
            ),
        ],
    )
`
	rel := "users/alice/apps/gh/gh.star"
	full := filepath.Join(env.s.DataDir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(app), 0o644))
	_, err := gorm.G[data.App](env.s.DB).Where("id = ?", env.app.ID).
		Select("Path", "Pushed").Updates(context.Background(), data.App{Path: &rel, Pushed: false})
	require.NoError(t, err)

	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())
	rr := env.do(t, http.MethodGet, "/devices/dev1/100/config", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"server_managed":true`)
	assert.Contains(t, rr.Body.String(), client.ID)

	rr = env.do(t, http.MethodPost, "/devices/dev1/100/oauth/auth/connect", url.Values{"client": {client.ID}})
	require.Equal(t, http.StatusSeeOther, rr.Code, rr.Body.String())
	loc, err := url.Parse(rr.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "consent", loc.Query().Get("prompt"))
	env.provider.challenge = loc.Query().Get("code_challenge")
	env.provider.redirectURI = loc.Query().Get("redirect_uri")

	// The callback stores the token and renders the app right away.
	rr = env.do(t, http.MethodGet, "/oauth/callback/"+client.ID+"?code=good-code&state="+url.QueryEscape(loc.Query().Get("state")), nil)
	require.Equal(t, "/devices/dev1/100/config", rr.Header().Get("Location"))

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, seenAuth, "the app was rendered and called the API")
	assert.Equal(t, "Bearer at-1", seenAuth[len(seenAuth)-1])
}
