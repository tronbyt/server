package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		version := "unknown"
		firmwareDir := filepath.Join(s.DataDir, "firmware")
		if vBytes, e := os.ReadFile(filepath.Join(firmwareDir, "firmware_version.txt")); e == nil {
			version = strings.TrimSpace(string(vBytes))
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

func (s *Server) doUpdateCheck() {
	url := "https://api.github.com/repos/tronbyt/server/releases/latest"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		slog.Debug("Failed to create HTTP request for update check", "error", err)
		return
	}

	githubToken := os.Getenv("GITHUB_TOKEN")
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

	if resp.StatusCode != http.StatusOK {
		return
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return
	}

	currentVersion := version.Version
	if !strings.HasPrefix(currentVersion, "v") {
		currentVersion = "v" + currentVersion
	}
	latestVersion := release.TagName
	if !strings.HasPrefix(latestVersion, "v") {
		latestVersion = "v" + latestVersion
	}

	if semver.IsValid(currentVersion) && semver.IsValid(latestVersion) {
		if semver.Compare(latestVersion, currentVersion) > 0 {
			slog.Info("Update available", "current", version.Version, "latest", release.TagName)
			s.UpdateAvailable = true
			s.LatestReleaseURL = release.HTMLURL
		}
	}
}
