package apps

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListUserApps(t *testing.T) {
	tmpDir := t.TempDir()
	userDir := filepath.Join(tmpDir, "users", "testuser", "apps")
	repoDir := filepath.Join(tmpDir, "users", "testuser", "repo", "apps")

	// Create app1 in 'apps' (Uploaded)
	app1Dir := filepath.Join(userDir, "app1")
	if err := os.MkdirAll(app1Dir, 0755); err != nil {
		t.Fatalf("Failed to create app1 dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(app1Dir, "app1.star"), []byte("load()"), 0644); err != nil {
		t.Fatalf("Failed to write app1 star file: %v", err)
	}

	// Create app2 in 'repo' (Git)
	app2Dir := filepath.Join(repoDir, "app2")
	if err := os.MkdirAll(app2Dir, 0755); err != nil {
		t.Fatalf("Failed to create app2 dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(app2Dir, "app2.star"), []byte("load()"), 0644); err != nil {
		t.Fatalf("Failed to write app2 star file: %v", err)
	}

	// Test listing
	apps := ListUserApps(tmpDir, "testuser")
	if len(apps) != 2 {
		t.Fatalf("Expected 2 apps, got %d", len(apps))
	}

	// Verify we got both
	foundApp1 := false
	foundApp2 := false
	for _, app := range apps {
		switch app.ID {
		case "app1":
			foundApp1 = true
			if app.Summary != "User uploaded app" {
				t.Errorf("App1 summary incorrect, got '%s'", app.Summary)
			}
		case "app2":
			foundApp2 = true
			if app.Summary != "Git Repository app" {
				t.Errorf("App2 summary incorrect, got '%s'", app.Summary)
			}
		}
	}

	if !foundApp1 || !foundApp2 {
		t.Errorf("Did not find both apps: foundApp1=%v, foundApp2=%v", foundApp1, foundApp2)
	}
}

// supports64x64 is an assertion by the app author that the app lays out on a
// square panel. Unlike supports2x it is deliberately NOT inferred from a
// screenshot file: a committed image proves a preview exists, not that anyone
// checked the app on a square panel.
func TestSupports64x64ComesFromTheManifestNotAScreenshot(t *testing.T) {
	dir := t.TempDir()
	appsDir := filepath.Join(dir, "system-apps", "apps", "squareapp")
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "id: squareapp\nname: Square App\nfileName: squareapp.star\npackageName: squareapp\nsupports64x64: true\n"
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(appsDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.yaml", manifest)
	write("squareapp.star", "")
	write("screenshot.webp", "")
	write("screenshot@64x64.webp", "")

	found, err := ListSystemApps(dir)
	if err != nil {
		t.Fatal(err)
	}
	var app *AppMetadata
	for i := range found {
		if found[i].ID == "squareapp" {
			app = &found[i]
		}
	}
	if app == nil {
		t.Fatal("squareapp was not scanned")
	}
	if !app.Supports64x64 {
		t.Error("supports64x64 in the manifest was not read")
	}
	if app.PreviewSquare == "" {
		t.Error("a @64x64 preview file beside the app was not picked up")
	}
	if app.Supports2x {
		t.Error("supports2x must not be set: no @2x file and no manifest key")
	}
}

// A custom app's manifest.yaml feeds the add-app filters (category, tags,
// broken), but must not change the ID/PackageName the routes address it by.
func TestListUserAppsReadsManifest(t *testing.T) {
	tmpDir := t.TempDir()
	appDir := filepath.Join(tmpDir, "users", "testuser", "repo", "apps", "myclock")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "id: something-else\nname: My Clock\npackageName: other\nfileName: other.star\n" +
		"category: time\ntags: [clock, retro]\nbroken: true\nbrokenReason: API gone\nupdated: 2026-01-02 03:04\n"
	for name, body := range map[string]string{"manifest.yaml": manifest, "myclock.star": "load()"} {
		if err := os.WriteFile(filepath.Join(appDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	apps := ListUserApps(tmpDir, "testuser")
	if len(apps) != 1 {
		t.Fatalf("Expected 1 app, got %d", len(apps))
	}
	app := apps[0]
	if app.ID != "myclock" || app.PackageName != "myclock" || app.FileName != "myclock.star" {
		t.Errorf("identity fields changed by manifest: ID=%q PackageName=%q FileName=%q", app.ID, app.PackageName, app.FileName)
	}
	if app.Name != "My Clock" || app.Category != "time" || len(app.Tags) != 2 || !app.Broken || app.BrokenReason != "API gone" {
		t.Errorf("manifest fields not applied: %+v", app.Manifest)
	}
	if app.Date != "2026-01-02 03:04" {
		t.Errorf("Date = %q, want manifest updated date", app.Date)
	}
	if app.Author != "testuser" || app.Summary != "Git Repository app" {
		t.Errorf("defaults lost for keys absent from manifest: Author=%q Summary=%q", app.Author, app.Summary)
	}
}

// Image-only custom apps (a .webp with no .star) follow the same date rule as
// .star apps: the manifest's updated date wins over the file mod time.
func TestListUserAppsImageOnlyUsesManifestUpdated(t *testing.T) {
	tmpDir := t.TempDir()
	appDir := filepath.Join(tmpDir, "users", "testuser", "apps", "pic")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"manifest.yaml": "updated: 2026-01-02 03:04\n", "pic.webp": ""} {
		if err := os.WriteFile(filepath.Join(appDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	apps := ListUserApps(tmpDir, "testuser")
	if len(apps) != 1 || apps[0].FileName != "pic.webp" {
		t.Fatalf("expected one image-only app, got %+v", apps)
	}
	if apps[0].Date != "2026-01-02 03:04" {
		t.Errorf("Date = %q, want manifest updated date", apps[0].Date)
	}
}

func TestListUserAppsIgnoresInvalidManifest(t *testing.T) {
	tmpDir := t.TempDir()
	appDir := filepath.Join(tmpDir, "users", "testuser", "apps", "app1")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"manifest.yaml": "name: [unclosed", "app1.star": "load()"} {
		if err := os.WriteFile(filepath.Join(appDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	apps := ListUserApps(tmpDir, "testuser")
	if len(apps) != 1 || apps[0].Name != "app1" || apps[0].Summary != "User uploaded app" {
		t.Fatalf("invalid manifest should leave defaults: %+v", apps)
	}
}
