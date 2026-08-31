package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/stretchr/testify/require"
)

func startOIDCAuthWithMetadata(t *testing.T, codeChallengeMethods []string) *httptest.ResponseRecorder {
	t.Helper()
	s := newTestServer(t)

	metadata := map[string]any{
		"issuer":                 "will-be-overridden",
		"authorization_endpoint": "will-be-overridden",
		"token_endpoint":         "will-be-overridden",
		"jwks_uri":               "will-be-overridden",
	}
	if codeChallengeMethods != nil {
		metadata["code_challenge_methods_supported"] = codeChallengeMethods
	}

	srv := newDiscoveryServer(t, metadata)
	metadata["issuer"] = srv.URL
	metadata["authorization_endpoint"] = srv.URL + "/auth"
	metadata["token_endpoint"] = srv.URL + "/token"
	metadata["jwks_uri"] = srv.URL + "/keys"

	s.Config.OIDCEnabled = true
	s.Config.OIDCIssuerURL = srv.URL
	s.Config.OIDCClientID = "client-id"
	s.Config.OIDCClientSecret = "client-secret"

	prov, err := s.setupOIDCProvider(context.Background())
	require.NoError(t, err)
	s.OIDCProvider = prov

	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/login", nil)
	rr := httptest.NewRecorder()
	s.startOIDCAuth(rr, req, false)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	return rr
}

func newDiscoveryServer(t *testing.T, metadata map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(metadata); err != nil {
			t.Errorf("failed to encode discovery document: %v", err)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestProviderSupportsPKCE(t *testing.T) {
	tests := []struct {
		name     string
		methods  []string
		expected bool
	}{
		{name: "advertises S256", methods: []string{"S256"}, expected: true},
		{name: "advertises S256 among others", methods: []string{"plain", "S256"}, expected: true},
		{name: "advertises plain only", methods: []string{"plain"}, expected: false},
		{name: "advertises no methods", methods: nil, expected: false},
		{name: "advertises empty methods", methods: []string{}, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := map[string]any{
				"issuer": "will-be-overridden",
			}
			if tt.methods != nil {
				metadata["code_challenge_methods_supported"] = tt.methods
			}

			srv := newDiscoveryServer(t, metadata)
			metadata["issuer"] = srv.URL

			provider, err := oidc.NewProvider(context.Background(), srv.URL)
			require.NoError(t, err)
			require.Equal(t, tt.expected, providerSupportsPKCE(provider))
		})
	}
}

func TestStartOIDCAuthIncludesPKCEWhenSupported(t *testing.T) {
	rr := startOIDCAuthWithMetadata(t, []string{"S256"})

	location := rr.Result().Header.Get("Location")
	require.NotEmpty(t, location)
	require.Contains(t, location, "code_challenge_method=S256")
	require.Contains(t, location, "code_challenge=")
}

func TestStartOIDCAuthOmitsPKCEWhenUnsupported(t *testing.T) {
	rr := startOIDCAuthWithMetadata(t, nil)

	location := rr.Result().Header.Get("Location")
	require.NotEmpty(t, location)
	require.NotContains(t, location, "code_challenge")
	require.NotContains(t, location, "code_challenge_method")
}
