package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"tronbyt-server/internal/gitutils"
	"tronbyt-server/internal/version"

	"golang.org/x/mod/semver"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		slog.Error("Failed to write health response", "error", err)
	}
}

func (s *Server) handleUpdateFirmware(w http.ResponseWriter, r *http.Request) {
	err := s.UpdateFirmwareBinaries()
	if err != nil {
		slog.Error("Failed to update firmware binaries", "error", err)
	}

	if r.Header.Get("Accept") == "application/json" {
		// Firmware version info
		version := s.GetLatestFirmwareVersion()
		if version == "" {
			version = "unknown"
		}

		resp := map[string]any{"success": err == nil, "version": version}
		if err != nil {
			resp["error"] = err.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Error("Failed to encode firmware response", "error", err)
		}
		return
	}

	http.Redirect(w, r, "/auth/edit", http.StatusSeeOther)
}

func (s *Server) checkForUpdates() {
	s.doUpdateCheck()
	ticker := time.NewTicker(1 * time.Hour)
	for range ticker.C {
		s.doUpdateCheck()
	}
}

func (s *Server) autoRefreshSystemRepo() {
	if s.Config.SystemAppsAutoRefresh != "1" {
		return
	}

	slog.Info("Scheduled system apps auto-refresh enabled (every 12h)")

	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		if s.Config.SystemAppsAutoRefresh != "1" {
			slog.Info("System apps auto-refresh disabled, stopping ticker")
			return
		}
		slog.Info("Performing scheduled system apps refresh")
		if err := s.refreshSystemRepo(); err != nil {
			slog.Error("Scheduled refresh of system repo failed", "error", err)
		}
	}
}

func (s *Server) refreshSystemRepo() error {
	repoURL := s.Config.SystemAppsRepo
	appsPath := filepath.Join(s.DataDir, "system-apps")
	if err := gitutils.EnsureRepo(appsPath, repoURL, s.Config.GitHubToken, true); err != nil {
		return err
	}

	s.RefreshSystemAppsCache()
	return nil
}

func (s *Server) doUpdateCheck() {
	if s.Config.EnableUpdateChecks != "1" {
		return
	}

	url := "https://api.github.com/repos/tronbyt/server/releases/latest"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		slog.Debug("Failed to create HTTP request for update check", "error", err)
		return
	}

	// Read stored ETag
	if val, err := s.getSetting("system_update_etag"); err == nil && val != "" {
		req.Header.Set("If-None-Match", val)
	}

	githubToken := s.Config.GitHubToken
	if githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+githubToken)
	}

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// Log debug only to reduce noise
		slog.Debug("Failed to check for updates", "error", err)
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Debug("Failed to close response body", "error", err)
		}
	}()

	var latestVersion, releaseURL string

	switch resp.StatusCode {
	case http.StatusNotModified:
		// Load persisted version info only if both are present
		tag, errTag := s.getSetting("system_update_latest_tag")
		url, errURL := s.getSetting("system_update_latest_url")
		if errTag == nil && errURL == nil && tag != "" && url != "" {
			latestVersion = tag
			releaseURL = url
		}
	case http.StatusOK:
		// Save new ETag
		newETag := resp.Header.Get("ETag")
		if newETag != "" {
			if err := s.setSetting("system_update_etag", newETag); err != nil {
				slog.Error("Failed to save system update ETag setting", "error", err)
			}
		}

		var release struct {
			TagName string `json:"tag_name"`
			HTMLURL string `json:"html_url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
			return
		}
		latestVersion = release.TagName
		releaseURL = release.HTMLURL

		// Persist version info
		if err := s.setSetting("system_update_latest_tag", latestVersion); err != nil {
			slog.Error("Failed to save system update latest tag", "error", err)
		}
		if err := s.setSetting("system_update_latest_url", releaseURL); err != nil {
			slog.Error("Failed to save system update latest URL", "error", err)
		}
	default:
		return
	}

	if latestVersion == "" {
		return
	}

	currentVersion := version.Version
	if !strings.HasPrefix(currentVersion, "v") {
		currentVersion = "v" + currentVersion
	}
	if !strings.HasPrefix(latestVersion, "v") {
		latestVersion = "v" + latestVersion
	}

	if semver.IsValid(currentVersion) && semver.IsValid(latestVersion) {
		if semver.Compare(latestVersion, currentVersion) > 0 {
			slog.Info("Update available", "current", version.Version, "latest", latestVersion)
			s.UpdateAvailable = true
			s.LatestReleaseURL = releaseURL
		}
	}
}
