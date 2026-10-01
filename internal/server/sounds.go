package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"tronbyt-server/internal/data"
	"tronbyt-server/web"

	securejoin "github.com/cyphar/filepath-securejoin"
	"gorm.io/gorm"
)

// Allowed sound file extensions for uploads.
var allowedAudioExtensions = []string{".wav", ".mp3", ".ogg", ".m4a"}

// SoundItem represents an available notification sound.
type SoundItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Tone      string `json:"tone"`
	IsDefault bool   `json:"is_default"`
	Size      int64  `json:"size,omitempty"`
}

// DefaultNotificationSounds holds the 5 built-in notification sounds with mapped piezo tone patterns.
// Frequencies in Hz, durations in milliseconds (frequency:duration_ms pairs).
var DefaultNotificationSounds = []SoundItem{
	{
		ID:        "default:chime",
		Name:      "Chime",
		URL:       "/static/sounds/chime.wav",
		Tone:      "523:120,659:120,784:220",
		IsDefault: true,
	},
	{
		ID:        "default:ding",
		Name:      "Ding",
		URL:       "/static/sounds/ding.wav",
		Tone:      "880:350",
		IsDefault: true,
	},
	{
		ID:        "default:bell",
		Name:      "Bell",
		URL:       "/static/sounds/bell.wav",
		Tone:      "587:180,440:320",
		IsDefault: true,
	},
	{
		ID:        "default:pop",
		Name:      "Pop",
		URL:       "/static/sounds/pop.wav",
		Tone:      "600:40,850:60",
		IsDefault: true,
	},
	{
		ID:        "default:alert",
		Name:      "Alert",
		URL:       "/static/sounds/alert.wav",
		Tone:      "784:100,0:50,784:150",
		IsDefault: true,
	},
	{
		ID:        "default:tron",
		Name:      "Tron Bit",
		URL:       "/static/sounds/tron.wav",
		Tone:      "1047:40,0:15,1319:40,0:15,1568:40,0:15,2093:60,0:20,1568:50,0:15,2093:150",
		IsDefault: true,
	},
	{
		ID:        "default:sonar",
		Name:      "Sonar",
		URL:       "/static/sounds/sonar.wav",
		Tone:      "1500:200,0:60,1500:120",
		IsDefault: true,
	},
}

// GetDefaultSounds returns a copy of the default notification sounds.
func GetDefaultSounds() []SoundItem {
	res := make([]SoundItem, len(DefaultNotificationSounds))
	copy(res, DefaultNotificationSounds)
	return res
}

// SoundsDir returns the path to the custom sounds upload directory in DataDir.
func (s *Server) SoundsDir() string {
	return filepath.Join(s.DataDir, "sounds")
}

// EnsureSoundsDir ensures that the custom sounds upload directory exists.
func (s *Server) EnsureSoundsDir() error {
	dir := s.SoundsDir()
	return os.MkdirAll(dir, 0755)
}

// GetCustomSounds returns all custom sound files uploaded to the sounds directory.
func (s *Server) GetCustomSounds() ([]SoundItem, error) {
	dir := s.SoundsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading custom sounds dir: %w", err)
	}

	var items []SoundItem
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if !slices.Contains(allowedAudioExtensions, ext) {
			continue
		}
		info, err := entry.Info()
		var size int64
		if err == nil {
			size = info.Size()
		}
		name := entry.Name()
		items = append(items, SoundItem{
			ID:        "custom:" + name,
			Name:      name,
			URL:       "/static/custom_sounds/" + url.PathEscape(name),
			IsDefault: false,
			Size:      size,
		})
	}
	return items, nil
}

// GetAllAvailableSounds returns both default sounds and uploaded custom sounds.
func (s *Server) GetAllAvailableSounds() []SoundItem {
	all := GetDefaultSounds()
	custom, err := s.GetCustomSounds()
	if err != nil {
		slog.Error("Failed to list custom sounds", "error", err)
		return all
	}
	all = append(all, custom...)
	return all
}

// MapSoundToTone implements Option A piezo fallback:
// Maps app/custom sound keywords to pleasant tone sequences, or defaults to a 2-tone chime.
func MapSoundToTone(soundName string) string {
	lower := strings.ToLower(soundName)
	switch {
	case strings.Contains(lower, "ding"):
		return "880:350"
	case strings.Contains(lower, "chime"):
		return "523:120,659:120,784:220"
	case strings.Contains(lower, "bell"):
		return "587:180,440:320"
	case strings.Contains(lower, "pop"):
		return "600:40,850:60"
	case strings.Contains(lower, "alert"):
		return "784:100,0:50,784:150"
	case strings.Contains(lower, "tron") || strings.Contains(lower, "bit"):
		return "1047:40,0:15,1319:40,0:15,1568:40,0:15,2093:60,0:20,1568:50,0:15,2093:150"
	case strings.Contains(lower, "sonar") || strings.Contains(lower, "ping"):
		return "1500:200,0:60,1500:120"
	default:
		return "659:120,880:180" // Option A pleasant 2-tone chime
	}
}

// findAppDir locates the directory containing an app by its ID, package name, or path.
func (s *Server) findAppDir(appID string) string {
	if appID == "" {
		return ""
	}

	cleanID := filepath.Clean(appID)
	if strings.Contains(cleanID, "..") || filepath.IsAbs(cleanID) {
		return ""
	}

	// 1. Direct path check in DataDir (e.g. "system-apps/apps/nfl_scores")
	directPath, err := securejoin.SecureJoin(s.DataDir, cleanID)
	if err == nil {
		if fi, err := os.Stat(directPath); err == nil && fi.IsDir() {
			return directPath
		}
	}

	// 2. Check system-apps/apps/<baseName>
	baseName := filepath.Base(cleanID)
	sysPath, err := securejoin.SecureJoin(s.DataDir, filepath.Join("system-apps", "apps", baseName))
	if err == nil {
		if fi, err := os.Stat(sysPath); err == nil && fi.IsDir() {
			return sysPath
		}
	}

	// 3. Search systemAppsCache
	s.systemAppsCacheMutex.RLock()
	for _, meta := range s.systemAppsCache {
		if meta.ID == cleanID || meta.PackageName == cleanID || filepath.Base(meta.Path) == cleanID || meta.ID == baseName {
			p, err := securejoin.SecureJoin(s.DataDir, meta.Path)
			if err == nil {
				if fi, err := os.Stat(p); err == nil {
					s.systemAppsCacheMutex.RUnlock()
					if fi.IsDir() {
						return p
					}
					return filepath.Dir(p)
				}
			}
		}
	}
	s.systemAppsCacheMutex.RUnlock()

	// 4. Check DB for matching app
	if s.DB != nil {
		dbApp, err := gorm.G[data.App](s.DB).Where("iname = ? OR name = ?", cleanID, cleanID).First(context.Background())
		if err == nil && dbApp.Path != nil && *dbApp.Path != "" {
			p, err := securejoin.SecureJoin(s.DataDir, *dbApp.Path)
			if err == nil {
				if fi, err := os.Stat(p); err == nil {
					if fi.IsDir() {
						return p
					}
					return filepath.Dir(p)
				}
			}
		}
	}

	// 5. Check user directories in DataDir/users/<username>/apps/<baseName> and .../repo/apps/<baseName>
	usersDir := filepath.Join(s.DataDir, "users")
	if entries, err := os.ReadDir(usersDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			userDir := filepath.Join(usersDir, entry.Name())
			candidate1, err1 := securejoin.SecureJoin(userDir, filepath.Join("apps", baseName))
			if err1 == nil {
				if fi, err := os.Stat(candidate1); err == nil && fi.IsDir() {
					return candidate1
				}
			}
			candidate2, err2 := securejoin.SecureJoin(userDir, filepath.Join("repo", "apps", baseName))
			if err2 == nil {
				if fi, err := os.Stat(candidate2); err == nil && fi.IsDir() {
					return candidate2
				}
			}
		}
	}

	return ""
}

// handleUnifiedSound serves notification audio files.
// Route: GET /sounds/{filename}
// Query params:
//   - appId or app: if provided, serves the sound file from the matching app directory.
//   - if not provided: checks custom sounds directory, then falls back to built-in static sounds.
func (s *Server) handleUnifiedSound(w http.ResponseWriter, r *http.Request) {
	rawFilename := r.PathValue("filename")
	if rawFilename == "" && strings.HasPrefix(r.URL.Path, "/sounds/") {
		rawFilename = strings.TrimPrefix(r.URL.Path, "/sounds/")
	}
	filename := filepath.Base(rawFilename)
	if filename == "" || filename == "." || filename != rawFilename {
		http.NotFound(w, r)
		return
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if !slices.Contains(allowedAudioExtensions, ext) {
		http.Error(w, "Unsupported audio format", http.StatusBadRequest)
		return
	}

	appID := r.PathValue("appId")
	if appID == "" || appID == "app" || appID == "_" {
		appID = r.URL.Query().Get("appId")
		if appID == "" {
			appID = r.URL.Query().Get("app")
		}
	}

	// 1. If appId is provided (and not "default" or "custom"), look in the app's directory
	if appID != "" && appID != "default" && appID != "custom" {
		appDir := s.findAppDir(appID)
		if appDir != "" {
			filePath, err := securejoin.SecureJoin(appDir, filename)
			if err == nil {
				if fi, err := os.Stat(filePath); err == nil && !fi.IsDir() {
					w.Header().Set("Cache-Control", "public, max-age=3600")
					http.ServeFile(w, r, filePath)
					return
				}
			}
		}
	}

	// 2. No appId: check custom uploaded sounds
	customPath, err := securejoin.SecureJoin(s.SoundsDir(), filename)
	if err == nil {
		if fi, err := os.Stat(customPath); err == nil && !fi.IsDir() {
			w.Header().Set("Cache-Control", "public, max-age=3600")
			http.ServeFile(w, r, customPath)
			return
		}
	}

	// 3. Fallback to built-in static sounds embedded in web.Assets
	staticPath := "static/sounds/" + filename
	content, err := web.Assets.ReadFile(staticPath)
	if err == nil {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeContent(w, r, filename, time.Time{}, bytes.NewReader(content))
		return
	}

	http.NotFound(w, r)
}

// ResolveSoundPayload generates the unified sound payload string tailored to the target device's audio capability:
// - AudioCapNone: returns ""
// - AudioCapPiezo: returns tone sequence (e.g. "523:120,659:120,784:220") or Option A keyword/chime fallback.
// - AudioCapFull: returns absolute sound URL for streaming or downloading audio.
func (s *Server) ResolveSoundPayload(device *data.Device, baseURL string, soundID string) string {
	if device == nil || soundID == "" {
		return ""
	}

	audioCap := device.GetAudioCapability()
	if audioCap == data.AudioCapNone {
		return ""
	}

	trimmedBase := strings.TrimRight(baseURL, "/")

	cleanID := strings.TrimSpace(soundID)
	// Reject CRLF injection attempts
	if strings.ContainsAny(cleanID, "\r\n") {
		return ""
	}

	// Check if soundID is an external URL
	if strings.HasPrefix(cleanID, "http://") || strings.HasPrefix(cleanID, "https://") {
		if audioCap == data.AudioCapFull {
			parsedURL, err := url.Parse(cleanID)
			if err == nil && parsedURL.Host != "" && (parsedURL.Scheme == "http" || parsedURL.Scheme == "https") {
				return parsedURL.String()
			}
		}
		// Piezo buzzers cannot stream or decode arbitrary external audio URLs
		return ""
	}

	// Match default sound by ID (e.g. "default:chime") or name (e.g. "chime")
	for _, def := range DefaultNotificationSounds {
		if cleanID == def.ID || strings.EqualFold(cleanID, def.Name) || cleanID == strings.TrimPrefix(def.ID, "default:") {
			if audioCap == data.AudioCapPiezo {
				return def.Tone
			}
			return trimmedBase + def.URL
		}
	}

	// If device is piezo and soundID looks like raw frequency:duration tone string, pass through
	if audioCap == data.AudioCapPiezo && strings.Contains(cleanID, ":") && !strings.Contains(cleanID, "/") && !strings.HasPrefix(cleanID, "app:") && !strings.HasPrefix(cleanID, "default:") && !strings.HasPrefix(cleanID, "custom:") {
		return cleanID
	}

	// Handle App Sound: "app:<app_id>:<filename>" or "app:<filename>"
	if remainder, ok := strings.CutPrefix(cleanID, "app:"); ok {
		var appID, filename string
		if strings.Contains(remainder, ":") {
			parts := strings.SplitN(remainder, ":", 2)
			appID = parts[0]
			filename = filepath.Base(parts[1])
		} else if strings.Contains(remainder, "/") {
			parts := strings.SplitN(remainder, "/", 2)
			appID = parts[0]
			filename = filepath.Base(parts[1])
		} else {
			filename = filepath.Base(remainder)
		}

		if audioCap == data.AudioCapPiezo {
			// Option A: Smart keyword tone mapping with pleasant 2-tone fallback
			return MapSoundToTone(filename)
		}

		if audioCap == data.AudioCapFull {
			if appID != "" {
				return fmt.Sprintf("%s/sounds/%s/%s", trimmedBase, url.PathEscape(appID), url.PathEscape(filename))
			}
			return fmt.Sprintf("%s/sounds/app/%s", trimmedBase, url.PathEscape(filename))
		}
		return ""
	}

	// Match custom sound (e.g. "custom:doorbell.mp3" or "doorbell.mp3")
	customFilename := strings.TrimPrefix(cleanID, "custom:")
	if audioCap == data.AudioCapFull {
		customPath, err := securejoin.SecureJoin(s.SoundsDir(), customFilename)
		if err == nil {
			if _, err := os.Stat(customPath); err == nil {
				return fmt.Sprintf("%s/static/custom_sounds/%s", trimmedBase, url.PathEscape(customFilename))
			}
		}
	}

	return ""
}

// TriggerDeviceSound dispatches a sound notification to a device based on its capability.
// Sends immediately to WebSocket connections and queues into PendingSound for HTTP polling devices.
func (s *Server) TriggerDeviceSound(ctx context.Context, device *data.Device, soundID string, baseURL string) error {
	if device == nil || soundID == "" {
		return nil
	}

	if device.IsMuted() {
		slog.Debug("Skipping sound trigger: device is muted", "device", device.ID)
		return nil
	}

	payload := s.ResolveSoundPayload(device, baseURL, soundID)
	if payload == "" {
		slog.Debug("No sound payload generated for device", "device", device.ID, "capability", device.GetAudioCapability(), "sound", soundID)
		return nil
	}

	// 1. Update in-memory struct immediately so currently running HTTP response handlers see it
	device.PendingSound = payload

	// 2. Update PendingSound in DB for HTTP polling devices
	if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(ctx, "pending_sound", payload); err != nil {
		slog.Error("Failed to set pending_sound", "device", device.ID, "error", err)
	}

	// 3. Broadcast to connected WebSocket devices
	wsMsg, err := json.Marshal(map[string]any{
		"type":  "sound",
		"sound": payload,
	})
	if err != nil {
		return fmt.Errorf("marshal sound ws message: %w", err)
	}

	s.Broadcaster.Notify(device.ID, DeviceCommandMessage{Payload: wsMsg})
	return nil
}
