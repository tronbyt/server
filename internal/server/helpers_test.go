package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/version"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/stretchr/testify/assert"
	"golang.org/x/text/language"
)

func TestParseTimeInput(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"04:00", "04:00", false},
		{"15:30", "15:30", false},
		{"04:00:00", "04:00", false},
		{"15:30:59", "15:30", false}, // Explicitly test stripping non-zero seconds
		{"invalid", "", true},
		{"25:00", "", true},
	}

	for _, tt := range tests {
		got, err := parseTimeInput(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseTimeInput(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if got != tt.expected {
			t.Errorf("parseTimeInput(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost:8000", true},
		{"localhost", true},
		{"127.0.0.1:8000", true},
		{"127.0.0.1", true},
		{"[::1]:8000", true},
		{"::1", true},
		{"192.168.1.42:8000", false},
		{"mytronbyt.local", false},
	}
	for _, tt := range tests {
		r := &http.Request{Host: tt.host}
		if got := isLoopbackHost(r); got != tt.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestRequestScheme(t *testing.T) {
	if got := requestScheme(&http.Request{}); got != "http" {
		t.Errorf("requestScheme(plain) = %q, want %q", got, "http")
	}
	if got := requestScheme(&http.Request{URL: &url.URL{Scheme: "https"}}); got != "https" {
		t.Errorf("requestScheme(https URL) = %q, want %q", got, "https")
	}
}

func TestRequestPort(t *testing.T) {
	tests := []struct {
		req  *http.Request
		want string
	}{
		{&http.Request{Host: "localhost:8000"}, "8000"},
		{&http.Request{Host: "192.168.1.42:8443"}, "8443"},
		{&http.Request{Host: "localhost"}, "80"},
		{&http.Request{Host: "localhost", URL: &url.URL{Scheme: "https"}}, "443"},
	}
	for _, tt := range tests {
		if got := requestPort(tt.req); got != tt.want {
			t.Errorf("requestPort(%q) = %q, want %q", tt.req.Host, got, tt.want)
		}
	}
}

func TestDetectedAccessURLNonLoopback(t *testing.T) {
	s := &Server{}
	r := &http.Request{Host: "192.168.1.42:8000"}
	if got := s.detectedAccessURL(r); got != "" {
		t.Errorf("detectedAccessURL(non-loopback) = %q, want empty", got)
	}
}

// The picker is built from a hand-maintained list, so a device type added later
// can silently never appear in it. Every type the server knows must be offered
// exactly once, and grouped under the panel size it actually drives.
func TestDeviceTypeChoicesOfferEveryTypeGroupedByPanelSize(t *testing.T) {
	s := &Server{}
	localizer := i18n.NewLocalizer(i18n.NewBundle(language.English), "en")

	groups := s.getDeviceTypeChoices(localizer)

	labels := make([]string, 0, len(groups))
	seen := make(map[data.DeviceType]int)
	for _, group := range groups {
		labels = append(labels, group.Label)
		assert.NotEmptyf(t, group.Options, "group %q is empty", group.Label)
		for _, option := range group.Options {
			seen[option.Value]++
			width, height := option.Value.DisplaySize()
			assert.Equalf(t, group.Label, fmt.Sprintf("%dx%d", width, height),
				"%s is grouped under %q but drives a %dx%d panel",
				option.Value.Slug(), group.Label, width, height)
		}
	}

	assert.Equal(t, []string{"64x32", "128x64", "64x64"}, labels)

	for deviceType, slug := range data.DeviceTypeToString {
		if deviceType == data.DeviceUnknown {
			continue // deliberately not offered; it is the zero value
		}
		assert.Equalf(t, 1, seen[deviceType], "%s is offered %d times, want exactly 1",
			slug, seen[deviceType])
	}
}

// TestGetBaseURLAndGetDeviceBaseURL verifies the expected behavior under test.
func TestGetBaseURLAndGetDeviceBaseURL(t *testing.T) {
	s := &Server{
		Config: &config.Settings{
			Port: "7000",
		},
	}

	// 1. Request with Host containing no port preserves Host without appending internal port
	reqDirect := &http.Request{Host: "192.168.1.15"}
	assert.Equal(t, "http://192.168.1.15", s.GetBaseURL(reqDirect))

	// 2. Request with explicit port preserves it
	reqWithPort := &http.Request{Host: "192.168.1.15:7000"}
	assert.Equal(t, "http://192.168.1.15:7000", s.GetBaseURL(reqWithPort))

	// 3. Request with X-Forwarded-Host preserves reverse proxy host
	reqProxy := &http.Request{
		Host: "192.168.1.15",
		Header: http.Header{
			"X-Forwarded-Host": []string{"tronbyt.example.com"},
		},
	}
	assert.Equal(t, "http://tronbyt.example.com", s.GetBaseURL(reqProxy))

	// 4. Request with X-Forwarded-Port
	reqProxyPort := &http.Request{
		Host: "tronbyt.example.com",
		Header: http.Header{
			"X-Forwarded-Port":  []string{"8443"},
			"X-Forwarded-Proto": []string{"https"},
		},
	}
	assert.Equal(t, "https://tronbyt.example.com:8443", s.GetBaseURL(reqProxyPort))

	// 5. GetDeviceBaseURL normalizes ws:// -> http:// and wss:// -> https://
	devWS := &data.Device{ImgURL: "ws://192.168.1.15:7000/tronberry/ws"}
	assert.Equal(t, "http://192.168.1.15:7000", s.GetDeviceBaseURL(devWS))

	devWSS := &data.Device{ImgURL: "wss://mytronbyt.internal:9443/tronberry/ws"}
	assert.Equal(t, "https://mytronbyt.internal:9443", s.GetDeviceBaseURL(devWSS))

	devHTTP := &data.Device{ImgURL: "http://192.168.1.15:7000/tronberry/next"}
	assert.Equal(t, "http://192.168.1.15:7000", s.GetDeviceBaseURL(devHTTP))
}

// TestRenderTemplatePopulatesVersionAndCommitURL verifies the expected behavior under test.
func TestRenderTemplatePopulatesVersionAndCommitURL(t *testing.T) {
	s := newTestServerAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/login", nil)

	version.EnsureGitInfo()
	origCommit, origBranch, origURL, origTag := version.Commit, version.Branch, version.CommitURL, version.ShowBranchTag
	t.Cleanup(func() {
		version.Commit, version.Branch, version.CommitURL, version.ShowBranchTag = origCommit, origBranch, origURL, origTag
	})
	version.Commit = "abcdef1234567890abcdef1234567890abcdef12"
	version.Branch = "feat/test"
	version.CommitURL = "https://github.com/tronbyt/server/tree/" + version.Commit
	version.ShowBranchTag = true

	// Case 1: When ShowBranchTag is true (default in this test environment on custom branch)
	rr := httptest.NewRecorder()
	s.renderTemplate(rr, req, "login", TemplateData{})
	assert.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, "Server")
	assert.NotContains(t, body, "(unknown)")
	assert.Contains(t, body, version.CommitURL)
	assert.Contains(t, body, version.Commit[:7])
	if version.ShowBranchTag {
		assert.Contains(t, body, "["+version.Branch+"]("+version.Commit[:7]+")")
	}

	// Case 2: When ShowBranchTag is false (upstream main)
	origShowBranchTag := version.ShowBranchTag
	defer func() { version.ShowBranchTag = origShowBranchTag }()
	version.ShowBranchTag = false

	rr2 := httptest.NewRecorder()
	s.renderTemplate(rr2, req, "login", TemplateData{})
	assert.Equal(t, http.StatusOK, rr2.Code)
	body2 := rr2.Body.String()
	assert.Contains(t, body2, "Server")
	assert.Contains(t, body2, version.Commit[:7])
	assert.NotContains(t, body2, "["+version.Branch+"]")
}

func TestGetBaseURL(t *testing.T) {
	s := &Server{}
	tests := []struct {
		name    string
		host    string
		headers map[string]string
		want    string
	}{
		{"plain", "example.com", nil, "http://example.com"},
		{"custom port", "example.com:8000", nil, "http://example.com:8000"},
		{"https proxy default port", "internal:8000", map[string]string{
			"X-Forwarded-Proto": "https", "X-Forwarded-Host": "tronbyt.example", "X-Forwarded-Port": "443",
		}, "https://tronbyt.example"},
		{"https proxy custom port", "internal:8000", map[string]string{
			"X-Forwarded-Proto": "https", "X-Forwarded-Host": "tronbyt.example", "X-Forwarded-Port": "8443",
		}, "https://tronbyt.example:8443"},
		{"http default port in host", "example.com:80", nil, "http://example.com"},
		{"https port on http is kept", "example.com:443", nil, "http://example.com:443"},
		{"ipv6 default port", "[::1]:80", nil, "http://[::1]"},
		{"ipv6 custom port", "[::1]:8000", nil, "http://[::1]:8000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tt.host
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			assert.Equal(t, tt.want, s.GetBaseURL(req))
		})
	}
}
