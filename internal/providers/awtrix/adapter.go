package awtrix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tronbyt-server/internal/chunker"
	"tronbyt-server/internal/providers"
	"tronbyt-server/internal/scaler"
)

// DefaultFPS is 15 frames per second for Awtrix capture.
const DefaultFPS = 15

// AdapterConfig holds parameters to configure the AwtrixAdapter.
type AdapterConfig struct {
	BaseURL     string
	FPS         int
	ScaleFilter string
	Layout      string
	HTTPClient  *http.Client
}

// AwtrixAdapter implements providers.ExternalAppProvider for AWTRIX NG.
type AwtrixAdapter struct {
	sem         chan struct{}
	baseURL     string
	fps         int
	scaleFilter string
	layout      string
	httpClient  *http.Client
}

// NewAdapter creates a new AwtrixAdapter.
func NewAdapter(cfg AdapterConfig) *AwtrixAdapter {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:7080"
	}
	fps := cfg.FPS
	if fps <= 0 {
		fps = DefaultFPS
	}
	scaleFilter := cfg.ScaleFilter
	if scaleFilter == "" {
		scaleFilter = scaler.FilterBicubic
	}
	layout := cfg.Layout
	if layout == "" {
		layout = "center"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	return &AwtrixAdapter{
		sem:         make(chan struct{}, 1),
		baseURL:     baseURL,
		fps:         fps,
		scaleFilter: scaleFilter,
		layout:      layout,
		httpClient:  httpClient,
	}
}

// Name returns the provider's unique identifier.
func (a *AwtrixAdapter) Name() string {
	return "awtrix"
}

type awtrixAppItem struct {
	Name    string         `json:"name"`
	Enabled bool           `json:"enabled"`
	InLoop  bool           `json:"inLoop"`
	Present bool           `json:"present"`
	Origin  string         `json:"origin"`
	Config  bool           `json:"config"`
	Meta    *awtrixAppMeta `json:"meta,omitempty"`
}

type awtrixAppMeta struct {
	Name    string `json:"name,omitempty"`
	Desc    string `json:"desc,omitempty"`
	Author  string `json:"author,omitempty"`
	Version string `json:"version,omitempty"`
}

// ListApps queries AWTRIX NG's /api/v1/apps endpoint to discover installed and built-in apps.
func (a *AwtrixAdapter) ListApps(ctx context.Context) ([]providers.ExternalAppMeta, error) {
	u := fmt.Sprintf("%s/api/v1/apps", a.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating apps request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying awtrix apps: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("awtrix apps returned status %d: %s", resp.StatusCode, string(body))
	}

	var items []awtrixAppItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decoding awtrix apps response: %w", err)
	}

	var result []providers.ExternalAppMeta
	for _, item := range items {
		if !item.Present && item.Origin != "builtin" {
			continue // skip uninstalled scripts
		}

		name := item.Name
		summary := fmt.Sprintf("AWTRIX NG %s app", item.Name)
		desc := summary
		author := "AWTRIX NG"

		if item.Meta != nil {
			if item.Meta.Name != "" {
				name = item.Meta.Name
			}
			if item.Meta.Desc != "" {
				summary = item.Meta.Desc
				desc = item.Meta.Desc
			}
			if item.Meta.Author != "" {
				author = item.Meta.Author
			}
		}

		result = append(result, providers.ExternalAppMeta{
			ID:      item.Name,
			Name:    name,
			Summary: summary,
			Desc:    desc,
			Author:  author,
		})
	}

	return result, nil
}

// GetAppSchema returns the Pixlet-compatible configuration schema for an Awtrix app.
func (a *AwtrixAdapter) GetAppSchema(ctx context.Context, appID string) ([]byte, error) {
	cfg, isBuiltin, err := a.fetchAppConfig(ctx, appID)
	if err != nil {
		slog.Warn("Failed to fetch awtrix app config, returning empty schema", "app", appID, "error", err)
		return []byte(`{"version":"1","schema":[]}`), nil
	}

	return TranslateAwtrixSchema(*cfg, isBuiltin)
}

func (a *AwtrixAdapter) fetchAppConfig(ctx context.Context, appID string) (*AwtrixConfigResponse, bool, error) {
	// Try builtin URL first
	builtinURL := fmt.Sprintf("%s/api/v1/apps/builtin/%s/config", a.baseURL, url.PathEscape(appID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, builtinURL, nil)
	if err != nil {
		return nil, false, err
	}

	resp, err := a.httpClient.Do(req)
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusOK {
			var cfg AwtrixConfigResponse
			if err := json.NewDecoder(resp.Body).Decode(&cfg); err == nil {
				return &cfg, true, nil
			}
		}
	}

	// Try non-builtin script URL
	scriptURL := fmt.Sprintf("%s/api/v1/apps/%s/config", a.baseURL, url.PathEscape(appID))
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, scriptURL, nil)
	if err != nil {
		return nil, false, err
	}

	resp2, err := a.httpClient.Do(req2)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("fetching config returned status %d", resp2.StatusCode)
	}

	var cfg AwtrixConfigResponse
	if err := json.NewDecoder(resp2.Body).Decode(&cfg); err != nil {
		return nil, false, err
	}

	return &cfg, false, nil
}

type screenData struct {
	Width  int   `json:"width"`
	Height int   `json:"height"`
	Pixels []int `json:"pixels"`
}

// RenderAppChunks configures the app on AWTRIX NG, captures virtual screen frames, and encodes them into WebP chunks.
func (a *AwtrixAdapter) RenderAppChunks(ctx context.Context, req providers.RenderRequest) ([]providers.RenderChunk, error) {
	// Acquire semaphore to ensure exclusive access to the Awtrix virtual display
	select {
	case a.sem <- struct{}{}:
		defer func() { <-a.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// 1. Query original settings so we can reliably restore them on exit
	type awtrixSettingsResp struct {
		AutoTransition       *bool `json:"autoTransition,omitempty"`
		TransitionDurationMs *int  `json:"transitionDurationMs,omitempty"`
	}
	var origSettings awtrixSettingsResp
	settingsURL := fmt.Sprintf("%s/api/v1/settings", a.baseURL)
	if sReq, err := http.NewRequestWithContext(ctx, http.MethodGet, settingsURL, nil); err == nil {
		if sResp, err := a.httpClient.Do(sReq); err == nil {
			_ = json.NewDecoder(sResp.Body).Decode(&origSettings)
			_, _ = io.Copy(io.Discard, io.LimitReader(sResp.Body, 1024))
			_ = sResp.Body.Close()
		}
	}

	// Ensure AWTRIX auto-transition and display settings are always restored when rendering completes or aborts
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		restoreMap := map[string]any{"autoTransition": true}
		if origSettings.AutoTransition != nil {
			restoreMap["autoTransition"] = *origSettings.AutoTransition
		}
		if origSettings.TransitionDurationMs != nil {
			restoreMap["transitionDurationMs"] = *origSettings.TransitionDurationMs
		}
		if b, err := json.Marshal(restoreMap); err == nil {
			if rReq, err := http.NewRequestWithContext(cleanupCtx, http.MethodPatch, settingsURL, bytes.NewReader(b)); err == nil {
				rReq.Header.Set("Content-Type", "application/json")
				if rResp, err := a.httpClient.Do(rReq); err == nil {
					_, _ = io.Copy(io.Discard, io.LimitReader(rResp.Body, 1024))
					_ = rResp.Body.Close()
				}
			}
		}
	}()

	// 2. If custom configuration options were provided, apply them via PATCH
	if len(req.Config) > 0 {
		if cfg, isBuiltin, err := a.fetchAppConfig(ctx, req.AppID); err == nil && len(cfg.Fields) > 0 {
			patchBody := ConvertUserConfigToAwtrix(cfg.Fields, req.Config)
			if len(patchBody) > 0 {
				patchURL := fmt.Sprintf("%s/api/v1/apps/builtin/%s/config", a.baseURL, url.PathEscape(req.AppID))
				if !isBuiltin {
					patchURL = fmt.Sprintf("%s/api/v1/apps/%s/config", a.baseURL, url.PathEscape(req.AppID))
				}
				bodyBytes, err := json.Marshal(patchBody)
				if err == nil {
					pReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, patchURL, bytes.NewReader(bodyBytes))
					if err == nil {
						pReq.Header.Set("Content-Type", "application/json")
						if pResp, err := a.httpClient.Do(pReq); err == nil {
							_, _ = io.Copy(io.Discard, io.LimitReader(pResp.Body, 1024))
							_ = pResp.Body.Close()
						}
					}
				}
			}
		}
	}

	// 3. Freeze AWTRIX auto-rotation and disable transitions so only this single app renders
	freezeBody := []byte(`{"autoTransition":false,"transitionDurationMs":0}`)
	if fReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, settingsURL, bytes.NewReader(freezeBody)); err == nil {
		fReq.Header.Set("Content-Type", "application/json")
		if fResp, err := a.httpClient.Do(fReq); err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(fResp.Body, 1024))
			_ = fResp.Body.Close()
		}
	}

	// 4. Activate the app on the AWTRIX display
	activateURL := fmt.Sprintf("%s/api/v1/apps/active", a.baseURL)

	// If the app is already active, Awtrix ignores PUT /apps/active and skips on_show() lifecycle hooks,
	// leaving animation timers running at the tail end. Briefly switch away using POST /apps/next to force
	// a clean lifecycle restart.
	deviceURL := fmt.Sprintf("%s/api/v1/device", a.baseURL)
	if dReq, err := http.NewRequestWithContext(ctx, http.MethodGet, deviceURL, nil); err == nil {
		if dResp, err := a.httpClient.Do(dReq); err == nil {
			var devInfo struct {
				CurrentApp string `json:"currentApp"`
			}
			if json.NewDecoder(dResp.Body).Decode(&devInfo) == nil && devInfo.CurrentApp == req.AppID {
				nextURL := fmt.Sprintf("%s/api/v1/apps/next", a.baseURL)
				if nReq, err := http.NewRequestWithContext(ctx, http.MethodPost, nextURL, nil); err == nil {
					if nResp, err := a.httpClient.Do(nReq); err == nil {
						_, _ = io.Copy(io.Discard, io.LimitReader(nResp.Body, 1024))
						_ = nResp.Body.Close()
					}
				}
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(dResp.Body, 1024))
			_ = dResp.Body.Close()
		}
	}

	activateBody, _ := json.Marshal(map[string]string{"name": req.AppID})
	actReq, err := http.NewRequestWithContext(ctx, http.MethodPut, activateURL, bytes.NewReader(activateBody))
	if err != nil {
		return nil, fmt.Errorf("creating activate request: %w", err)
	}
	actReq.Header.Set("Content-Type", "application/json")

	actResp, err := a.httpClient.Do(actReq)
	if err != nil {
		return nil, fmt.Errorf("activating awtrix app: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(actResp.Body, 1024))
	_ = actResp.Body.Close()
	if actResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("activating awtrix app returned status %d", actResp.StatusCode)
	}

	// Wait 150ms for initial layout rendering
	select {
	case <-time.After(150 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// 4. Determine capture parameters
	fps := req.FPS
	if fps <= 0 {
		fps = a.fps
	}
	if fps > 30 {
		fps = 30
	}
	durationMs := req.DurationMs
	if durationMs <= 0 {
		durationMs = 10000
	}

	targetFrames := max(1, (durationMs*fps)/1000)

	frameInterval := time.Duration(1000/fps) * time.Millisecond
	screenURL := fmt.Sprintf("%s/api/v1/display/screen", a.baseURL)

	layoutMode := a.layout
	if customLayout, ok := req.Config["_layout"].(string); ok && customLayout != "" && customLayout != "default" {
		layoutMode = customLayout
	} else if customLayout, ok := req.Config["layout"].(string); ok && (customLayout == "center" || customLayout == "fill" || customLayout == "stretch" || customLayout == "native") {
		layoutMode = customLayout
	}
	if layoutMode == "stretch" {
		layoutMode = "fill"
	}
	if layoutMode != "fill" && layoutMode != "native" && layoutMode != "center" {
		layoutMode = "center"
	}

	var frames []*image.RGBA
	var layoutDetermined bool
	var detectedLayout layoutInfo

	// 5. Capture frames
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()

	consecutiveErrors := 0
	for range targetFrames {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}

		sReq, err := http.NewRequestWithContext(ctx, http.MethodGet, screenURL, nil)
		if err != nil {
			continue
		}
		sResp, err := a.httpClient.Do(sReq)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= 5 {
				return nil, fmt.Errorf("reading awtrix screen frame: %w", err)
			}
			continue
		}

		if sResp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, io.LimitReader(sResp.Body, 1024))
			_ = sResp.Body.Close()
			consecutiveErrors++
			if consecutiveErrors >= 5 {
				return nil, fmt.Errorf("awtrix screen frame returned status %d repeatedly", sResp.StatusCode)
			}
			continue
		}
		consecutiveErrors = 0

		var sd screenData
		decodeErr := json.NewDecoder(sResp.Body).Decode(&sd)
		_, _ = io.Copy(io.Discard, io.LimitReader(sResp.Body, 4096))
		_ = sResp.Body.Close()

		if decodeErr != nil || sd.Width <= 0 || sd.Height <= 0 || len(sd.Pixels) != sd.Width*sd.Height {
			continue
		}

		// Convert pixel array to image.RGBA (row-major 64x32)
		img := image.NewRGBA(image.Rect(0, 0, sd.Width, sd.Height))
		for idx, pix := range sd.Pixels {
			offset := idx * 4
			img.Pix[offset] = uint8((pix >> 16) & 0xFF)
			img.Pix[offset+1] = uint8((pix >> 8) & 0xFF)
			img.Pix[offset+2] = uint8(pix & 0xFF)
			img.Pix[offset+3] = 0xFF
		}

		// Determine layout mode on the first frame with visible content
		if !layoutDetermined && layoutMode != "native" && sd.Width == 64 && sd.Height == 32 {
			if info, ok := detectLayoutInfo(img); ok {
				layoutDetermined = true
				detectedLayout = info
			}
		}

		if sd.Width == 64 && sd.Height == 32 {
			img = applyAwtrixLayout(img, detectedLayout, layoutMode)
		}

		img = fitToTargetCanvas(img, req.Width, req.Height, layoutMode, detectedLayout, a.scaleFilter)

		frames = append(frames, img)
	}

	if len(frames) == 0 {
		return nil, errors.New("no frames captured from awtrix screen")
	}

	// 5. Partition frames into WebP chunk(s)
	return chunker.SplitFramesIntoChunksWithDuration(frames, fps, req.MaxChunkSize, req.MaxChunkDurationMs, durationMs)
}

type layoutInfo struct {
	mode    string  // "8row", "16row-narrow", "16row-full", "32row"
	bgColor [4]byte // R, G, B, A
}

func detectLayoutInfo(img *image.RGBA) (layoutInfo, bool) {
	if img == nil || img.Bounds().Dx() < 64 || img.Bounds().Dy() < 32 {
		return layoutInfo{}, false
	}
	// Sample bottom-right corner pixel as candidate background
	offBR := img.PixOffset(63, 31)
	bgR, bgG, bgB := img.Pix[offBR], img.Pix[offBR+1], img.Pix[offBR+2]

	// Check if the bottom row (y=31) is uniform
	isUniformRow31 := true
	for x := range 64 {
		off := img.PixOffset(x, 31)
		if img.Pix[off] != bgR || img.Pix[off+1] != bgG || img.Pix[off+2] != bgB {
			isUniformRow31 = false
			break
		}
	}

	// If bottom row is not uniform, the app renders active content across all 32 rows
	if !isUniformRow31 {
		return layoutInfo{mode: "32row", bgColor: [4]byte{0, 0, 0, 0xFF}}, true
	}

	bgColor := [4]byte{bgR, bgG, bgB, 0xFF}

	// Find the bounding box of pixels differing from bgColor in rows 0..30
	maxY := -1
	maxX := -1
	hasContent := false

	for y := range 31 {
		for x := range 64 {
			off := img.PixOffset(x, y)
			if img.Pix[off] != bgR || img.Pix[off+1] != bgG || img.Pix[off+2] != bgB {
				hasContent = true
				if y > maxY {
					maxY = y
				}
				if x > maxX {
					maxX = x
				}
			}
		}
	}

	// If no content differing from bg is found yet (e.g. frame is pure black or pure clear before draw), wait for next frame
	if !hasContent {
		return layoutInfo{}, false
	}

	var mode string
	if maxY <= 7 {
		mode = "8row"
	} else if maxY <= 15 {
		if maxX <= 52 {
			mode = "16row-narrow"
		} else {
			mode = "16row-full"
		}
	} else {
		mode = "32row"
	}

	return layoutInfo{mode: mode, bgColor: bgColor}, true
}

func applyAwtrixLayout(src *image.RGBA, info layoutInfo, layout string) *image.RGBA {
	if layout == "native" {
		return src
	}

	if layout == "fill" {
		switch info.mode {
		case "8row":
			// 8-row app (e.g. Ghost Parade: 32x8, High Speed Train: 32x8):
			// 2x horizontal scale (32->64) and 4x vertical scale (8->32) to fill 64x32.
			dst := image.NewRGBA(image.Rect(0, 0, 64, 32))
			for y := range 8 {
				for x := range 32 {
					srcOff := src.PixOffset(x, y)
					r, g, b, a := src.Pix[srcOff], src.Pix[srcOff+1], src.Pix[srcOff+2], src.Pix[srcOff+3]
					for dy := range 4 {
						for dx := range 2 {
							dstOff := dst.PixOffset(x*2+dx, y*4+dy)
							dst.Pix[dstOff] = r
							dst.Pix[dstOff+1] = g
							dst.Pix[dstOff+2] = b
							dst.Pix[dstOff+3] = a
						}
					}
				}
			}
			return dst

		case "16row-narrow":
			// 16-row 52-column app (e.g. Beautiful Weather: 52x16):
			// Stretch 52 columns across 64 columns, and 2x vertical scale 16 rows to 32 rows.
			dst := image.NewRGBA(image.Rect(0, 0, 64, 32))
			for y := range 16 {
				for dx := range 64 {
					sx := (dx*51 + 31) / 63
					srcOff := src.PixOffset(sx, y)
					r, g, b, a := src.Pix[srcOff], src.Pix[srcOff+1], src.Pix[srcOff+2], src.Pix[srcOff+3]
					for dy := range 2 {
						dstOff := dst.PixOffset(dx, y*2+dy)
						dst.Pix[dstOff] = r
						dst.Pix[dstOff+1] = g
						dst.Pix[dstOff+2] = b
						dst.Pix[dstOff+3] = a
					}
				}
			}
			return dst

		case "16row-full":
			// 16-row 64-column app (e.g. Pixel Aquarium: 64x16, Weather: 64x16):
			// 2x vertical scale 16 rows to 32 rows across all 64 columns.
			dst := image.NewRGBA(image.Rect(0, 0, 64, 32))
			for y := range 16 {
				for x := range 64 {
					srcOff := src.PixOffset(x, y)
					r, g, b, a := src.Pix[srcOff], src.Pix[srcOff+1], src.Pix[srcOff+2], src.Pix[srcOff+3]
					for dy := range 2 {
						dstOff := dst.PixOffset(x, y*2+dy)
						dst.Pix[dstOff] = r
						dst.Pix[dstOff+1] = g
						dst.Pix[dstOff+2] = b
						dst.Pix[dstOff+3] = a
					}
				}
			}
			return dst

		default:
			return src
		}
	}

	// Default: "center"
	switch info.mode {
	case "8row":
		// 8-row app: 2x integer scale to 64x16, centered vertically at Y=8..23.
		dst := image.NewRGBA(image.Rect(0, 0, 64, 32))
		for i := 0; i < len(dst.Pix); i += 4 {
			dst.Pix[i] = info.bgColor[0]
			dst.Pix[i+1] = info.bgColor[1]
			dst.Pix[i+2] = info.bgColor[2]
			dst.Pix[i+3] = info.bgColor[3]
		}
		for y := range 8 {
			for x := range 32 {
				srcOff := src.PixOffset(x, y)
				r, g, b, a := src.Pix[srcOff], src.Pix[srcOff+1], src.Pix[srcOff+2], src.Pix[srcOff+3]
				for dy := range 2 {
					for dx := range 2 {
						dstOff := dst.PixOffset(x*2+dx, 8+y*2+dy)
						dst.Pix[dstOff] = r
						dst.Pix[dstOff+1] = g
						dst.Pix[dstOff+2] = b
						dst.Pix[dstOff+3] = a
					}
				}
			}
		}
		return dst

	case "16row-narrow":
		// 16-row 52-column app: center horizontally at X=6, vertically at Y=8
		dst := image.NewRGBA(image.Rect(0, 0, 64, 32))
		for i := 0; i < len(dst.Pix); i += 4 {
			dst.Pix[i] = info.bgColor[0]
			dst.Pix[i+1] = info.bgColor[1]
			dst.Pix[i+2] = info.bgColor[2]
			dst.Pix[i+3] = info.bgColor[3]
		}
		maxX := min(src.Bounds().Dx(), 64)
		for y := range 16 {
			for x := range maxX {
				if x+6 >= 64 {
					continue
				}
				srcOff := src.PixOffset(x, y)
				dstOff := dst.PixOffset(x+6, y+8)
				copy(dst.Pix[dstOff:dstOff+4], src.Pix[srcOff:srcOff+4])
			}
		}
		return dst

	case "16row-full":
		// 16-row 64-column app: center vertically at Y=8 across full width
		dst := image.NewRGBA(image.Rect(0, 0, 64, 32))
		for i := 0; i < len(dst.Pix); i += 4 {
			dst.Pix[i] = info.bgColor[0]
			dst.Pix[i+1] = info.bgColor[1]
			dst.Pix[i+2] = info.bgColor[2]
			dst.Pix[i+3] = info.bgColor[3]
		}
		for y := range 16 {
			for x := range 64 {
				srcOff := src.PixOffset(x, y)
				dstOff := dst.PixOffset(x, y+8)
				copy(dst.Pix[dstOff:dstOff+4], src.Pix[srcOff:srcOff+4])
			}
		}
		return dst

	default:
		return src
	}
}

func fitToTargetCanvas(img *image.RGBA, targetW, targetH int, layout string, info layoutInfo, scaleFilter string) *image.RGBA {
	if img == nil {
		return nil
	}
	imgW := img.Bounds().Dx()
	imgH := img.Bounds().Dy()
	if targetW <= 0 || targetH <= 0 || imgW <= 0 || imgH <= 0 {
		return img
	}
	if targetW == imgW && targetH == imgH {
		return img
	}

	// 128x64 (Tronberry / 2x wide displays)
	if targetW == 128 && targetH == 64 && imgW == 64 && imgH == 32 {
		return scaler.ScaleFrame2x(img, scaleFilter)
	}

	bg := info.bgColor
	if bg[3] == 0 {
		bg[3] = 0xFF
	}

	// 64x64 (Square displays)
	if targetW == 64 && targetH == 64 && imgW == 64 && imgH == 32 {
		dst := image.NewRGBA(image.Rect(0, 0, 64, 64))
		if layout == "fill" {
			for y := range 32 {
				for x := range 64 {
					srcOff := img.PixOffset(x, y)
					r, g, b, a := img.Pix[srcOff], img.Pix[srcOff+1], img.Pix[srcOff+2], img.Pix[srcOff+3]
					for dy := range 2 {
						dstOff := dst.PixOffset(x, y*2+dy)
						dst.Pix[dstOff] = r
						dst.Pix[dstOff+1] = g
						dst.Pix[dstOff+2] = b
						dst.Pix[dstOff+3] = a
					}
				}
			}
			return dst
		}

		// Fill background
		for i := 0; i < len(dst.Pix); i += 4 {
			dst.Pix[i] = bg[0]
			dst.Pix[i+1] = bg[1]
			dst.Pix[i+2] = bg[2]
			dst.Pix[i+3] = bg[3]
		}
		yOffset := 0
		if layout != "native" {
			yOffset = (64 - 32) / 2 // = 16
		}
		for y := range 32 {
			for x := range 64 {
				srcOff := img.PixOffset(x, y)
				dstOff := dst.PixOffset(x, y+yOffset)
				copy(dst.Pix[dstOff:dstOff+4], img.Pix[srcOff:srcOff+4])
			}
		}
		return dst
	}

	// 128x128 (Square 2x displays)
	if targetW == 128 && targetH == 128 && imgW == 64 && imgH == 32 {
		img128 := scaler.ScaleFrame2x(img, scaleFilter)
		dst := image.NewRGBA(image.Rect(0, 0, 128, 128))
		if layout == "fill" {
			for y := range 64 {
				for x := range 128 {
					srcOff := img128.PixOffset(x, y)
					r, g, b, a := img128.Pix[srcOff], img128.Pix[srcOff+1], img128.Pix[srcOff+2], img128.Pix[srcOff+3]
					for dy := range 2 {
						dstOff := dst.PixOffset(x, y*2+dy)
						dst.Pix[dstOff] = r
						dst.Pix[dstOff+1] = g
						dst.Pix[dstOff+2] = b
						dst.Pix[dstOff+3] = a
					}
				}
			}
			return dst
		}

		for i := 0; i < len(dst.Pix); i += 4 {
			dst.Pix[i] = bg[0]
			dst.Pix[i+1] = bg[1]
			dst.Pix[i+2] = bg[2]
			dst.Pix[i+3] = bg[3]
		}
		yOffset := 0
		if layout != "native" {
			yOffset = (128 - 64) / 2 // = 32
		}
		for y := range 64 {
			for x := range 128 {
				srcOff := img128.PixOffset(x, y)
				dstOff := dst.PixOffset(x, y+yOffset)
				copy(dst.Pix[dstOff:dstOff+4], img128.Pix[srcOff:srcOff+4])
			}
		}
		return dst
	}

	// General fallback for any other target dimensions
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	if layout == "fill" {
		for y := range targetH {
			sy := (y * imgH) / targetH
			for x := range targetW {
				sx := (x * imgW) / targetW
				srcOff := img.PixOffset(sx, sy)
				dstOff := dst.PixOffset(x, y)
				copy(dst.Pix[dstOff:dstOff+4], img.Pix[srcOff:srcOff+4])
			}
		}
		return dst
	}

	for i := 0; i < len(dst.Pix); i += 4 {
		dst.Pix[i] = bg[0]
		dst.Pix[i+1] = bg[1]
		dst.Pix[i+2] = bg[2]
		dst.Pix[i+3] = bg[3]
	}
	offsetX := max(0, (targetW-imgW)/2)
	offsetY := 0
	if layout != "native" {
		offsetY = max(0, (targetH-imgH)/2)
	}
	for y := range imgH {
		if y+offsetY >= targetH {
			break
		}
		for x := range imgW {
			if x+offsetX >= targetW {
				break
			}
			srcOff := img.PixOffset(x, y)
			dstOff := dst.PixOffset(x+offsetX, y+offsetY)
			copy(dst.Pix[dstOff:dstOff+4], img.Pix[srcOff:srcOff+4])
		}
	}
	return dst
}
