package server

import (
	"net/http"
	"net/url"
	"testing"
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
