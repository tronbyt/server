package config

import (
	"os"
	"testing"
)

func TestLoadSettings(t *testing.T) {
	if err := os.Setenv("SECRET_KEY", "testkey"); err != nil {
		t.Fatalf("Failed to set env: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("SECRET_KEY"); err != nil {
			t.Logf("Failed to unset env: %v", err)
		}
	}()

	cfg, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings failed: %v", err)
	}

	if cfg.SecretKey != "testkey" {
		t.Errorf("Expected SECRET_KEY 'testkey', got '%s'", cfg.SecretKey)
	}
}
