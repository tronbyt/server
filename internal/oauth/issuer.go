// Package oauth holds the parts of server-managed OAuth2 that do not depend on
// the HTTP server: endpoint normalization and the issuer key used to match an
// app's OAuth2 schema field with the OAuth clients configured on the server.
package oauth

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

// IssuerKey identifies an authorization server by its normalized pair of
// endpoints. Tronbyt has no discovery, so the endpoints themselves stand in
// for the issuer: a client is offered to an app only when both match exactly.
func IssuerKey(authorizationEndpoint, tokenEndpoint string) (string, error) {
	auth, err := CanonicalEndpoint(authorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("authorization endpoint: %w", err)
	}
	token, err := CanonicalEndpoint(tokenEndpoint)
	if err != nil {
		return "", fmt.Errorf("token endpoint: %w", err)
	}
	return auth + " " + token, nil
}

// CanonicalEndpoint returns the normalized form of an OAuth endpoint URL:
// lowercase scheme and host, punycode for international domains, no default
// port and no trailing slash. The path stays case-sensitive. It rejects URLs
// that are not https (plain http is allowed for loopback hosts only) and URLs
// with a query, fragment or userinfo, so two spellings of one endpoint always
// compare equal and nothing extra rides along with the client secret.
func CanonicalEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Opaque != "" {
		return "", errors.New("URL must be absolute")
	}
	if u.User != nil {
		return "", errors.New("URL must not contain credentials")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", errors.New("URL must not contain a query")
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return "", errors.New("URL must not contain a fragment")
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("URL must have a host")
	}
	if net.ParseIP(host) == nil {
		ascii, err := idna.Lookup.ToASCII(host)
		if err != nil {
			return "", fmt.Errorf("invalid host: %w", err)
		}
		host = ascii
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "https":
	case "http":
		if !isLoopbackHost(host) {
			return "", errors.New("URL must use https")
		}
	default:
		return "", errors.New("URL must use https")
	}

	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}

	return scheme + "://" + host + strings.TrimRight(u.EscapedPath(), "/"), nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
