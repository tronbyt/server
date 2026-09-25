package connections

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"
)

func TestRegistryGetByName(t *testing.T) {
	r := NewRegistry(Strava())

	p, ok := r.Get("strava")
	assert.True(t, ok)
	assert.Equal(t, "strava", p.Name)

	// Case-insensitive lookup.
	p, ok = r.Get("STRAVA")
	assert.True(t, ok)
	assert.Equal(t, "strava", p.Name)

	_, ok = r.Get("nope")
	assert.False(t, ok)
}

// TestAuthCodeOptions pins the per-provider authorize-redirect parameters
// the connect handler sends: the offline+consent default when
// AuthCodeParams is nil (via oauth2.AccessTypeOffline/ApprovalForce,
// which today spell access_type=offline and prompt=consent), a
// provider's own list when set, and nothing extra for an empty non-nil
// slice.
func TestAuthCodeOptions(t *testing.T) {
	cases := []struct {
		name       string
		provider   *Provider
		wantParams map[string]string // expected query params on the auth URL
		absent     []string          // params that must NOT appear
	}{
		{
			"nil params get the offline+consent default",
			Strava(),
			map[string]string{"access_type": "offline", "prompt": "consent"},
			[]string{"approval_prompt"},
		},
		{
			"provider-specific params replace the default",
			Google(),
			map[string]string{"access_type": "offline", "prompt": "consent"},
			[]string{"approval_prompt"},
		},
		{
			"empty non-nil slice means no extra params",
			&Provider{
				Name:           "bare",
				AuthorizeURL:   "https://example.com/authorize",
				TokenURL:       "https://example.com/token",
				AuthCodeParams: []oauth2.AuthCodeOption{},
			},
			map[string]string{},
			[]string{"access_type", "approval_prompt", "prompt"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.provider.OAuth2Config("id", "secret", "https://tronbyt.example.com/oauth-callback", nil)
			authURL, err := url.Parse(cfg.AuthCodeURL("state123", tc.provider.AuthCodeOptions()...))
			if err != nil {
				t.Fatalf("parse auth url: %v", err)
			}
			q := authURL.Query()
			for k, v := range tc.wantParams {
				assert.Equal(t, v, q.Get(k), "param %s", k)
			}
			for _, k := range tc.absent {
				assert.False(t, q.Has(k), "param %s must be absent", k)
			}
		})
	}
}

func TestRegistryMatchAuthorizeURL(t *testing.T) {
	r := NewRegistry(Strava())

	cases := []struct {
		name string
		url  string
		want string // expected provider name; "" means no match
	}{
		{"strava authorize", "https://www.strava.com/oauth/authorize", "strava"},
		{"strava with query", "https://www.strava.com/oauth/authorize?foo=bar", "strava"},
		{"strava case mixed", "https://WWW.STRAVA.COM/oauth/authorize", "strava"},
		{"unrelated host", "https://accounts.spotify.com/authorize", ""},
		{"garbage", "not-a-url", ""},
		{"empty", "", ""},
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
