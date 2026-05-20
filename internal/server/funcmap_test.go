package server

import (
	"testing"
	"time"

	"tronbyt-server/internal/data"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/stretchr/testify/assert"
	"golang.org/x/text/language"
)

func TestHumanizeTime(t *testing.T) {
	bundle := i18n.NewBundle(language.English)
	localizer := i18n.NewLocalizer(bundle, "en")

	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{
			name:     "Zero Time",
			input:    time.Time{},
			expected: "Never",
		},
		{
			name:     "Nil Pointer",
			input:    (*time.Time)(nil),
			expected: "Never",
		},
		{
			name:     "Int64 Zero",
			input:    int64(0),
			expected: "Never",
		},
		{
			name:     "Epoch Time",
			input:    time.Unix(0, 0),
			expected: "Never",
		},
		{
			name:     "Epoch Time UTC",
			input:    time.Unix(0, 0).UTC(),
			expected: "Never",
		},
		{
			name:     "Epoch Pointer",
			input:    func() *time.Time { t := time.Unix(0, 0); return &t }(),
			expected: "Never",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Using "TimeAgo" as prefix since it uses humanizeTime internally
			result := humanizeTime(localizer, tt.input, "TimeAgo")
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTmplContains(t *testing.T) {
	tests := []struct {
		name     string
		slice    any
		item     string
		expected bool
	}{
		{
			name:     "String slice contains item",
			slice:    []string{"a", "b", "c"},
			item:     "b",
			expected: true,
		},
		{
			name:     "String slice does not contain item",
			slice:    []string{"a", "b", "c"},
			item:     "d",
			expected: false,
		},
		{
			name:     "Any slice contains item",
			slice:    []any{"a", 1, true},
			item:     "a",
			expected: true,
		},
		{
			name:     "Any slice does not contain item",
			slice:    []any{"a", 1, true},
			item:     "b",
			expected: false,
		},
		{
			name:     "Data StringSlice contains item",
			slice:    data.StringSlice{"a", "b", "c"},
			item:     "c",
			expected: true,
		},
		{
			name:     "Nil slice",
			slice:    nil,
			item:     "a",
			expected: false,
		},
		{
			name:     "Not a slice",
			slice:    "not a slice",
			item:     "a",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tmplContains(tt.slice, tt.item)
			assert.Equal(t, tt.expected, result)
		})
	}
}
