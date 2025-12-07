package apps

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListUserApps(t *testing.T) {
	tmpDir := t.TempDir()
	userDir := filepath.Join(tmpDir, "users", "testuser", "apps")

	// Create app
	app1Dir := filepath.Join(userDir, "app1")
	if err := os.MkdirAll(app1Dir, 0755); err != nil {
		t.Fatalf("Failed to create app dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(app1Dir, "app1.star"), []byte("load()"), 0644); err != nil {
		t.Fatalf("Failed to write star file: %v", err)
	}

	apps, err := ListUserApps(tmpDir, "testuser")
	if err != nil {
		t.Fatalf("ListUserApps failed: %v", err)
	}

	if len(apps) != 1 {
		t.Errorf("Expected 1 app, got %d", len(apps))
	}
	if apps[0].ID != "app1" {
		t.Errorf("Expected app ID 'app1', got '%s'", apps[0].ID)
	}
}
