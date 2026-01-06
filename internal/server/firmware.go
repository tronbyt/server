package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/firmware"

	"log/slog"

	"gorm.io/gorm"
)

func (s *Server) UpdateFirmwareBinaries() error {
	firmwareRepo := os.Getenv("FIRMWARE_REPO")
	if firmwareRepo == "" {
		firmwareRepo = "https://github.com/tronbyt/firmware-esp32"
	}

	owner := "tronbyt"
	repo := "firmware-esp32"

	if strings.Contains(firmwareRepo, "github.com/") {
		parts := strings.Split(strings.Split(firmwareRepo, "github.com/")[1], "/")
		if len(parts) >= 2 {
			owner = parts[0]
			repo = strings.TrimSuffix(parts[1], ".git")
		}
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	token := s.Config.GitHubToken
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("Failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"url"` // API URL
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return err
	}

	firmwareDir := filepath.Join(s.DataDir, "firmware")
	if err := os.MkdirAll(firmwareDir, 0755); err != nil {
		return err
	}

	versionFile := filepath.Join(firmwareDir, "firmware_version.txt")
	currentVersion := ""
	if data, err := os.ReadFile(versionFile); err == nil {
		currentVersion = strings.TrimSpace(string(data))
	}

	if currentVersion == release.TagName {
		slog.Info("Firmware up to date", "version", currentVersion)
		return nil
	}

	mapping := map[string]string{
		"tidbyt-gen1_firmware.bin":               "tidbyt-gen1.bin",
		"tidbyt-gen1_swap_firmware.bin":          "tidbyt-gen1_swap.bin",
		"tidbyt-gen2_firmware.bin":               "tidbyt-gen2.bin",
		"pixoticker_firmware.bin":                "pixoticker.bin",
		"tronbyt-s3_firmware.bin":                "tronbyt-S3.bin",
		"tronbyt-s3-wide_firmware.bin":           "tronbyt-s3-wide.bin",
		"matrixportal-s3_firmware.bin":           "matrixportal-s3.bin",
		"matrixportal-s3-waveshare_firmware.bin": "matrixportal-s3-waveshare.bin",
	}

	count := 0
	for _, asset := range release.Assets {
		localName, ok := mapping[asset.Name]
		if !ok {
			continue
		}

		slog.Info("Downloading firmware", "asset", asset.Name)

		// Use API URL with Accept header to get binary
		dReq, _ := http.NewRequest(http.MethodGet, asset.URL, nil)
		dReq.Header.Set("Accept", "application/octet-stream")
		if token != "" {
			dReq.Header.Set("Authorization", "Bearer "+token)
		}

		dResp, err := client.Do(dReq)
		if err != nil {
			slog.Error("Failed to download firmware asset", "asset", asset.Name, "error", err)
			continue
		}
		defer func() {
			if err := dResp.Body.Close(); err != nil {
				slog.Error("Failed to close asset body", "asset", asset.Name, "error", err)
			}
		}()

		if dResp.StatusCode != http.StatusOK {
			slog.Error("Failed to download firmware asset (bad status)", "asset", asset.Name, "status", dResp.StatusCode)
			continue
		}

		outFile, err := os.Create(filepath.Join(firmwareDir, localName))
		if err != nil {
			slog.Error("Failed to create firmware file", "file", localName, "error", err)
			continue
		}
		if _, err := io.Copy(outFile, dResp.Body); err != nil {
			slog.Error("Failed to write firmware file", "file", localName, "error", err)
		}
		if err := outFile.Close(); err != nil {
			slog.Error("Failed to close firmware file", "file", localName, "error", err)
		}
		count++
	}

	if count > 0 {
		if err := os.WriteFile(versionFile, []byte(release.TagName), 0644); err != nil {
			slog.Error("Failed to write version file", "error", err)
		}
	}

	return nil
}

func (s *Server) handleFirmwareGenerateGet(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)

	// Check firmware availability
	firmwareDir := filepath.Join(s.DataDir, "firmware")
	binsAvailable := false
	version := ""

	if _, err := os.Stat(firmwareDir); err == nil {
		// Check for bin files
		files, _ := os.ReadDir(firmwareDir)
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".bin") {
				binsAvailable = true
				break
			}
		}

		// Check version
		versionBytes, err := os.ReadFile(filepath.Join(firmwareDir, "firmware_version.txt"))
		if err == nil {
			version = strings.TrimSpace(string(versionBytes))
		}
	}

	localizer := s.getLocalizer(r)
	s.renderTemplate(w, r, "firmware", TemplateData{
		User:                  user,
		Device:                device,
		FirmwareBinsAvailable: binsAvailable,
		FirmwareVersion:       version,
		DeviceTypeChoices:     s.getDeviceTypeChoices(localizer),
		Localizer:             localizer,
	})
}

func (s *Server) handleFirmwareGeneratePost(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

	ssid := r.FormValue("wifi_ap")
	password := r.FormValue("wifi_password")
	imgURL := r.FormValue("img_url")
	swapColors := r.FormValue("swap_colors") == "on"

	if ssid == "" || password == "" || imgURL == "" {
		http.Error(w, "Missing fields", http.StatusBadRequest)
		return
	}

	binData, err := firmware.Generate(s.DataDir, device.Type, ssid, password, imgURL, swapColors)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate firmware: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s-firmware.bin\"", device.Name))
	if _, err := w.Write(binData); err != nil {
		slog.Error("Failed to write firmware data to response", "error", err)
		// Log error, but can't change HTTP status after writing headers.
	}
}

func (s *Server) handleTriggerOTA(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

	binName := device.Type.FirmwareFilename(device.SwapColors)
	if binName == "" {
		s.flashAndRedirect(w, r, "OTA not supported for this device type", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
		return
	}

	firmwarePath := filepath.Join(s.DataDir, "firmware", binName)
	if _, err := os.Stat(firmwarePath); os.IsNotExist(err) {
		s.flashAndRedirect(w, r, "Firmware binary not found. Please update firmware binaries in Admin settings.", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
		return
	}

	// Construct URL
	// Using the new /static/firmware/ route
	baseURL := s.GetBaseURL(r)
	// Ensure baseURL has no trailing slash, but /static/firmware/ does
	updateURL := fmt.Sprintf("%s/static/firmware/%s", strings.TrimRight(baseURL, "/"), binName)

	if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(r.Context(), "pending_update_url", updateURL); err != nil {
		slog.Error("Failed to save pending update", "error", err)
		s.flashAndRedirect(w, r, "Internal Error", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
		return
	}
	device.PendingUpdateURL = updateURL

	// Notify Device (to wake up WS loop)
	s.Broadcaster.Notify(device.ID, nil)

	s.flashAndRedirect(w, r, "OTA Update queued. Device should update shortly.", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
}

func (s *Server) GetFirmwareVersion() string {
	versionBytes, err := os.ReadFile(filepath.Join(s.DataDir, "firmware", "firmware_version.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(versionBytes))
}
