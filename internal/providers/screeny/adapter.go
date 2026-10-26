package screeny

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"tronbyt-server/internal/providers"

	"github.com/gorilla/websocket"
)

const (
	// DefaultTransitionSkipSec is the duration (in seconds) of the Screeny Studio crossfade between patches.
	// Frames with patch timer < DefaultTransitionSkipSec are discarded to avoid blending artifacts.
	DefaultTransitionSkipSec float32 = 2.10
)

// AdapterConfig holds configuration settings for the ScreenyAdapter.
type AdapterConfig struct {
	StudioURL         string
	FPS               int
	MaxWebPBytes      int
	ScaleFilter       string
	TransitionSkipSec float32
	HTTPClient        *http.Client
	WSUpgrader        *websocket.Dialer
}

// ScreenyAdapter implements the providers.ExternalAppProvider interface for gotwalt/screeny.
type ScreenyAdapter struct {
	sem               chan struct{}
	studioURL         string
	fps               int
	maxWebPBytes      int
	scaleFilter       string
	transitionSkipSec float32
	httpClient        *http.Client
	wsDialer          *websocket.Dialer

	// Cached bootstrap data to avoid re-querying on every schema request.
	cacheMu     sync.RWMutex
	cachedApps  []providers.ExternalAppMeta
	cachedPatch map[string]StudioPatch
	lastCached  time.Time

	// Active patch tracking to preserve continuous procedural animation streams
	activePatchMu sync.Mutex
	activePatchID string
	lastParams    map[string]float32
}

// NewAdapter creates a new ScreenyAdapter with the provided configuration.
func NewAdapter(cfg AdapterConfig) *ScreenyAdapter {
	studioURL := strings.TrimRight(cfg.StudioURL, "/")
	if studioURL == "" {
		studioURL = "http://localhost:8787"
	}
	fps := cfg.FPS
	if fps <= 0 {
		fps = DefaultFPS
	}
	maxBytes := cfg.MaxWebPBytes
	if maxBytes <= 0 {
		maxBytes = 5242880 // 5 MB default
	}

	scaleFilter := cfg.ScaleFilter
	if scaleFilter == "" {
		scaleFilter = FilterBicubic
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	wsDialer := cfg.WSUpgrader
	if wsDialer == nil {
		wsDialer = &websocket.Dialer{
			HandshakeTimeout: 5 * time.Second,
		}
	}

	transitionSkipSec := cfg.TransitionSkipSec
	if transitionSkipSec == 0 {
		transitionSkipSec = DefaultTransitionSkipSec
	} else if transitionSkipSec < 0 {
		transitionSkipSec = 0 // explicitly disabled
	}

	return &ScreenyAdapter{
		sem:               make(chan struct{}, 1),
		studioURL:         studioURL,
		fps:               fps,
		maxWebPBytes:      maxBytes,
		scaleFilter:       scaleFilter,
		transitionSkipSec: transitionSkipSec,
		httpClient:        httpClient,
		wsDialer:          wsDialer,
		cachedPatch:       make(map[string]StudioPatch),
	}
}

// Name returns the provider's unique identifier.
func (a *ScreenyAdapter) Name() string {
	return "screeny"
}

// ListApps queries screeny-studio's /api/v1/bootstrap to discover available patches.
func (a *ScreenyAdapter) ListApps(ctx context.Context) ([]providers.ExternalAppMeta, error) {
	bootstrap, err := a.fetchBootstrap(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching screeny bootstrap: %w", err)
	}

	var metaList []providers.ExternalAppMeta
	patchMap := make(map[string]StudioPatch)

	for _, p := range bootstrap.Patches {
		patchMap[p.ID] = p
		// Skip patches explicitly marked unplayable on this host.
		if !p.Playable && p.UnplayableReason != nil {
			slog.Debug("Screeny patch marked unplayable, skipping", "id", p.ID, "reason", *p.UnplayableReason)
			continue
		}

		meta := providers.ExternalAppMeta{
			ID:         p.ID,
			Name:       p.Name,
			Summary:    p.Blurb,
			Desc:       p.Blurb,
			Author:     "Screeny",
			Provider:   a.Name(),
			Supports2x: true,
			Tags:       []string{"screeny", "30fps"},
		}
		metaList = append(metaList, meta)
	}

	a.cacheMu.Lock()
	a.cachedApps = metaList
	a.cachedPatch = patchMap
	a.lastCached = time.Now()
	a.cacheMu.Unlock()

	return metaList, nil
}

// GetAppSchema returns the Pixlet-compatible JSON schema for the specified Screeny patch ID.
func (a *ScreenyAdapter) GetAppSchema(ctx context.Context, appID string) ([]byte, error) {
	a.cacheMu.RLock()
	patch, found := a.cachedPatch[appID]
	a.cacheMu.RUnlock()

	if !found {
		// Cache miss; refresh bootstrap once
		if _, err := a.ListApps(ctx); err != nil {
			return nil, fmt.Errorf("loading screeny patches for schema %s: %w", appID, err)
		}
		a.cacheMu.RLock()
		patch, found = a.cachedPatch[appID]
		a.cacheMu.RUnlock()
	}

	if !found {
		return nil, fmt.Errorf("patch %q not found in screeny-studio", appID)
	}

	includeScaleFilter := true
	if v := ctx.Value(providers.Supports2xKey); v != nil {
		if b, ok := v.(bool); ok {
			includeScaleFilter = b
		}
	}

	return TranslatePatchParamsToPixletSchema(patch.Params, includeScaleFilter)
}

// RenderAppChunks renders a Screeny patch at 30 FPS for the requested duration,
// capturing frames and packaging them into WebP chunks.
func (a *ScreenyAdapter) RenderAppChunks(ctx context.Context, req providers.RenderRequest) ([]providers.RenderChunk, error) {
	// Mutex lock to ensure sequential rendering access to screeny-studio instance.
	// Uses a 1-capacity channel with select on ctx.Done so cancelled/timed-out requests never queue up.
	select {
	case a.sem <- struct{}{}:
		defer func() { <-a.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	fps := req.FPS
	if fps <= 0 {
		fps = a.fps
	}
	if fps <= 0 {
		fps = DefaultFPS
	}

	durationMs := req.DurationMs
	if durationMs <= 0 {
		durationMs = 10000 // 10s default
	}

	maxChunkSize := req.MaxChunkSize

	// 1. Activate the target patch in screeny-studio ONLY if it is not already active.
	// Preserves continuous procedural generation (e.g. Overland, Knot) across chunk cycles.
	a.activePatchMu.Lock()
	patchChanged := a.activePatchID != req.AppID
	if patchChanged {
		a.activePatchID = req.AppID
		a.lastParams = make(map[string]float32)
	}
	if a.lastParams == nil {
		a.lastParams = make(map[string]float32)
	}
	a.activePatchMu.Unlock()

	if patchChanged {
		if err := a.activatePatch(ctx, req.AppID); err != nil {
			a.activePatchMu.Lock()
			if a.activePatchID == req.AppID {
				a.activePatchID = ""
			}
			a.activePatchMu.Unlock()
			return nil, fmt.Errorf("activating patch %s: %w", req.AppID, err)
		}
	}

	scaleFilter := a.scaleFilter
	if sf, ok := req.Config["scale_filter"].(string); ok && sf != "" && sf != "default" {
		scaleFilter = sf
	}

	// 2. Apply config parameters only when changed or on patch switch
	for paramID, val := range req.Config {
		if paramID == "scale_filter" {
			continue // Handled internally by server-side scaling pipeline
		}
		if fVal, ok := parseParamValue(val); ok {
			a.activePatchMu.Lock()
			prevVal, exists := a.lastParams[paramID]
			needsSet := patchChanged || !exists || prevVal != fVal
			a.activePatchMu.Unlock()
			if needsSet {
				if err := a.setParam(ctx, paramID, fVal); err != nil {
					slog.Warn("Failed to set screeny parameter", "param", paramID, "value", fVal, "error", err)
				} else {
					a.activePatchMu.Lock()
					a.lastParams[paramID] = fVal
					a.activePatchMu.Unlock()
				}
			}
		}
	}

	// 3. Connect to screeny-studio WebSocket preview stream and capture frames.
	// For full renders (>= 5000ms), reserve ~1.2s of pipeline headroom so live capture +
	// lossless WebP encoding + WebSocket transmission finishes before the previous cycle's
	// dwell_secs expires on the device, preventing buffer starvation and frame looping.
	captureDurationMs := durationMs
	if durationMs >= 5000 {
		captureDurationMs = max(durationMs-1200, durationMs*85/100)
	}
	frames, err := a.captureFrames(ctx, fps, captureDurationMs, req.Width, req.Height, scaleFilter, patchChanged)
	if err != nil {
		return nil, fmt.Errorf("capturing screeny frames: %w", err)
	}

	if len(frames) == 0 {
		return nil, errors.New("captured zero frames from screeny-studio")
	}

	// 4. Split captured frames into animated WebP chunks paced across durationMs
	return SplitFramesIntoChunksWithDuration(frames, fps, maxChunkSize, req.MaxChunkDurationMs, durationMs)
}

// fetchBootstrap calls GET /api/v1/bootstrap on screeny-studio.
func (a *ScreenyAdapter) fetchBootstrap(ctx context.Context) (*StudioBootstrap, error) {
	u := fmt.Sprintf("%s/api/v1/bootstrap", a.studioURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bootstrap returned status %d", resp.StatusCode)
	}

	var bootstrap StudioBootstrap
	if err := json.NewDecoder(resp.Body).Decode(&bootstrap); err != nil {
		return nil, fmt.Errorf("decoding bootstrap JSON: %w", err)
	}

	return &bootstrap, nil
}

// activatePatch calls POST /api/v1/set_patch on screeny-studio.
func (a *ScreenyAdapter) activatePatch(ctx context.Context, patchID string) error {
	u := fmt.Sprintf("%s/api/v1/set_patch", a.studioURL)
	payload, err := json.Marshal(map[string]string{"id": patchID})
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("set_patch status %d", resp.StatusCode)
	}

	return nil
}

// setParam calls POST /api/v1/set_param on screeny-studio.
func (a *ScreenyAdapter) setParam(ctx context.Context, paramID string, val float32) error {
	u := fmt.Sprintf("%s/api/v1/set_param", a.studioURL)
	payload, err := json.Marshal(map[string]any{
		"id":    paramID,
		"value": val,
	})
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("set_param status %d", resp.StatusCode)
	}

	return nil
}

// captureFrames opens a WebSocket to /api/v1/ws and collects frames for durationMs.
func (a *ScreenyAdapter) captureFrames(ctx context.Context, fps, durationMs, targetWidth, targetHeight int, scaleFilter string, patchChanged bool) ([]*image.RGBA, error) {
	u, err := url.Parse(a.studioURL)
	if err != nil {
		return nil, fmt.Errorf("parsing studio URL: %w", err)
	}

	wsScheme := "ws"
	if u.Scheme == "https" {
		wsScheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/api/v1/ws?fps=%d&repeat=true&overview=false", wsScheme, u.Host, fps)

	conn, _, err := a.wsDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dialing screeny ws %s: %w", wsURL, err)
	}
	defer func() { _ = conn.Close() }()

	targetFrameCount := (durationMs * fps) / 1000
	if targetFrameCount <= 0 {
		targetFrameCount = 30
	}
	const maxCapturedFrames = 1800 // safety limit: 60s @ 30 FPS
	if targetFrameCount > maxCapturedFrames {
		targetFrameCount = maxCapturedFrames
	}

	scaleRequired := targetWidth == 128 && targetHeight == 64
	var frames []*image.RGBA
	timeoutDuration := time.Duration(durationMs+5000) * time.Millisecond
	if patchChanged && a.transitionSkipSec > 0 {
		timeoutDuration += time.Duration(a.transitionSkipSec*1000) * time.Millisecond
	}
	timeout := time.After(timeoutDuration)

	for len(frames) < targetFrameCount {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			// If we got some frames before timeout, return what we have
			if len(frames) > 0 {
				return frames, nil
			}
			return nil, errors.New("timeout waiting for screeny frames")
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		msgType, msgData, err := conn.ReadMessage()
		if err != nil {
			if len(frames) > 0 {
				break
			}
			return nil, fmt.Errorf("reading ws frame: %w", err)
		}

		if msgType != websocket.BinaryMessage {
			continue // skip JSON heartbeats / state messages
		}

		// Screeny Studio performs a 2.0-second crossfade when switching patches.
		// While timer < transitionSkipSec, discard blend frames only if a patch switch just occurred.
		if patchChanged && len(msgData) == PreviewPacketLen && a.transitionSkipSec > 0 {
			timer := math.Float32frombits(binary.LittleEndian.Uint32(msgData[4:8]))
			if timer < a.transitionSkipSec {
				continue
			}
		}

		frame, err := DecodeScreenyFrame(msgData)
		if err != nil {
			slog.Debug("Failed to decode screeny frame, skipping", "error", err)
			continue
		}

		if scaleRequired {
			frame = ScaleFrame2x(frame, scaleFilter)
		}

		frames = append(frames, frame)
	}

	return frames, nil
}

// parseParamValue parses user-provided form configuration into a float32 expected by Screeny.
func parseParamValue(val any) (float32, bool) {
	switch v := val.(type) {
	case float64:
		return float32(v), true
	case float32:
		return v, true
	case int:
		return float32(v), true
	case int32:
		return float32(v), true
	case int64:
		return float32(v), true
	case json.Number:
		if parsed, err := v.Float64(); err == nil {
			return float32(parsed), true
		}
	case bool:
		if v {
			return 1.0, true
		}
		return 0.0, true
	case string:
		if strings.EqualFold(v, "true") {
			return 1.0, true
		}
		if strings.EqualFold(v, "false") {
			return 0.0, true
		}
		if parsed, err := strconv.ParseFloat(v, 32); err == nil {
			return float32(parsed), true
		}
	}
	return 0, false
}
