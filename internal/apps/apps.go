package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"log/slog"

	"gopkg.in/yaml.v3"
)

// Manifest reflects the structure of manifest.yaml for system apps.
type Manifest struct {
	ID                  string  `yaml:"id"`
	Name                string  `yaml:"name"`
	Summary             string  `yaml:"summary"`
	Desc                string  `yaml:"desc"`
	Author              string  `yaml:"author"`
	FileName            string  `yaml:"fileName"`
	PackageName         string  `yaml:"packageName"`
	RecommendedInterval int     `yaml:"recommendedInterval"`
	Supports2x          bool    `yaml:"supports2x"`
	Preview             string  `yaml:"preview"`   // Primary preview image
	Preview2x           string  `yaml:"preview2x"` // Secondary 2x preview image
	Broken              *bool   `yaml:"broken,omitempty"`
	BrokenReason        *string `yaml:"brokenReason,omitempty"`
	Date                string  `yaml:"date"` // Optional
}

type AppMetadata struct {
	ID                  string `json:"name"`        // JSON "name" is the package name / ID
	Name                string `json:"displayName"` // JSON "displayName" is the human readable name
	Summary             string `json:"summary"`
	Desc                string `json:"description"`
	Author              string `json:"author"`
	Preview             string `json:"image"`
	Preview2x           string `json:"image2x,omitempty"` // For 2x preview images
	FileName            string `json:"starFile"`
	RecommendedInterval int    `json:"recommendedInterval"`
	Supports2x          bool   `json:"supports2x,omitempty"`
	Date                string `json:"date,omitempty"` // When the app was added/updated

	// Fields populated by logic
	Path         string `json:"path"`
	PackageName  string `json:"packageName"`
	IsInstalled  bool   `json:"is_installed"`
	Broken       bool   `json:"broken"` // Not in JSON, but useful
	BrokenReason string `json:"brokenReason,omitempty"`
}

func ListSystemApps(dataDir string) ([]AppMetadata, error) {
	var apps []AppMetadata
	var err error

	// Always scan the directory
	apps, err = scanSystemApps(dataDir)
	if err != nil {
		return nil, err
	}

	// Now, for each app, try to read from its manifest.yaml (overrides or supplements)
	for i := range apps {
		dirName := apps[i].ID // Capture directory name (initially set to ID)
		appDir := filepath.Join(dataDir, "system-apps", "apps", dirName)
		manifestPath := filepath.Join(appDir, "manifest.yaml")

		var manifest Manifest
		if manifestData, err := os.ReadFile(manifestPath); err == nil {
			if err := yaml.Unmarshal(manifestData, &manifest); err != nil {
				slog.Warn("Failed to parse manifest.yaml", "appID", apps[i].ID, "error", err)
			} else {
				// Merge data from manifest, prioritizing it
				if manifest.ID != "" {
					apps[i].ID = manifest.ID
				}
				if manifest.Name != "" {
					apps[i].Name = manifest.Name
				}
				if manifest.Summary != "" {
					apps[i].Summary = manifest.Summary
				}
				if manifest.Desc != "" {
					apps[i].Desc = manifest.Desc
				}
				if manifest.Author != "" {
					apps[i].Author = manifest.Author
				}
				if manifest.FileName != "" {
					apps[i].FileName = manifest.FileName
				}
				if manifest.PackageName != "" {
					apps[i].PackageName = manifest.PackageName
				}
				apps[i].RecommendedInterval = manifest.RecommendedInterval
				if manifest.Preview != "" {
					apps[i].Preview = filepath.Join(dirName, manifest.Preview)
				}
				if manifest.Preview2x != "" {
					apps[i].Preview2x = filepath.Join(dirName, manifest.Preview2x)
				}
				apps[i].Supports2x = manifest.Supports2x
				// Handle *bool and *string
				if manifest.Broken != nil {
					apps[i].Broken = *manifest.Broken
				}
				if manifest.BrokenReason != nil {
					apps[i].BrokenReason = *manifest.BrokenReason
				}
				if manifest.Date != "" {
					apps[i].Date = manifest.Date
				}
			}
		} else {
			slog.Debug("manifest.yaml not found for app", "appID", apps[i].ID, "error", err)
		}

		// Finalize paths and defaults
		if apps[i].PackageName == "" {
			apps[i].PackageName = dirName
		}
		if apps[i].FileName == "" {
			apps[i].FileName = apps[i].PackageName + ".star"
		}
		apps[i].Path = filepath.Join("system-apps", "apps", apps[i].PackageName, apps[i].FileName)

		// Derive Preview if not set
		if apps[i].Preview == "" {
			candidates := []string{apps[i].PackageName}
			starStem := strings.TrimSuffix(apps[i].FileName, ".star")
			if starStem != apps[i].PackageName {
				candidates = append(candidates, starStem)
			}
			candidates = append(candidates, "screenshot")

			found := false
			for _, base := range candidates {
				if found {
					break
				}
				for _, ext := range []string{".webp", ".gif", ".png"} {
					fname := base + ext
					fpath := filepath.Join(appDir, fname)
					if _, err := os.Stat(fpath); err == nil {
						apps[i].Preview = filepath.Join(dirName, fname)

						// Check 2x
						fname2x := base + "@2x" + ext
						fpath2x := filepath.Join(appDir, fname2x)
						if _, err := os.Stat(fpath2x); err == nil {
							apps[i].Preview2x = filepath.Join(dirName, fname2x)
							apps[i].Supports2x = true
						}
						found = true
						break
					}
				}
			}
		} else if apps[i].Preview2x == "" {
			// If Preview was set from manifest, check for matching 2x if not set
			relPreview := strings.TrimPrefix(apps[i].Preview, dirName+string(filepath.Separator))
			ext := filepath.Ext(relPreview)
			base := strings.TrimSuffix(relPreview, ext)
			fname2x := base + "@2x" + ext
			fpath2x := filepath.Join(appDir, fname2x)
			if _, err := os.Stat(fpath2x); err == nil {
				apps[i].Preview2x = filepath.Join(dirName, fname2x)
				apps[i].Supports2x = true
			}
		}
	}

	return apps, nil
}

func scanSystemApps(dataDir string) ([]AppMetadata, error) {
	appsDir := filepath.Join(dataDir, "system-apps", "apps")
	var apps []AppMetadata

	entries, err := os.ReadDir(appsDir)
	if os.IsNotExist(err) {
		return nil, nil // Return empty if dir doesn't exist
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read system apps dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			appID := entry.Name()
			// Basic default metadata
			app := AppMetadata{
				ID:          appID,
				Name:        appID,
				PackageName: appID,
				FileName:    appID + ".star",
			}
			apps = append(apps, app)
		}
	}
	return apps, nil
}

func ListUserApps(dataDir, username string) ([]AppMetadata, error) {
	userAppsDir := filepath.Join(dataDir, "users", username, "apps")
	var apps []AppMetadata

	entries, err := os.ReadDir(userAppsDir)
	if os.IsNotExist(err) {
		return apps, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read user apps directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			appName := entry.Name()
			appDir := filepath.Join(userAppsDir, appName)

			// Default AppMetadata for user app
			userApp := AppMetadata{
				ID:          appName,
				Name:        appName,
				PackageName: appName,
				Author:      username,
				Summary:     "User uploaded app",
				IsInstalled: false,
			}

			// Try to find .star file
			files, _ := os.ReadDir(appDir)
			var starFile string
			for _, f := range files {
				if filepath.Ext(f.Name()) == ".star" {
					starFile = f.Name()
					break
				}
			}

			if starFile != "" {
				userApp.FileName = starFile
				userApp.Path = filepath.Join("users", username, "apps", appName, starFile)

				// Infer Preview/Preview2x from convention if files exist
				baseFileName := strings.TrimSuffix(starFile, ".star")
				previewWebP := baseFileName + ".webp"
				preview2xWebP := baseFileName + "@2x.webp"

				if _, err := os.Stat(filepath.Join(appDir, previewWebP)); err == nil {
					userApp.Preview = previewWebP
				}
				if _, err := os.Stat(filepath.Join(appDir, preview2xWebP)); err == nil {
					userApp.Preview2x = preview2xWebP
					userApp.Supports2x = true
				}

				// Use file mod time as date
				if info, err := os.Stat(filepath.Join(appDir, starFile)); err == nil {
					userApp.Date = info.ModTime().Format("2006-01-02 15:04")
				}

				// No broken status for user apps by default
				userApp.Broken = false
				apps = append(apps, userApp)
			}
		}
	}
	return apps, nil
}
