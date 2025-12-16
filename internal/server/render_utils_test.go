package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]string
	}{
		{
			name: "User reported case",
			input: map[string]any{
				"heigth_bar": "1",
				"location": map[string]any{
					"description": "Dummy Address, City, Country",
					"lat":         "0.0",
					"lng":         "0.0",
					"locality":    "DummyCity",
					"place_id":    "dummy_place_id",
					"timezone":    "America/New_York",
				},
				"show_text": "true",
				"width_bar": "4",
			},
			expected: map[string]string{
				"heigth_bar": "1",
				"location":   `{"description":"Dummy Address, City, Country","lat":"0.0","lng":"0.0","locality":"DummyCity","place_id":"dummy_place_id","timezone":"America/New_York"}`,
				"show_text":  "true",
				"width_bar":  "4",
			},
		},
		{
			name: "Simple string values",
			input: map[string]any{
				"key1": "value1",
				"key2": "value2",
			},
			expected: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
		},
		{
			name: "Integer value",
			input: map[string]any{
				"key": 123,
			},
			expected: map[string]string{
				"key": "123",
			},
		},
		{
			name: "Boolean value",
			input: map[string]any{
				"key": true,
			},
			expected: map[string]string{
				"key": "true",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeConfig(tt.input)

			// Check if keys match
			if len(got) != len(tt.expected) {
				t.Errorf("normalizeConfig() returned %d keys, want %d", len(got), len(tt.expected))
			}

			for k, v := range tt.expected {
				gotVal, ok := got[k]
				if !ok {
					t.Errorf("normalizeConfig() missing key %s", k)
					continue
				}

				// For JSON strings, the order of keys might differ, so we should unmarshal and compare if it looks like JSON
				if (strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}")) || (strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]")) {
					var gotJSON, expectedJSON any
					if err := json.Unmarshal([]byte(gotVal), &gotJSON); err != nil {
						t.Errorf("normalizeConfig() produced invalid JSON for key %s: %v", k, err)
						continue
					}
					if err := json.Unmarshal([]byte(v), &expectedJSON); err != nil {
						t.Errorf("Test case expected value is invalid JSON for key %s: %v", k, err)
						continue
					}
					if !reflect.DeepEqual(gotJSON, expectedJSON) {
						t.Errorf("normalizeConfig() for key %s = %s, want %s", k, gotVal, v)
					}
				} else {
					if gotVal != v {
						t.Errorf("normalizeConfig() for key %s = %v, want %v", k, gotVal, v)
					}
				}
			}
		})
	}
}
