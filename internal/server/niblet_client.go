package server

// Niblet Cloud client shared by install counts and community app stats.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// nibletHTTPClient is shared so sequential calls reuse connections. It follows
// no redirects.
var nibletHTTPClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// errNibletUnauthorized means Niblet Cloud rejected a bearer token.
var errNibletUnauthorized = errors.New("niblet cloud rejected the token")

// nibletRequest calls Niblet Cloud. It allows HTTPS, or HTTP only on loopback
// for development, follows no redirects, and reads at most 1 MiB.
func (s *Server) nibletRequest(ctx context.Context, method, path, token string, body any) (int, []byte, error) {
	if !s.isNibletCloudEnabled() {
		return 0, nil, errors.New("niblet cloud is disabled")
	}
	base, err := url.Parse(s.Config.NibletCloudURL)
	if err != nil || base == nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		(base.Scheme != "https" && (base.Scheme != "http" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost" && base.Hostname() != "::1"))) {
		return 0, nil, errors.New("niblet cloud requires HTTPS (HTTP is allowed only on loopback)")
	}
	var encoded io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		encoded = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base.String(), "/")+path, encoded)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := nibletHTTPClient.Do(req)
	if err != nil {
		return 0, nil, errors.New("niblet cloud request failed")
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			slog.Debug("Failed to close Niblet Cloud response", "error", err)
		}
	}()
	payload, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
	if len(payload) > 1024*1024 {
		return res.StatusCode, nil, errors.New("niblet response too large")
	}
	return res.StatusCode, payload, err
}

// nibletCall sends a request and returns the body when Niblet Cloud answers
// with the wanted status. A 401 for an authenticated call returns
// errNibletUnauthorized so callers can drop the token.
func (s *Server) nibletCall(ctx context.Context, method, path, token string, body any, want int) ([]byte, error) {
	status, payload, err := s.nibletRequest(ctx, method, path, token, body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized && token != "" {
		return nil, errNibletUnauthorized
	}
	if status != want {
		return nil, fmt.Errorf("niblet cloud returned HTTP %d", status)
	}
	return payload, nil
}
