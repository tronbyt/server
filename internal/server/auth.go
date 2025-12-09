package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"log/slog"
	"tronbyt-server/internal/auth"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/gitutils"

	"github.com/nicksnyder/go-i18n/v2/i18n"
)

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleLoginGet called")

	// Check session
	session, _ := s.Store.Get(r, "session-name")
	if username, ok := session.Values["username"].(string); ok {
		// Validate user exists in DB
		var user data.User
		if err := s.DB.First(&user, "username = ?", username).Error; err == nil {
			// User exists, redirect to home
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		} else {
			// User not found in DB, invalidate session
			slog.Info("User in session not found in DB, invalidating session", "username", username)
			session.Options.MaxAge = -1 // Expire cookie
			if err := s.saveSession(w, r, session); err != nil {
				slog.Error("Failed to save session after invalidation", "error", err)
			}
			// Fall through to login logic
		}
	}

	// Auto-Login Check
	if s.Config.SingleUserAutoLogin == "1" {
		var count int64
		if err := s.DB.Model(&data.User{}).Count(&count).Error; err == nil && count == 1 {
			if s.isTrustedNetwork(r) {
				var user data.User
				s.DB.First(&user)
				session.Values["username"] = user.Username
				session.Options.MaxAge = 86400 * 30
				if err := s.saveSession(w, r, session); err != nil {
					slog.Error("Failed to save session for auto-login", "error", err)
				}
				slog.Info("Auto-logged in single user from trusted network", "username", user.Username, "ip", s.getRealIP(r))
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
		}
	}

	var count int64
	if err := s.DB.Model(&data.User{}).Count(&count).Error; err != nil {
		slog.Error("Failed to count users", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if count == 0 {
		slog.Info("No users found, redirecting to registration for owner setup")
		http.Redirect(w, r, "/auth/register", http.StatusSeeOther)
		return
	}

	s.renderTemplate(w, r, "login", TemplateData{})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleLoginPost called")
	username := r.FormValue("username")
	password := r.FormValue("password")

	var user data.User
	if err := s.DB.First(&user, "username = ?", username).Error; err != nil {
		slog.Warn("Login failed: user not found", "username", username)
		localizer := s.getLocalizer(r)
		s.renderTemplate(w, r, "login", TemplateData{Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Invalid username or password"})}})
		return
	}

	valid, legacy, err := auth.VerifyPassword(user.Password, password)
	if err != nil {
		slog.Error("Password check error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if !valid {
		slog.Warn("Login failed: invalid password", "username", username)
		localizer := s.getLocalizer(r)
		s.renderTemplate(w, r, "login", TemplateData{Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Invalid username or password"})}})
		return
	}

	// Upgrade password if legacy
	if legacy {
		slog.Info("Upgrading password hash", "username", username)
		newHash, err := auth.HashPassword(password)
		if err == nil {
			s.DB.Model(&user).Update("password", newHash)
		} else {
			slog.Error("Failed to upgrade password hash", "error", err)
		}
	}

	// Login successful
	slog.Info("Login successful", "username", username)
	session, _ := s.Store.Get(r, "session-name")
	session.Values["username"] = user.Username

	if r.FormValue("remember") == "on" {
		session.Options.MaxAge = 86400 * 30
	} else {
		session.Options.MaxAge = 0
	}

	if err := s.saveSession(w, r, session); err != nil {
		slog.Error("Failed to save session", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	delete(session.Values, "username")
	if err := s.saveSession(w, r, session); err != nil {
		slog.Error("Failed to save session on logout", "error", err)
		// Non-fatal, redirect anyway
	}
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

func (s *Server) handleRegisterGet(w http.ResponseWriter, r *http.Request) {
	var count int64
	s.DB.Model(&data.User{}).Count(&count)

	if s.Config.EnableUserRegistration != "1" && count > 0 {
		session, _ := s.Store.Get(r, "session-name")
		currentUsername, ok := session.Values["username"].(string)
		if !ok {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		var user data.User
		if err := s.DB.First(&user, "username = ?", currentUsername).Error; err != nil || !user.IsAdmin {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
	}

	var flashes []string
	if count == 0 {
		localizer := s.getLocalizer(r)
		flashes = append(flashes, localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "System Setup: Please create the 'admin' user."}))
	}

	s.renderTemplate(w, r, "register", TemplateData{Flashes: flashes, UserCount: int(count)})
}

func (s *Server) handleRegisterPost(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")
	email := r.FormValue("email")

	var count int64
	s.DB.Model(&data.User{}).Count(&count)

	localizer := s.getLocalizer(r)

	if s.Config.EnableUserRegistration != "1" && count > 0 {
		session, _ := s.Store.Get(r, "session-name")
		currentUsername, ok := session.Values["username"].(string)
		if !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		var currentUser data.User
		if err := s.DB.First(&currentUser, "username = ?", currentUsername).Error; err != nil || !currentUser.IsAdmin {
			http.Error(w, localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "User registration is not enabled."}), http.StatusForbidden)
			return
		}
	}

	if username == "" || password == "" {
		s.renderTemplate(w, r, "register", TemplateData{Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Username and password required"})}})
		return
	}

	var existing data.User
	if err := s.DB.First(&existing, "username = ?", username).Error; err == nil {
		s.renderTemplate(w, r, "register", TemplateData{Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Username already exists"})}})
		return
	}

	hashedPassword, err := auth.HashPassword(password)
	if err != nil {
		slog.Error("Failed to hash password", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Determine if requester is admin
	var requesterIsAdmin bool
	if count == 0 {
		requesterIsAdmin = true
	} else {
		session, _ := s.Store.Get(r, "session-name")
		if currentUsername, ok := session.Values["username"].(string); ok {
			var currentUser data.User
			if err := s.DB.First(&currentUser, "username = ?", currentUsername).Error; err == nil {
				requesterIsAdmin = currentUser.IsAdmin
			}
		}
	}

	isAdmin := false
	if count == 0 {
		isAdmin = true
	} else if requesterIsAdmin {
		isAdmin = r.FormValue("is_admin") == "on"
	}

	apiKey, _ := generateSecureToken(32)

	newUser := data.User{
		Username: username,
		Password: hashedPassword,
		Email:    email,
		APIKey:   apiKey,
		IsAdmin:  isAdmin,
	}

	if err := s.DB.Create(&newUser).Error; err != nil {
		slog.Error("Failed to create user", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Auto-login the first created user
	if count == 0 {
		session, _ := s.Store.Get(r, "session-name")
		session.Values["username"] = newUser.Username
		if err := s.saveSession(w, r, session); err != nil {
			slog.Error("Failed to save session for auto-login", "error", err)
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleEditUserGet(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Credentials").First(&user, "username = ?", username).Error; err != nil {
		slog.Error("Failed to fetch user for edit", "username", username, "error", err)
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	// Get System Repo Info if admin (Stub for now or implement)	// Python: system_apps.get_system_repo_info
	// I'll leave it empty for now or implement later if critical.
	// Template expects 'system_repo_info' but I don't pass it in TemplateData explicitly,
	// unless I extend TemplateData or pass map.
	// Go TemplateData has User.
	// I need to add fields to TemplateData if I want to pass extra info.
	// 'FirmwareVersion' is there.

	firmwareVersion := "unknown"
	firmwareFile := filepath.Join(s.DataDir, "firmware", "firmware_version.txt")
	if bytes, err := os.ReadFile(firmwareFile); err == nil {
		firmwareVersion = strings.TrimSpace(string(bytes))
	}

	var systemRepoInfo *gitutils.RepoInfo
	if s.Config.SystemAppsRepo != "" {
		path := filepath.Join(s.DataDir, "system-apps")
		info, err := gitutils.GetRepoInfo(path, s.Config.SystemAppsRepo)
		if err != nil {
			slog.Error("Failed to get system repo info", "error", err)
		} else {
			systemRepoInfo = info
		}
	}

	var userRepoInfo *gitutils.RepoInfo
	if user.AppRepoURL != "" {
		path := filepath.Join(s.DataDir, "users", user.Username, "apps")
		info, err := gitutils.GetRepoInfo(path, user.AppRepoURL)
		if err != nil {
			slog.Error("Failed to get user repo info", "error", err)
		} else {
			userRepoInfo = info
		}
	}

	s.renderTemplate(w, r, "edit", TemplateData{
		User:                &user,
		FirmwareVersion:     firmwareVersion,
		SystemRepoInfo:      systemRepoInfo,
		UserRepoInfo:        userRepoInfo,
		GlobalSystemRepoURL: s.Config.SystemAppsRepo,
	})
}

func (s *Server) handleEditUserPost(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	oldPassword := r.FormValue("old_password")
	newPassword := r.FormValue("password")

	if oldPassword != "" && newPassword != "" {
		valid, _, err := auth.VerifyPassword(user.Password, oldPassword)
		if err != nil || !valid {
			localizer := s.getLocalizer(r)
			s.renderTemplate(w, r, "edit", TemplateData{User: &user, Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Invalid old password"})}})
			return
		}

		hash, err := auth.HashPassword(newPassword)
		if err != nil {
			http.Error(w, "Failed to hash password", http.StatusInternalServerError)
			return
		}
		user.Password = hash
		if err := s.DB.Save(&user).Error; err != nil {
			slog.Error("Failed to update password", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		// Flash success?
	}

	http.Redirect(w, r, "/auth/edit", http.StatusSeeOther)
}

func (s *Server) handleGenerateAPIKey(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	apiKey, err := generateSecureToken(32)
	if err != nil {
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	if err := s.DB.Model(&data.User{}).Where("username = ?", username).Update("api_key", apiKey).Error; err != nil {
		slog.Error("Failed to update API key", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/auth/edit", http.StatusSeeOther)
}

func (s *Server) handleSetAPIKey(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	apiKey := r.FormValue("api_key")
	if apiKey == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther) // Should redirect to edit page?
		return
	}

	if err := s.DB.Model(&data.User{}).Where("username = ?", username).Update("api_key", apiKey).Error; err != nil {
		slog.Error("Failed to update API key", "error", err)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) SetupAuthRoutes() {
	s.Router.HandleFunc("GET /auth/login", s.handleLoginGet)
	s.Router.HandleFunc("POST /auth/login", s.handleLoginPost)
	s.Router.HandleFunc("GET /auth/logout", s.handleLogout)
	s.Router.HandleFunc("GET /auth/register", s.handleRegisterGet)
	s.Router.HandleFunc("POST /auth/register", s.handleRegisterPost)
	s.Router.HandleFunc("GET /auth/edit", s.handleEditUserGet)
	s.Router.HandleFunc("POST /auth/edit", s.handleEditUserPost)
	s.Router.HandleFunc("POST /auth/generate_api_key", s.handleGenerateAPIKey)
	s.Router.HandleFunc("POST /auth/set_api_key", s.handleSetAPIKey)

	// WebAuthn
	s.Router.HandleFunc("GET /auth/webauthn/register/begin", s.handleWebAuthnRegisterBegin)
	s.Router.HandleFunc("POST /auth/webauthn/register/finish", s.handleWebAuthnRegisterFinish)
	s.Router.HandleFunc("GET /auth/webauthn/login/begin", s.handleWebAuthnLoginBegin)
	s.Router.HandleFunc("POST /auth/webauthn/login/finish", s.handleWebAuthnLoginFinish)
	s.Router.HandleFunc("POST /auth/webauthn/delete/{id}", s.handleDeleteWebAuthnCredential)
}
