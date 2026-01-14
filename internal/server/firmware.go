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

	// Cleanup old custom firmware uploads
	if files, err := os.ReadDir(firmwareDir); err == nil {
		for _, f := range files {
			if strings.HasPrefix(f.Name(), "custom_") {
				info, err := f.Info()
				if err != nil {
					slog.Warn("Failed to get file info for custom firmware", "file", f.Name(), "error", err)
					continue
				}
				// Cleanup files older than 24 hours
				if time.Since(info.ModTime()) > 24*time.Hour {
					if err := os.Remove(filepath.Join(firmwareDir, f.Name())); err != nil {
						slog.Warn("Failed to delete old custom firmware", "file", f.Name(), "error", err)
					}
				}
			}
		}
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
		// OTA firmware binaries (app only, flashable at 0x10000)
		"tidbyt-gen1_firmware.bin":               "tidbyt-gen1.bin",
		"tidbyt-gen1_swap_firmware.bin":          "tidbyt-gen1_swap.bin",
		"tidbyt-gen2_firmware.bin":               "tidbyt-gen2.bin",
		"pixoticker_firmware.bin":                "pixoticker.bin",
		"tronbyt-s3_firmware.bin":                "tronbyt-S3.bin",
		"tronbyt-s3-wide_firmware.bin":           "tronbyt-s3-wide.bin",
		"matrixportal-s3_firmware.bin":           "matrixportal-s3.bin",
		"matrixportal-s3-waveshare_firmware.bin": "matrixportal-s3-waveshare.bin",
		// Merged binaries (bootloader + partition + app, flashable at 0x0)
		"tidbyt-gen1_merged.bin":               "tidbyt-gen1_merged.bin",
		"tidbyt-gen1_swap_merged.bin":          "tidbyt-gen1_swap_merged.bin",
		"tidbyt-gen2_merged.bin":               "tidbyt-gen2_merged.bin",
		"pixoticker_merged.bin":                "pixoticker_merged.bin",
		"tronbyt-s3_merged.bin":                "tronbyt-S3_merged.bin",
		"tronbyt-s3-wide_merged.bin":           "tronbyt-s3-wide_merged.bin",
		"matrixportal-s3_merged.bin":           "matrixportal-s3_merged.bin",
		"matrixportal-s3-waveshare_merged.bin": "matrixportal-s3-waveshare_merged.bin",
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
	merged := r.FormValue("merged") == "on"

	if ssid == "" || password == "" || imgURL == "" {
		http.Error(w, "Missing fields", http.StatusBadRequest)
		return
	}

	var binData []byte
	var err error
	var filename string

	if merged {
		binData, err = firmware.GenerateMerged(s.DataDir, device.Type, ssid, password, imgURL, swapColors)
		filename = fmt.Sprintf("%s-merged.bin", device.Name)
	} else {
		binData, err = firmware.Generate(s.DataDir, device.Type, ssid, password, imgURL, swapColors)
		filename = fmt.Sprintf("%s-firmware.bin", device.Name)
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate firmware: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	if _, err := w.Write(binData); err != nil {
		slog.Error("Failed to write firmware data to response", "error", err)
		// Log error, but can't change HTTP status after writing headers.
	}
}

func (s *Server) handleTriggerOTA(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

	var updateURL string

	// Check if a file was uploaded
	if err := r.ParseMultipartForm(32 << 20); err == nil { // 32 MB max memory
		file, _, err := r.FormFile("firmware_file")
		if err == nil {
			defer func() {
				if err := file.Close(); err != nil {
					slog.Error("Failed to close uploaded firmware file", "error", err)
				}
			}()

			// Save to temp file in firmware dir
			firmwareDir := filepath.Join(s.DataDir, "firmware")
			if err := os.MkdirAll(firmwareDir, 0755); err != nil {
				slog.Error("Failed to create firmware dir", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			// Use a random filename to avoid conflicts and caching
			randomStr, err := generateSecureToken(8)
			if err != nil {
				slog.Error("Failed to generate secure token for firmware filename", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			tempFilename := fmt.Sprintf("custom_%s_%s.bin", device.ID, randomStr)
			tempFilePath := filepath.Join(firmwareDir, tempFilename)

			out, err := os.Create(tempFilePath)
			if err != nil {
				slog.Error("Failed to create temp firmware file", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			defer func() {
				if err := out.Close(); err != nil {
					slog.Error("Failed to close temp firmware file", "error", err)
				}
			}()

			if _, err := io.Copy(out, file); err != nil {
				slog.Error("Failed to save uploaded firmware", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			baseURL := s.GetBaseURL(r)
			updateURL = fmt.Sprintf("%s/static/firmware/%s", strings.TrimRight(baseURL, "/"), tempFilename)
			slog.Info("Custom firmware uploaded", "device", device.ID, "url", updateURL)
		}
	}

	if updateURL == "" {
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
		updateURL = fmt.Sprintf("%s/static/firmware/%s", strings.TrimRight(baseURL, "/"), binName)
	}

	if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(r.Context(), "pending_update_url", updateURL); err != nil {
		slog.Error("Failed to save pending update", "error", err)
		s.flashAndRedirect(w, r, "Internal Error", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
		return
	}
	device.PendingUpdateURL = updateURL

	// Notify Device (to wake up WS loop)
	payload := map[string]string{"ota_url": updateURL}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to marshal OTA payload", "error", err)
		s.flashAndRedirect(w, r, "Internal Error", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
		return
	}
	s.Broadcaster.Notify(device.ID, DeviceCommandMessage{Payload: jsonPayload})

	s.flashAndRedirect(w, r, "OTA Update queued. Device should update shortly.", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
}

func (s *Server) GetFirmwareVersion() string {
	versionBytes, err := os.ReadFile(filepath.Join(s.DataDir, "firmware", "firmware_version.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(versionBytes))
}
