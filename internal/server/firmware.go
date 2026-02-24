package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/firmware"

	"log/slog"

	securejoin "github.com/cyphar/filepath-securejoin"

	"golang.org/x/mod/semver"

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

	// Read stored ETag
	var storedETag string
	if val, err := s.getSetting("firmware_releases_etag"); err == nil {
		storedETag = val
	}

	// Fetch last 5 releases
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=5", owner, repo)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	if storedETag != "" {
		req.Header.Set("If-None-Match", storedETag)
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

	if resp.StatusCode == http.StatusNotModified {
		slog.Info("Firmware releases not modified (ETag match)")
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		// If we hit a rate limit (403) or other error, check if we have any cached firmware.
		// If so, we can proceed without failing completely.
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			releasesDir := filepath.Join(s.DataDir, "firmware", "releases")
			if entries, err := os.ReadDir(releasesDir); err == nil && len(entries) > 0 {
				slog.Warn("GitHub API rate limit exceeded. Using cached firmware releases.", "count", len(entries))
				return nil
			}
		}
		return fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	// Save new ETag
	newETag := resp.Header.Get("ETag")
	if newETag != "" {
		if err := s.setSetting("firmware_releases_etag", newETag); err != nil {
			slog.Error("Failed to save firmware ETag setting", "error", err)
		}
	}

	var releases []struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"url"` // API URL
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return err
	}

	baseFirmwareDir := filepath.Join(s.DataDir, "firmware")
	releasesDir := filepath.Join(baseFirmwareDir, "releases")
	if err := os.MkdirAll(releasesDir, 0755); err != nil {
		return err
	}

	// Cleanup old custom firmware uploads
	if files, err := os.ReadDir(baseFirmwareDir); err == nil {
		for _, f := range files {
			if strings.HasPrefix(f.Name(), "custom_") {
				info, err := f.Info()
				if err != nil {
					slog.Warn("Failed to get file info for custom firmware", "file", f.Name(), "error", err)
					continue
				}
				// Cleanup files older than 24 hours
				if time.Since(info.ModTime()) > 24*time.Hour {
					if err := os.Remove(filepath.Join(baseFirmwareDir, f.Name())); err != nil {
						slog.Warn("Failed to delete old custom firmware", "file", f.Name(), "error", err)
					}
				}
			}
		}
	}

	// Mapping of asset names to local names
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
		"tidbyt-gen1_merged.bin":     "tidbyt-gen1_merged.bin",
		"tronbyt-s3_merged.bin":      "tronbyt-S3_merged.bin",
		"matrixportal-s3_merged.bin": "matrixportal-s3_merged.bin",
	}

	for _, release := range releases {
		versionDir := filepath.Join(releasesDir, release.TagName)
		if err := os.MkdirAll(versionDir, 0755); err != nil {
			slog.Error("Failed to create version dir", "version", release.TagName, "error", err)
			continue
		}

		// Iterate over assets and download them if they are missing or empty.

		for _, asset := range release.Assets {
			localName, ok := mapping[asset.Name]
			if !ok {
				continue
			}

			localPath := filepath.Join(versionDir, localName)
			if info, err := os.Stat(localPath); err == nil && info.Size() > 0 {
				continue // Already exists and is not empty
			}

			slog.Info("Downloading firmware asset", "version", release.TagName, "asset", asset.Name)

			dReq, err := http.NewRequest(http.MethodGet, asset.URL, nil)
			if err != nil {
				slog.Error("Failed to create firmware download request", "asset", asset.Name, "error", err)
				continue
			}
			dReq.Header.Set("Accept", "application/octet-stream")
			if token != "" {
				dReq.Header.Set("Authorization", "Bearer "+token)
			}

			dResp, err := client.Do(dReq)
			if err != nil {
				slog.Error("Failed to download firmware asset", "asset", asset.Name, "error", err)
				continue
			}

			// Closure to handle defer properly in loop
			func() {
				defer func() {
					if err := dResp.Body.Close(); err != nil {
						slog.Error("Failed to close response body", "asset", asset.Name, "error", err)
					}
				}()
				if dResp.StatusCode != http.StatusOK {
					if dResp.StatusCode == http.StatusForbidden {
						slog.Warn("Failed to download firmware asset (rate limit)", "asset", asset.Name, "status", dResp.StatusCode)
					} else {
						slog.Error("Failed to download firmware asset (bad status)", "asset", asset.Name, "status", dResp.StatusCode)
					}
					return
				}

				tempPath := localPath + ".tmp"
				outFile, err := os.Create(tempPath)
				if err != nil {
					slog.Error("Failed to create temp firmware file", "file", tempPath, "error", err)
					return
				}

				if _, err := io.Copy(outFile, dResp.Body); err != nil {
					_ = outFile.Close()
					_ = os.Remove(tempPath)
					slog.Error("Failed to write firmware file", "file", localPath, "error", err)
					return
				}
				if err := outFile.Close(); err != nil {
					_ = os.Remove(tempPath)
					slog.Error("Failed to close temp firmware file", "file", tempPath, "error", err)
					return
				}

				if err := os.Rename(tempPath, localPath); err != nil {
					slog.Error("Failed to rename firmware file", "from", tempPath, "to", localPath, "error", err)
					_ = os.Remove(tempPath)
				}
			}()
		}
	}

	// Update version file to point to the latest tag
	return nil
}

func (s *Server) GetAvailableFirmwareVersions() []string {
	releasesDir := filepath.Join(s.DataDir, "firmware", "releases")
	entries, err := os.ReadDir(releasesDir)
	if err != nil {
		return nil
	}

	var versions []string
	for _, entry := range entries {
		if entry.IsDir() {
			versions = append(versions, entry.Name())
		}
	}

	slices.SortFunc(versions, func(a, b string) int {
		return semver.Compare(b, a) // Descending order
	})

	return versions
}

func (s *Server) handleFirmwareGenerateGet(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)

	// Check firmware availability
	// Default to latest
	version := s.GetLatestFirmwareVersion()
	binsAvailable := false

	if version != "" {
		firmwareDir := filepath.Join(s.DataDir, "firmware", "releases", version)
		if _, err := os.Stat(firmwareDir); err == nil {
			binsAvailable = true
		}
	}

	localizer := s.getLocalizer(r)
	// Check if URL contains localhost
	imgURL := device.ImgURL
	var urlWarning string
	if strings.Contains(imgURL, "localhost") || strings.Contains(imgURL, "127.0.0.1") {
		urlWarning = "localhost"
	}
	s.renderTemplate(w, r, "firmware", TemplateData{
		User:                      user,
		Device:                    device,
		FirmwareBinsAvailable:     binsAvailable,
		FirmwareVersion:           version,
		AvailableFirmwareVersions: s.GetAvailableFirmwareVersions(),
		DeviceTypeChoices:         s.getDeviceTypeChoices(localizer),
		Localizer:                 localizer,
		URLWarning:                urlWarning,
	})
}

func (s *Server) handleFirmwareGeneratePost(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

	ssid := r.FormValue("wifi_ap")
	password := r.FormValue("wifi_password")
	imgURL := r.FormValue("img_url")
	swapColors := r.FormValue("swap_colors") == "on"
	otaOnly := r.FormValue("ota_only") == "on"
	version := r.FormValue("version")

	if ssid == "" || password == "" || imgURL == "" {
		http.Error(w, "Missing fields", http.StatusBadRequest)
		return
	}

	if version == "" {
		version = s.GetLatestFirmwareVersion() // Default to latest
	}

	if version == "" {
		http.Error(w, "No firmware versions available", http.StatusInternalServerError)
		return
	}
	firmwareDir, err := securejoin.SecureJoin(filepath.Join(s.DataDir, "firmware", "releases"), version)
	if err != nil {
		slog.Error("Failed to resolve firmware directory", "error", err)
		http.Error(w, "Invalid version", http.StatusBadRequest)
		return
	}

	var binData []byte
	var filename string

	if otaOnly {
		binData, err = firmware.Generate(firmwareDir, device.Type, ssid, password, imgURL, swapColors)
		filename = fmt.Sprintf("%s-firmware.bin", device.Name)
	} else {
		binData, err = firmware.GenerateMerged(firmwareDir, device.Type, ssid, password, imgURL, swapColors)
		filename = fmt.Sprintf("%s-merged.bin", device.Name)
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate firmware: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	if _, err := w.Write(binData); err != nil {
		slog.Error("Failed to write firmware data to response", "error", err)
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
		version := r.FormValue("version")
		if version == "" {
			version = s.GetLatestFirmwareVersion() // Default to latest
		}

		if version == "" {
			s.flashAndRedirect(w, r, "No firmware available.", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
			return
		}

		binName := device.Type.FirmwareFilename(device.SwapColors)
		if binName == "" {
			s.flashAndRedirect(w, r, "OTA not supported for this device type", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
			return
		}

		// Check if file exists in the versioned directory
		firmwareBase := filepath.Join(s.DataDir, "firmware", "releases")
		versionDir, err := securejoin.SecureJoin(firmwareBase, version)
		if err != nil {
			slog.Error("Failed to resolve firmware version directory", "version", version, "error", err)
			s.flashAndRedirect(w, r, "Invalid firmware version.", fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
			return
		}

		firmwarePath := filepath.Join(versionDir, binName)
		if _, err := os.Stat(firmwarePath); os.IsNotExist(err) {
			s.flashAndRedirect(w, r, fmt.Sprintf("Firmware binary not found for version %s.", version), fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
			return
		}

		// Construct URL
		// We need to serve this file via HTTP.
		// Existing /static/firmware/ maps to DataDir/firmware.
		// So we can access it via /static/firmware/releases/<version>/<binName>
		baseURL := s.GetBaseURL(r)
		updateURL = fmt.Sprintf("%s/static/firmware/releases/%s/%s", strings.TrimRight(baseURL, "/"), url.PathEscape(version), url.PathEscape(binName))
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

func (s *Server) GetLatestFirmwareVersion() string {
	versions := s.GetAvailableFirmwareVersions()
	if len(versions) > 0 {
		return versions[0]
	}
	return ""
}
