package config

import (
	"os"
	"testing"
)

func TestLoadSettings(t *testing.T) {
	if err := os.Setenv("DATA_DIR", "testdata"); err != nil {
		t.Fatalf("Failed to set env: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("DATA_DIR"); err != nil {
			t.Logf("Failed to unset env: %v", err)
		}
	}()

	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings failed: %v", err)
	}

	if cfg.DataDir != "testdata" {
		t.Errorf("Expected DATA_DIR 'testdata', got '%s'", cfg.DataDir)
	}
}
