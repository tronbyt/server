package server

import "testing"

func stringPtr(s string) *string {
	return &s
}

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
