package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"tronbyt-server/internal/chunker"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/providers"
	"tronbyt-server/internal/renderer"
	"tronbyt-server/web"

	securejoin "github.com/cyphar/filepath-securejoin"
	"gorm.io/gorm"
)

// RenderApp consolidates the logic for rendering an app for a device.
// It handles config overrides, timezone/locale injection, dwell time, and filters.
func (s *Server) RenderApp(ctx context.Context, device *data.Device, app *data.App, appPath string, configOverrides map[string]any) ([]byte, []string, error) {
	// Config
	var config map[string]any
	switch {
	case configOverrides != nil:
		config = configOverrides
	case app != nil && app.Config != nil:
		config = maps.Clone(app.Config)
	default:
		config = make(map[string]any)
	}

	// Timezone & Locale
	var deviceTimezone string
	var locale *string
	supports2x := false
	width, height := data.DefaultCanvasWidth, data.DefaultCanvasHeight

	if device != nil {
		deviceTimezone = device.GetTimezone()
		// The legacy "$tz" config variable should eventually be removed.
		// The system apps already use `time.tz()`, but users' custom apps might not.
		config["$tz"] = deviceTimezone
		locale = device.Locale
		supports2x = device.Type.Supports2x()
		width, height = device.Type.CanvasSize()
	}

	// Dwell Time
	var appInterval int
	if device != nil {
		appInterval = device.GetEffectiveDwellTime(app)
	} else {
		appInterval = 15 // Default fallback if no device context
	}

	// Filters
	var filters []string
	if device != nil {
		filters = s.getEffectiveFilters(device, app)
	}

	// App-level rendering overrides are optional when rendering previews.
	var showFullAnimation *bool
	if app != nil {
		showFullAnimation = app.ShowFullAnimation
	}

	// 2. Static WebP App
	if strings.HasSuffix(strings.ToLower(appPath), ".webp") {
		content, err := os.ReadFile(appPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read static webp app: %w", err)
		}
		return content, nil, nil
	}

	return renderer.Render(
		ctx,
		appPath,
		config,
		width, height,
		time.Duration(appInterval)*time.Second,
		30*time.Second,
		true,
		supports2x,
		&deviceTimezone,
		locale,
		filters,
		showFullAnimation,
	)
}

// possiblyRender executes background rendering for an app if its interval has elapsed or content has changed.
func (s *Server) possiblyRender(ctx context.Context, app *data.App, device *data.Device, user *data.User) bool {
	// 1. Pushed App (Pre-rendered)
	if app.Pushed {
		if app.AutoPin {
			s.handleAutoPin(ctx, app, device, user, true)
		}
		return true
	}

	if app.Path == nil || *app.Path == "" {
		return false
	}

	appBasename := fmt.Sprintf("%s-%s", app.Name, app.Iname)

	// External Provider App (e.g. Screeny)
	if provider, appID, ok := s.parseExternalAppPath(*app.Path); ok {
		appKey := fmt.Sprintf("%s:%s", device.ID, app.Iname)
		s.inFlightRendersMu.Lock()
		if s.inFlightRenders == nil {
			s.inFlightRenders = make(map[string]chan struct{})
		}
		if s.renderPending == nil {
			s.renderPending = make(map[string]bool)
		}
		if s.appRenderServed == nil {
			s.appRenderServed = make(map[string]bool)
		}

		if waitCh, inFlight := s.inFlightRenders[appKey]; inFlight {
			s.inFlightRendersMu.Unlock()
			slog.Debug("Render already in flight for external app, waiting for completion", "app", appBasename)
			select {
			case <-waitCh:
			case <-ctx.Done():
			}
			return len(s.getAppChunks(device.ID, app)) > 0
		}
		doneCh := make(chan struct{})
		s.inFlightRenders[appKey] = doneCh
		alreadyServed := s.appRenderServed[appKey]
		s.inFlightRendersMu.Unlock()
		defer func() {
			s.inFlightRendersMu.Lock()
			delete(s.inFlightRenders, appKey)
			close(doneCh)
			isPending := s.renderPending[appKey]
			delete(s.renderPending, appKey)
			s.inFlightRendersMu.Unlock()

			if isPending {
				s.backgroundRenders.Go(func() {
					bgCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
					defer cancel()
					if latestApp, err := gorm.G[data.App](s.DB).Where("id = ?", app.ID).First(bgCtx); err == nil {
						s.possiblyRender(bgCtx, &latestApp, device, user)
					}
				})
			}
		}()

		forceRender := s.filtersChangedSinceLastRender(device, app) || app.LastRender.IsZero()
		dwellSecs := 15
		if device != nil {
			dwellSecs = device.GetEffectiveDwellTime(app)
		}
		if dwellSecs <= 0 {
			dwellSecs = 15
		}

		// Refresh cadence:
		// Screeny procedural simulations (e.g. Flock, Overland, Knot) generate continuous frames
		// on each rotation cycle based on dwell time, regardless of database UInterval (which defaults to 10m).
		// Once a rendered batch has been served to a device (alreadyServed), the next rotation cycle must
		// render a fresh continuation batch so the animation never loops the same clip twice.
		hasCachedChunks := len(s.getAppChunks(device.ID, app)) > 0
		renderExpired := false
		if provider.Name() == "screeny" {
			renderExpired = !hasCachedChunks || alreadyServed || time.Since(app.LastRender) >= time.Duration(max(1, dwellSecs-1))*time.Second
		} else if app.UInterval > 0 {
			renderExpired = !hasCachedChunks || time.Since(app.LastRender) > time.Duration(app.UInterval)*time.Minute
		} else {
			renderExpired = !hasCachedChunks || alreadyServed || time.Since(app.LastRender) >= time.Duration(max(1, dwellSecs-1))*time.Second
		}

		maxChunkSize := s.GetMaxChunkSizeForDevice(device)
		needsRechunk := false
		if maxChunkSize > 0 && device != nil {
			deviceWebpDir, err := s.ensureDeviceImageDir(device.ID)
			if err == nil {
				primaryPath := s.getAppWebpPath(deviceWebpDir, app)
				if fi, err := os.Stat(primaryPath); err == nil && fi.Size() > int64(maxChunkSize) {
					needsRechunk = true
					slog.Info("Cached external app primary image exceeds device max chunk size; forcing re-render",
						"app", appBasename, "size", fi.Size(), "max", maxChunkSize)
				}
			}
			if !needsRechunk {
				for _, ch := range s.getAppChunks(device.ID, app) {
					if len(ch.WebPBytes) > maxChunkSize {
						needsRechunk = true
						slog.Info("Cached external app chunk exceeds device max chunk size; forcing re-render",
							"app", appBasename, "chunk_size", len(ch.WebPBytes), "max", maxChunkSize)
						break
					}
				}
			}
		}

		if forceRender || needsRechunk || renderExpired {
			slog.Info("Rendering external app", "provider", provider.Name(), "app", appBasename)
			startTime := time.Now()

			width, height := data.DefaultCanvasWidth, data.DefaultCanvasHeight
			if device != nil {
				width, height = device.Type.CanvasSize()
				if device.Type.Supports2x() {
					width *= 2
					height *= 2
				}
			}

			maxChunkDurationMs := s.GetMaxChunkDurationForDevice(device)
			renderDurationMs := min(max(dwellSecs*1000, 5000), 60000)

			req := providers.RenderRequest{
				AppID:              appID,
				Width:              width,
				Height:             height,
				FPS:                s.Config.ScreenyFPS,
				DurationMs:         renderDurationMs,
				MaxChunkSize:       maxChunkSize,
				MaxChunkDurationMs: maxChunkDurationMs,
				Timezone:           device.GetTimezone(),
				Config:             app.Config,
			}

			chunks, err := provider.RenderAppChunks(ctx, req)
			renderDur := time.Since(startTime)
			completedAt := time.Now()
			s.metrics.renderDuration.Observe(renderDur.Seconds())

			if err != nil || len(chunks) == 0 {
				s.metrics.renderTotal.WithLabelValues("error").Inc()
				slog.Error("Error rendering external app", "provider", provider.Name(), "app", appBasename, "error", err)

				// Update in-memory timestamps so erroring apps don't hammer the provider in a tight loop
				app.LastRender = completedAt
				app.LastRenderDur = renderDur
				app.EmptyLastRender = true

				// Graceful fallback: check if we have previously cached chunks that fit within maxChunkSize
				if existing := s.getAppChunks(device.ID, app); len(existing) > 0 && (maxChunkSize <= 0 || len(existing[0].WebPBytes) <= maxChunkSize) {
					slog.Warn("Reusing previously cached chunk(s) for unreachable external app", "app", appBasename)
					return true
				}

				// Record failed render in DB
				appUpdates := data.App{
					LastRender:      completedAt,
					LastRenderDur:   renderDur,
					EmptyLastRender: true,
				}
				_, _ = gorm.G[data.App](s.DB).Where("id = ?", app.ID).
					Select("LastRender", "LastRenderDur", "EmptyLastRender").
					Updates(ctx, appUpdates)
				return false
			}

			s.metrics.renderTotal.WithLabelValues("success").Inc()

			// Save chunks to disk and cache
			if err := s.saveAppChunks(device.ID, app, chunks); err != nil {
				slog.Error("Failed to save external app chunks", "app", appBasename, "error", err)
			}
			s.inFlightRendersMu.Lock()
			s.appRenderServed[appKey] = false
			s.inFlightRendersMu.Unlock()

			// Atomic DB update
			appUpdates := data.App{
				LastRender:           completedAt,
				LastSuccessfulRender: &completedAt,
				LastRenderDur:        renderDur,
				EmptyLastRender:      false,
			}
			rowsAffected, err := gorm.G[data.App](s.DB).Where("id = ?", app.ID).
				Select("LastRender", "LastSuccessfulRender", "LastRenderDur", "EmptyLastRender").
				Updates(ctx, appUpdates)
			if err != nil {
				slog.Error("Failed to update app state in DB", "app", appBasename, "error", err)
			} else if rowsAffected == 0 {
				slog.Info("App no longer exists in DB, aborting", "app", appBasename)
				return false
			}

			app.LastRender = completedAt
			app.LastSuccessfulRender = &completedAt
			app.LastRenderDur = renderDur
			app.EmptyLastRender = false

			if app.AutoPin {
				s.handleAutoPin(ctx, app, device, user, true)
			}
			return true
		}
		return true // Not time to render yet
	}

	appPath, err := securejoin.SecureJoin(s.DataDir, *app.Path)
	if err != nil {
		slog.Error("Failed to resolve app path", "path", *app.Path, "error", err)
		return false
	}
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	if err != nil {
		slog.Error("Failed to get device webp directory for rendering", "device_id", device.ID, "error", err)
		return false
	}
	webpPath, err := securejoin.SecureJoin(webpDir, fmt.Sprintf("%s.webp", appBasename))
	if err != nil {
		slog.Error("Path traversal attempt in webp path", "app", appBasename, "error", err)
		return false
	}

	// 2. Static WebP App
	if strings.HasSuffix(strings.ToLower(*app.Path), ".webp") {
		if _, err := os.Stat(webpPath); os.IsNotExist(err) {
			// Copy or chunk from source
			if b, err := os.ReadFile(appPath); err == nil {
				maxChunkSize := s.GetMaxChunkSizeForDevice(device)
				if device != nil && maxChunkSize > 0 && len(b) > maxChunkSize {
					maxChunkDurationMs := s.GetMaxChunkDurationForDevice(device)
					dwellSecs := device.GetEffectiveDwellTime(app)
					chunks, chunkErr := chunker.SplitWebPBytesIntoChunks(b, maxChunkSize, maxChunkDurationMs, dwellSecs*1000)
					if chunkErr == nil && len(chunks) > 1 {
						if err := s.saveAppChunks(device.ID, app, chunks); err != nil {
							slog.Error("Failed to save static webp chunks", "app", appBasename, "error", err)
						}
						return true
					}
				}
				if err := os.WriteFile(webpPath, b, 0644); err != nil {
					slog.Error("Failed to write static webp file", "src", appPath, "dst", webpPath, "error", err)
					return false
				}
			} else {
				slog.Warn("Source WebP not found", "path", appPath)
				return false
			}
		}
		return true // Exists
	}

	// 3. Starlark App - Check Interval
	now := time.Now()
	forceRender := s.filtersChangedSinceLastRender(device, app)
	uinterval := app.UInterval
	if uinterval <= 0 {
		uinterval = 10
	}
	// uinterval is minutes. Allow a 5-second grace window to absorb rotation timer jitter for 1-minute apps.
	uintervalDuration := time.Duration(uinterval) * time.Minute
	if uintervalDuration >= 10*time.Second {
		uintervalDuration -= 5 * time.Second
	}
	if forceRender || time.Since(app.LastRender) >= uintervalDuration {
		slog.Info("Rendering app", "app", appBasename)

		startTime := time.Now()
		imgBytes, messages, err := s.RenderApp(ctx, device, app, appPath, nil)
		renderDur := time.Since(startTime)

		for _, msg := range messages {
			slog.Debug("Render message", "app", appBasename, "message", msg)
		}

		empty := len(imgBytes) == 0
		success := err == nil && !empty

		s.metrics.renderDuration.Observe(renderDur.Seconds())
		switch {
		case err != nil:
			s.metrics.renderTotal.WithLabelValues("error").Inc()
			slog.Error("Error rendering app", "app", appBasename, "error", err)
		case empty:
			s.metrics.renderTotal.WithLabelValues("empty").Inc()
			slog.Debug("No output from app", "app", appBasename)
		default:
			s.metrics.renderTotal.WithLabelValues("success").Inc()
		}

		// Update App State in DB - This is our atomic check-and-update.
		// If the app was deleted while we were rendering, RowsAffected will be 0.
		appUpdates := data.App{
			LastRender:      now,
			LastRenderDur:   renderDur,
			EmptyLastRender: !success,
			RenderMessages:  data.StringSlice(messages),
		}

		q := gorm.G[data.App](s.DB).Where("id = ?", app.ID)
		var newHash string
		if success {
			newHash = fmt.Sprintf("%x", sha256.Sum256(imgBytes))
			appUpdates.LastRenderHash = newHash
			appUpdates.LastSuccessfulRender = &now
			q = q.Select("LastRender", "LastRenderDur", "EmptyLastRender", "RenderMessages", "LastSuccessfulRender", "LastRenderHash")
		} else {
			q = q.Select("LastRender", "LastRenderDur", "EmptyLastRender", "RenderMessages")
		}

		rowsAffected, err := q.Updates(ctx, appUpdates)
		if err != nil {
			slog.Error("Failed to update app state in DB", "app", appBasename, "error", err)
			return false
		}

		if rowsAffected == 0 {
			slog.Info("App no longer exists in DB, aborting", "app", appBasename)
			return false
		}

		if success {
			// Save WebP / Chunks:
			// If device has a hardware chunk size budget and the animation exceeds it,
			// automatically partition the WebP animation into sequential chunks.
			maxChunkSize := s.GetMaxChunkSizeForDevice(device)
			if device != nil && maxChunkSize > 0 && len(imgBytes) > maxChunkSize {
				maxChunkDurationMs := s.GetMaxChunkDurationForDevice(device)
				dwellSecs := device.GetEffectiveDwellTime(app)
				targetDurationMs := dwellSecs * 1000
				if app != nil && app.ShowFullAnimation != nil && *app.ShowFullAnimation {
					targetDurationMs = 0
				}

				chunks, chunkErr := chunker.SplitWebPBytesIntoChunks(imgBytes, maxChunkSize, maxChunkDurationMs, targetDurationMs)
				if chunkErr == nil && len(chunks) > 1 {
					slog.Info("Partitioned oversized Starlark animation into chunks",
						"app", appBasename,
						"device", device.ID,
						"original_bytes", len(imgBytes),
						"chunks", len(chunks),
						"max_chunk_size", maxChunkSize,
					)
					if err := s.saveAppChunks(device.ID, app, chunks); err != nil {
						slog.Error("Failed to save Starlark app chunks", "app", appBasename, "error", err)
					}
				} else {
					if chunkErr != nil {
						slog.Warn("Failed to split oversized Starlark animation into chunks", "app", appBasename, "error", chunkErr)
					}
					s.cleanupAppChunks(device.ID, app)
					if err := os.WriteFile(webpPath, imgBytes, 0644); err != nil {
						slog.Error("Failed to write webp", "path", webpPath, "error", err)
					}
				}
			} else {
				if device != nil {
					s.cleanupAppChunks(device.ID, app)
				}
				if err := os.WriteFile(webpPath, imgBytes, 0644); err != nil {
					slog.Error("Failed to write webp", "path", webpPath, "error", err)
				}
			}

			// Trigger notification sound if configured and opted in
			if app.NotificationSound != nil && *app.NotificationSound != "" && *app.NotificationSound != "none" && device != nil && device.GetAudioCapability() != data.AudioCapNone {
				contentChanged := (app.LastRenderHash != newHash)
				shouldPlay := false
				if app.NotificationSoundTrigger == "on_change" {
					shouldPlay = contentChanged
				}

				// Debounce: Enforce 30-second cooldown to prevent sound spam from animated apps
				if shouldPlay && app.LastSoundAt != nil && now.Sub(*app.LastSoundAt) < 30*time.Second {
					slog.Debug("Suppressing sound notification due to cooldown", "device", device.ID, "app", appBasename, "cooldown", 30*time.Second)
					shouldPlay = false
				}

				if shouldPlay {
					baseURL := s.GetDeviceBaseURL(device)
					if err := s.TriggerDeviceSound(ctx, device, *app.NotificationSound, baseURL); err != nil {
						slog.Error("Failed to trigger device sound on render", "device", device.ID, "app", appBasename, "error", err)
					} else {
						app.LastSoundAt = &now
						if _, err := gorm.G[data.App](s.DB).Where("id = ?", app.ID).Update(ctx, "last_sound_at", now); err != nil {
							slog.Debug("Failed to update last_sound_at in DB", "app", app.ID, "error", err)
						}
					}
				}
			}
		}

		// Update in-memory object (passed pointer)
		app.LastRender = now
		if success {
			app.LastSuccessfulRender = &now
			app.LastRenderHash = newHash
		}
		app.LastRenderDur = renderDur
		app.EmptyLastRender = !success
		app.RenderMessages = messages

		// Handle Autopin
		if app.AutoPin {
			s.handleAutoPin(ctx, app, device, user, success)
		}
		return success
	}

	return true // Not time to render yet, assume existing is fine
}

func (s *Server) handleAutoPin(ctx context.Context, app *data.App, device *data.Device, user *data.User, success bool) {
	shouldNotify := false
	if success {
		// Pin if not already pinned
		if device.PinnedApp == nil || *device.PinnedApp != app.Iname {
			if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(ctx, "pinned_app", app.Iname); err != nil {
				slog.Error("Failed to pin app", "app", app.Iname, "device_id", device.ID, "error", err)
			} else {
				device.PinnedApp = &app.Iname
				shouldNotify = true
			}
		}
	} else {
		// Unpin if currently pinned to this app
		if device.PinnedApp != nil && *device.PinnedApp == app.Iname {
			if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(ctx, "pinned_app", nil); err != nil {
				slog.Error("Failed to unpin app", "app", app.Iname, "device_id", device.ID, "error", err)
			} else {
				device.PinnedApp = nil
				shouldNotify = true
			}
		}
	}

	if shouldNotify && user != nil {
		s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})
	}
}

func (s *Server) getEffectiveFilters(device *data.Device, app *data.App) []string {
	return s.getEffectiveFiltersAt(device, app, time.Now())
}

func (s *Server) getEffectiveFiltersAt(device *data.Device, app *data.App, at time.Time) []string {
	var filters []string

	// Night/dim mode filters override app-level filters when set to a real filter.
	if modeFilter := deviceModeColorFilterAt(device, at); modeFilter != nil && *modeFilter != data.ColorFilterNone {
		filters = append(filters, string(*modeFilter))
		return filters
	}

	// Determine base device filter
	var deviceFilter data.ColorFilter
	if device.ColorFilter != nil {
		deviceFilter = *device.ColorFilter
	} else {
		deviceFilter = data.ColorFilterNone
	}

	appFilter := data.ColorFilterInherit
	if app != nil && app.ColorFilter != nil {
		appFilter = *app.ColorFilter
	}

	if appFilter != data.ColorFilterInherit {
		if appFilter != data.ColorFilterNone {
			filters = append(filters, string(appFilter))
		}
	} else if deviceFilter != data.ColorFilterNone {
		filters = append(filters, string(deviceFilter))
	}
	return filters
}

func (s *Server) filtersChangedSinceLastRender(device *data.Device, app *data.App) bool {
	return s.filtersChangedSinceLastRenderAt(device, app, time.Now())
}

func (s *Server) filtersChangedSinceLastRenderAt(device *data.Device, app *data.App, now time.Time) bool {
	if app.LastRender.IsZero() {
		return false
	}
	at := app.LastRender.In(device.GetLocation())
	return !slices.Equal(s.getEffectiveFiltersAt(device, app, at), s.getEffectiveFiltersAt(device, app, now.In(device.GetLocation())))
}

func deviceModeColorFilterAt(device *data.Device, at time.Time) *data.ColorFilter {
	if device.GetNightModeIsActiveAt(at) && device.NightColorFilter != nil {
		return device.NightColorFilter
	}
	if device.GetDimModeIsActiveAt(at) && device.DimColorFilter != nil {
		return device.DimColorFilter
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if err := in.Close(); err != nil {
			slog.Error("Failed to close source file", "error", err)
		}
	}()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if err := out.Close(); err != nil {
			slog.Error("Failed to close destination file", "error", err)
		}
	}()

	_, err = io.Copy(out, in)
	return err
}

func (s *Server) sendDefaultImage(w http.ResponseWriter, r *http.Request, device *data.Device) {
	// Fallback if main image retrieval fails
	path := "static/images/default.webp"

	// Get default image from embedded assets
	f, err := web.Assets.Open(path)
	if err != nil {
		slog.Error("Failed to open default image from assets", "error", err)
		http.Error(w, "Image not found", http.StatusNotFound)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("Failed to close default image file", "error", err)
		}
	}()

	stat, _ := f.Stat()

	// Headers
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")

	brightness := device.GetEffectiveBrightness()
	w.Header().Set("Tronbyt-Brightness", fmt.Sprintf("%d", brightness))

	dwell := device.DefaultInterval
	w.Header().Set("Tronbyt-Dwell-Secs", fmt.Sprintf("%d", dwell))

	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "default.webp", stat.ModTime(), rs)
	} else {
		slog.Error("Embedded file does not implement ReadSeeker")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
