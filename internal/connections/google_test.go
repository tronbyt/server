package connections

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"
)

func TestRegistryMatchesGoogle(t *testing.T) {
	r := NewRegistry(Strava(), Spotify(), GitHub(), Google())

	cases := []struct {
		name string
		url  string
		want string // expected provider name; "" means no match
	}{
		{"google authorize", "https://accounts.google.com/o/oauth2/v2/auth", "google"},
		{"google with query", "https://accounts.google.com/o/oauth2/v2/auth?client_id=x&scope=y", "google"},
		{"google case mixed", "https://ACCOUNTS.GOOGLE.COM/o/oauth2/v2/auth", "google"},
		{"legacy v1 path same host", "https://accounts.google.com/o/oauth2/auth", "google"},
		{"token host is not the authorize host", "https://oauth2.googleapis.com/token", ""},
		{"unrelated google host", "https://www.googleapis.com/auth/analytics.readonly", ""},
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

// TestGoogleAuthCodeURLParams pins the parameters Google's refresh-token
// issuance hinges on: access_type=offline plus prompt=consent, and never
// the legacy approval_prompt, which Google rejects when prompt is
// present. Pinned here (not just via the default options) because the
// provider declares them explicitly in AuthCodeParams.
func TestGoogleAuthCodeURLParams(t *testing.T) {
	prov := Google()
	cfg := prov.OAuth2Config("id", "secret", "https://tronbyt.example.com/oauth-callback", nil)

	authURL, err := url.Parse(cfg.AuthCodeURL("state123", prov.AuthCodeOptions()...))
	if err != nil {
		t.Fatalf("parse auth url: %v", err)
	}
	q := authURL.Query()
	assert.Equal(t, "offline", q.Get("access_type"))
	assert.Equal(t, "consent", q.Get("prompt"))
	assert.False(t, q.Has("approval_prompt"),
		"Google errors when approval_prompt is combined with prompt")
}

func TestGoogleProviderShape(t *testing.T) {
	prov := Google()
	cfg := prov.OAuth2Config("id", "secret", "https://tronbyt.example.com/oauth-callback", nil)

	// Confidential client on the redirect flow only: Google's device-flow
	// scope allowlist excludes the API scopes apps declare.
	assert.True(t, prov.SupportsCodeFlow())
	assert.False(t, prov.SupportsDeviceAuth())

	// Client credentials go in POST params; token endpoint is the
	// dedicated googleapis host.
	assert.Equal(t, oauth2.AuthStyleInParams, cfg.Endpoint.AuthStyle)
	assert.Equal(t, "https://oauth2.googleapis.com/token", cfg.Endpoint.TokenURL)

	// No Identify: userinfo would need an email/profile scope we don't
	// request.
	assert.Nil(t, prov.Identify)
}

// TestGoogleScopePassThrough checks both halves of scope handling: schema
// -declared scopes are requested verbatim (space-joined, no ScopeJoin
// quirk), and the analytics scope serves as the fallback when the schema
// declares none.
func TestGoogleScopePassThrough(t *testing.T) {
	prov := Google()

	cases := []struct {
		name      string
		scopes    []string
		wantScope string
	}{
		{
			"schema scopes win",
			[]string{"https://www.googleapis.com/auth/calendar.readonly"},
			"https://www.googleapis.com/auth/calendar.readonly",
		},
		{
			"multiple scopes space-joined",
			[]string{"https://www.googleapis.com/auth/analytics.readonly", "openid"},
			"https://www.googleapis.com/auth/analytics.readonly openid",
		},
		{
			"defaults when schema declares none",
			nil,
			"https://www.googleapis.com/auth/analytics.readonly",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := prov.OAuth2Config("id", "secret", "https://tronbyt.example.com/oauth-callback", tc.scopes)
			authURL, err := url.Parse(cfg.AuthCodeURL("state123", prov.AuthCodeOptions()...))
			if err != nil {
				t.Fatalf("parse auth url: %v", err)
			}
			assert.Equal(t, tc.wantScope, authURL.Query().Get("scope"))
		})
	}
}
