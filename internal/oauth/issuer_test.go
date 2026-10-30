package oauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalEndpoint(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "https://github.com/login/oauth/authorize", "https://github.com/login/oauth/authorize"},
		{"trailing slash", "https://github.com/login/oauth/authorize/", "https://github.com/login/oauth/authorize"},
		{"uppercase scheme and host", "HTTPS://GitHub.COM/login/oauth/authorize", "https://github.com/login/oauth/authorize"},
		{"path keeps case", "https://example.com/OAuth/Token", "https://example.com/OAuth/Token"},
		{"default port dropped", "https://example.com:443/token", "https://example.com/token"},
		{"custom port kept", "https://example.com:8443/token", "https://example.com:8443/token"},
		{"surrounding spaces", "  https://example.com/token  ", "https://example.com/token"},
		{"idn to punycode", "https://bücher.example/token", "https://xn--bcher-kva.example/token"},
		{"root path", "https://example.com/", "https://example.com"},
		{"http on localhost", "http://localhost:8080/token", "http://localhost:8080/token"},
		{"http on loopback ip", "http://127.0.0.1/token", "http://127.0.0.1/token"},
		{"ipv6 loopback", "http://[::1]:9000/token", "http://[::1]:9000/token"},
		{"ipv6 default port", "https://[2001:db8::1]:443/token", "https://[2001:db8::1]/token"},
		{"escaped path kept", "https://example.com/a%2Fb/token", "https://example.com/a%2Fb/token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalEndpoint(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCanonicalEndpointRejects(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"relative", "/oauth/token"},
		{"http on public host", "http://example.com/token"},
		{"other scheme", "ftp://example.com/token"},
		{"query", "https://example.com/token?tenant=common"},
		{"empty query", "https://example.com/token?"},
		{"fragment", "https://example.com/token#x"},
		{"userinfo", "https://user:pass@example.com/token"},
		{"opaque", "https:example.com/token"},
		{"no host", "https:///token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CanonicalEndpoint(tt.in)
			assert.Error(t, err)
		})
	}
}

func TestIssuerKey(t *testing.T) {
	a, err := IssuerKey("https://GitHub.com/login/oauth/authorize/", "https://github.com:443/login/oauth/access_token")
	require.NoError(t, err)
	b, err := IssuerKey("https://github.com/login/oauth/authorize", "https://github.com/login/oauth/access_token")
	require.NoError(t, err)
	assert.Equal(t, a, b)
	assert.Equal(t, "https://github.com/login/oauth/authorize https://github.com/login/oauth/access_token", a)

	other, err := IssuerKey("https://github.com/login/oauth/authorize", "https://evil.example/token")
	require.NoError(t, err)
	assert.NotEqual(t, a, other)

	_, err = IssuerKey("https://github.com/login/oauth/authorize", "http://evil.example/token")
	assert.ErrorContains(t, err, "token endpoint")
	_, err = IssuerKey("", "https://github.com/login/oauth/access_token")
	assert.ErrorContains(t, err, "authorization endpoint")
}
