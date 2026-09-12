package connections

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"
)

func TestRegistryMatchesBuiltinProviders(t *testing.T) {
	r := NewRegistry(Strava(), Spotify())

	cases := []struct {
		name string
		url  string
		want string // expected provider name; "" means no match
	}{
		{"strava", "https://www.strava.com/oauth/authorize", "strava"},
		{"spotify", "https://accounts.spotify.com/authorize", "spotify"},
		{"spotify with query", "https://accounts.spotify.com/authorize?client_id=x", "spotify"},
		{"unknown host", "https://example.com/oauth/authorize", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := r.MatchAuthorizeURL(tc.url)
			if tc.want == "" {
				assert.False(t, ok)
				return
			}
			assert.True(t, ok)
			assert.Equal(t, tc.want, got.Name)
		})
	}
}

// TestStravaScopesAreCommaJoined pins Strava's unusual scope encoding:
// the authorize URL must carry one comma-delimited scope value, not the
// space-joined list the oauth2 library would produce by default.
func TestStravaScopesAreCommaJoined(t *testing.T) {
	cfg := Strava().OAuth2Config("id", "secret", "https://tronbyt.example.com/oauth-callback", []string{"read", "activity:read"})

	authURL, err := url.Parse(cfg.AuthCodeURL("state123"))
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	assert.Equal(t, "read,activity:read", authURL.Query().Get("scope"))
	assert.False(t, strings.Contains(authURL.Query().Get("scope"), " "),
		"Strava rejects space-delimited scopes encoded as '+'")

	// Strava wants client credentials as POST params, not a Basic header.
	assert.Equal(t, oauth2.AuthStyleInParams, cfg.Endpoint.AuthStyle)
}

func TestSpotifyConfigUsesBasicAuthAndSpaceScopes(t *testing.T) {
	prov := Spotify()
	cfg := prov.OAuth2Config("id", "secret", "https://tronbyt.example.com/oauth-callback", nil)

	assert.Equal(t, oauth2.AuthStyleInHeader, cfg.Endpoint.AuthStyle)
	assert.Equal(t, "https://accounts.spotify.com/api/token", cfg.Endpoint.TokenURL)

	authURL, err := url.Parse(cfg.AuthCodeURL("state123"))
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	// Defaults apply when the schema declares no scopes, space-delimited.
	assert.Equal(t, strings.Join(prov.DefaultScopes, " "), authURL.Query().Get("scope"))
}
