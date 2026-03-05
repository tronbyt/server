package config

import (
	"testing"
)

func TestLoadSettings(t *testing.T) {
	t.Setenv("DATA_DIR", "testdata")

	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings failed: %v", err)
	}

	if cfg.DataDir != "testdata" {
		t.Errorf("Expected DATA_DIR 'testdata', got '%s'", cfg.DataDir)
	}
}
