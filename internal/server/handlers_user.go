package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/gitutils"
	"tronbyt-server/internal/legacy"

	"gorm.io/gorm"
)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleIndex called")
	user := GetUser(r)

	devicesWithUI := make([]DeviceWithUIScale, 0, len(user.Devices))

	for i := range user.Devices {
		device := &user.Devices[i]
		slog.Debug("handleIndex device", "id", device.ID, "apps_count", len(device.Apps))

		// Sort Apps
		sort.Slice(device.Apps, func(i, j int) bool {
			return device.Apps[i].Order < device.Apps[j].Order
		})

		// Calculate UI Brightness
		var customScale map[int]int
		if device.CustomBrightnessScale != "" {
			customScale = data.ParseCustomBrightnessScale(device.CustomBrightnessScale)
		}
		bUI := device.Brightness.UIScale(customScale)

		devicesWithUI = append(devicesWithUI, DeviceWithUIScale{
			Device:       device,
			BrightnessUI: bUI,
		})
	}

	s.renderTemplate(w, r, "index", TemplateData{User: user, DevicesWithUIScales: devicesWithUI})
}

func (s *Server) handleAdminIndex(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user.Username != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var users []data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").Find(&users).Error; err != nil {
		slog.Error("Failed to list users", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Sort Apps for each device
	for i := range users {
		for j := range users[i].Devices {
			dev := &users[i].Devices[j]
			sort.Slice(dev.Apps, func(a, b int) bool {
				return dev.Apps[a].Order < dev.Apps[b].Order
			})
		}
	}

	// We need to inject the current admin user into TemplateData for the header/nav
	var adminUser data.User
	for _, u := range users {
		if u.Username == "admin" {
			adminUser = u
			break
		}
	}

	s.renderTemplate(w, r, "adminindex", TemplateData{User: &adminUser, Users: users})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	targetUsername := r.PathValue("username")
	user := GetUser(r)
	if user.Username != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if targetUsername == user.Username {
		http.Error(w, "Cannot delete yourself", http.StatusBadRequest)
		return
	}

	var targetUser data.User
	if err := s.DB.Preload("Devices").First(&targetUser, "username = ?", targetUsername).Error; err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// Clean up files
	for _, d := range targetUser.Devices {
		deviceWebpDir, err := s.ensureDeviceImageDir(d.ID)
		if err != nil {
			slog.Error("Failed to get device webp directory for deletion", "device_id", d.ID, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		if err := os.RemoveAll(deviceWebpDir); err != nil {
			slog.Error("Failed to remove device webp directory", "device_id", d.ID, "error", err)
		}
	}
	userAppsDir := filepath.Join(s.DataDir, "users", targetUsername)
	if err := os.RemoveAll(userAppsDir); err != nil {
		slog.Error("Failed to remove user apps directory", "username", targetUsername, "error", err)
	}

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Delete Apps for all user's devices
		var deviceIDs []string
		for _, d := range targetUser.Devices {
			deviceIDs = append(deviceIDs, d.ID)
		}
		if len(deviceIDs) > 0 {
			if err := tx.Where("device_id IN ?", deviceIDs).Delete(&data.App{}).Error; err != nil {
				return err
			}
		}

		// 2. Delete Devices
		if err := tx.Where("username = ?", targetUsername).Delete(&data.Device{}).Error; err != nil {
			return err
		}

		// 3. Delete Credentials
		if err := tx.Where("user_id = ?", targetUsername).Delete(&data.WebAuthnCredential{}).Error; err != nil {
			return err
		}

		// 4. Delete User
		if err := tx.Delete(&targetUser).Error; err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		slog.Error("Failed to delete user", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleSetThemePreference(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)

	theme := r.FormValue("theme")
	if theme == "" {
		http.Error(w, "Theme required", http.StatusBadRequest)
		return
	}

	if err := s.DB.Model(&data.User{}).Where("username = ?", user.Username).Update("theme_preference", theme).Error; err != nil {
		slog.Error("Failed to update theme preference", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleSetUserRepo(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)

	repoURL := r.FormValue("app_repo_url")
	if err := s.DB.Model(&data.User{}).Where("username = ?", user.Username).Update("app_repo_url", repoURL).Error; err != nil {
		slog.Error("Failed to update user repo URL", "error", err)
		s.flashAndRedirect(w, r, "Failed to update repository URL.", "/auth/edit", http.StatusSeeOther)
		return
	}

	appsPath := filepath.Join(s.DataDir, "users", user.Username, "repo")
	if repoURL == "" {
		if err := os.RemoveAll(appsPath); err != nil {
			slog.Error("Failed to remove user repo directory", "error", err)
			s.flashAndRedirect(w, r, "Failed to remove user repository. Check server logs.", "/auth/edit", http.StatusSeeOther)
			return
		}
	} else {
		if err := gitutils.EnsureRepo(appsPath, repoURL, s.Config.GitHubToken, true); err != nil {
			slog.Error("Failed to sync user repo", "error", err)
			s.flashAndRedirect(w, r, "Failed to sync user repository. Check server logs.", "/auth/edit", http.StatusSeeOther)
			return
		}
	}

	s.flashAndRedirect(w, r, "User repository updated successfully.", "/auth/edit", http.StatusSeeOther)
}

func (s *Server) handleRefreshUserRepo(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)

	if user.AppRepoURL != "" {
		appsPath := filepath.Join(s.DataDir, "users", user.Username, "repo")
		if err := gitutils.EnsureRepo(appsPath, user.AppRepoURL, s.Config.GitHubToken, true); err != nil {
			slog.Error("Failed to refresh user repo", "error", err)
			s.flashAndRedirect(w, r, "Failed to refresh user repository. Check server logs.", "/auth/edit", http.StatusSeeOther)
			return
		}
	}

	s.flashAndRedirect(w, r, "User repository refreshed successfully.", "/auth/edit", http.StatusSeeOther)
}

func (s *Server) handleExportUserConfig(w http.ResponseWriter, r *http.Request) {
	userContext := GetUser(r)

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").Preload("Credentials").First(&user, "username = ?", userContext.Username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	// Scrub sensitive data
	user.Password = ""
	user.APIKey = ""

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s_config.json", user.Username))

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(user); err != nil {
		slog.Error("Failed to export user config", "error", err)
	}
}

func (s *Server) handleImportUserConfig(w http.ResponseWriter, r *http.Request) {
	userContext := GetUser(r)

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File required", http.StatusBadRequest)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Error("Failed to close uploaded config file", "error", err)
		}
	}()

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		slog.Error("Failed to read uploaded file", "error", err)
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}

	var importedUser data.User

	// Try standard format first
	errStandard := json.Unmarshal(content, &importedUser)
	if errStandard != nil || importedUser.Username == "" {
		// Try legacy format
		var legacyUser legacy.LegacyUser
		if errLegacy := json.Unmarshal(content, &legacyUser); errLegacy == nil && legacyUser.Username != "" {
			slog.Info("Detected legacy user config format")
			// Map LegacyUser to data.User
			importedUser = legacyUser.ToDataUser()
		} else {
			// Both failed
			slog.Error("Failed to decode imported user JSON (both standard and legacy)", "standard_error", errStandard, "legacy_error", errLegacy)
			s.flashAndRedirect(w, r, "Invalid JSON file or unsupported format.", "/auth/edit", http.StatusSeeOther)
			return
		}
	}

	var currentUser data.User
	if err := s.DB.Preload("Devices").First(&currentUser, "username = ?", userContext.Username).Error; err != nil {
		slog.Error("User not found during import", "username", userContext.Username, "error", err)
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	// Update fields
	currentUser.Email = importedUser.Email
	currentUser.ThemePreference = importedUser.ThemePreference
	currentUser.SystemRepoURL = importedUser.SystemRepoURL
	currentUser.AppRepoURL = importedUser.AppRepoURL
	if importedUser.APIKey != "" {
		currentUser.APIKey = importedUser.APIKey
	}

	// Clean up files for all existing devices
	for _, d := range currentUser.Devices {
		deviceWebpDir, err := s.ensureDeviceImageDir(d.ID)
		if err != nil {
			slog.Error("Failed to get device webp directory for cleanup", "device_id", d.ID, "error", err)
			s.flashAndRedirect(w, r, "Import failed: internal server error.", "/auth/edit", http.StatusSeeOther)
			return
		}
		if err := os.RemoveAll(deviceWebpDir); err != nil {
			slog.Error("Failed to remove device webp directory", "device_id", d.ID, "error", err)
		}
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		// Delete existing devices and apps
		var deviceIDs []string
		for _, d := range currentUser.Devices {
			deviceIDs = append(deviceIDs, d.ID)
		}
		if len(deviceIDs) > 0 {
			if err := tx.Where("device_id IN ?", deviceIDs).Delete(&data.App{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", deviceIDs).Delete(&data.Device{}).Error; err != nil {
				return err
			}
		}

		if err := tx.Save(&currentUser).Error; err != nil {
			return err
		}

		for _, dev := range importedUser.Devices {
			dev.Username = currentUser.Username
			// Reset App IDs so the DB generates new ones (prevents sequence desync)
			for i := range dev.Apps {
				dev.Apps[i].ID = 0
			}

			if err := tx.Create(&dev).Error; err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		slog.Error("Import failed", "error", err)
		s.flashAndRedirect(w, r, "Import failed. Check server logs.", "/auth/edit", http.StatusSeeOther)
		return
	}

	s.flashAndRedirect(w, r, "Configuration imported successfully.", "/auth/edit", http.StatusSeeOther)
}

func (s *Server) handleSetSystemRepo(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user.Username != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	repoURL := r.FormValue("app_repo_url")
	if repoURL == "" {
		repoURL = s.Config.SystemAppsRepo
	}

	// Save to global setting
	if err := s.setSetting("system_apps_repo", repoURL); err != nil {
		slog.Error("Failed to save system repo setting", "error", err)
	}

	// Update in-memory config
	s.Config.SystemAppsRepo = repoURL

	if err := s.refreshSystemRepo(); err != nil {
		slog.Error("Failed to update system repo", "error", err)
	}

	http.Redirect(w, r, "/auth/edit", http.StatusSeeOther)
}

func (s *Server) handleRefreshSystemRepo(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user.Username != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := s.refreshSystemRepo(); err != nil {
		slog.Error("Failed to refresh system repo", "error", err)
	}

	if r.Header.Get("Accept") == "application/json" {
		repoURL := s.Config.SystemAppsRepo
		appsPath := filepath.Join(s.DataDir, "system-apps")
		repoInfo, err := gitutils.GetRepoInfo(appsPath, repoURL)
		if err != nil {
			slog.Error("Failed to get repo info", "error", err)
			http.Error(w, "Failed to get repository information", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(repoInfo); err != nil {
			slog.Error("Failed to encode repo info", "error", err)
		}
		return
	}

	http.Redirect(w, r, "/auth/edit", http.StatusSeeOther)
}
