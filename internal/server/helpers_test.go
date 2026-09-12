package server

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"tronbyt-server/internal/data"

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
