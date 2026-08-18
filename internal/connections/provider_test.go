package connections

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
