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
	"math/rand/v2"
	"net"
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
	activePatchMu      sync.Mutex
	activePatchID      string
	lastParams         map[string]float32
	lastSeed           uint32
	lastPatchActivated time.Time
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

	scaleFilter := cfg.ScaleFilter
	if scaleFilter == "" {
		scaleFilter = FilterBicubic
	}

	safeDialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		if ip := net.ParseIP(host); ip != nil {
			if providers.IsBlockedMetadataIP(ip) {
				return nil, fmt.Errorf("connection to prohibited IP %s blocked", ip)
			}
		} else if ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host); err == nil {
			for _, ip := range ips {
				if providers.IsBlockedMetadataIP(ip) {
					return nil, fmt.Errorf("connection to prohibited IP %s blocked", ip)
				}
			}
		}
		d := &net.Dialer{Timeout: 5 * time.Second}
		return d.DialContext(ctx, network, addr)
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				DialContext: safeDialContext,
			},
		}
	}
	if httpClient.CheckRedirect == nil {
		httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return errors.New("HTTP redirects are not permitted for external provider requests")
		}
	}

	wsDialer := cfg.WSUpgrader
	if wsDialer == nil {
		wsDialer = &websocket.Dialer{
			HandshakeTimeout: 5 * time.Second,
			NetDialContext:   safeDialContext,
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

	return TranslatePatchParamsToPixletSchema(patch.Params, includeScaleFilter, patch.Seeded)
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

	// 1. Activate the target patch in screeny-studio ONLY if it is not already active
	// or if periodic re-sync is due (every 5 minutes in case studio restarted).
	// Preserves continuous procedural generation (e.g. Overland, Knot) across chunk cycles.
	a.activePatchMu.Lock()
	patchChanged := a.activePatchID != req.AppID || time.Since(a.lastPatchActivated) > 5*time.Minute
	if patchChanged {
		a.activePatchID = req.AppID
		a.lastParams = make(map[string]float32)
		a.lastSeed = 0
	}
	if a.lastParams == nil {
		a.lastParams = make(map[string]float32)
	}
	a.activePatchMu.Unlock()

	if patchChanged {
		if err := a.activatePatch(ctx, req.AppID); err != nil {
			a.activePatchMu.Lock()
			a.activePatchID = ""
			a.lastParams = make(map[string]float32)
			a.lastSeed = 0
			a.activePatchMu.Unlock()
			return nil, fmt.Errorf("activating patch %s: %w", req.AppID, err)
		}
		a.activePatchMu.Lock()
		a.lastPatchActivated = time.Now()
		a.activePatchMu.Unlock()
	}

	transitionOccurred := patchChanged

	// 2. Manage random or fixed seed for generative patches
	if a.isPatchSeeded(ctx, req.AppID) {
		fixedSeed, hasFixedSeed := parseSeedValue(req.Config["seed"])
		if hasFixedSeed {
			a.activePatchMu.Lock()
			needsSeed := patchChanged || a.lastSeed != fixedSeed
			a.activePatchMu.Unlock()
			if needsSeed {
				if err := a.setSeed(ctx, fixedSeed); err != nil {
					slog.Warn("Failed to set screeny seed", "patch", req.AppID, "seed", fixedSeed, "error", err)
					a.activePatchMu.Lock()
					a.lastSeed = 0
					a.activePatchMu.Unlock()
				} else {
					a.activePatchMu.Lock()
					a.lastSeed = fixedSeed
					a.activePatchMu.Unlock()
					transitionOccurred = true
				}
			}
		} else {
			// No fixed seed provided: randomize seed on each render
			randomSeed := 1 + rand.Uint32N(math.MaxUint32-1)
			if err := a.setSeed(ctx, randomSeed); err != nil {
				slog.Warn("Failed to randomize screeny seed", "patch", req.AppID, "seed", randomSeed, "error", err)
				a.activePatchMu.Lock()
				a.lastSeed = 0
				a.activePatchMu.Unlock()
			} else {
				a.activePatchMu.Lock()
				a.lastSeed = randomSeed
				a.activePatchMu.Unlock()
				transitionOccurred = true
			}
		}
	}

	scaleFilter := a.scaleFilter
	if sf, ok := req.Config["scale_filter"].(string); ok && sf != "" && sf != "default" {
		scaleFilter = sf
	}

	// 3. Apply config parameters only when changed or on patch switch
	for paramID, val := range req.Config {
		if paramID == "scale_filter" || paramID == "seed" {
			continue // Handled internally by server-side scaling pipeline and seed management
		}
		if fVal, ok := parseParamValue(val); ok {
			a.activePatchMu.Lock()
			prevVal, exists := a.lastParams[paramID]
			needsSet := patchChanged || !exists || prevVal != fVal
			a.activePatchMu.Unlock()
			if needsSet {
				if err := a.setParam(ctx, paramID, fVal); err != nil {
					slog.Warn("Failed to set screeny parameter", "param", paramID, "value", fVal, "error", err)
					a.activePatchMu.Lock()
					a.activePatchID = ""
					a.lastParams = make(map[string]float32)
					a.lastSeed = 0
					a.activePatchMu.Unlock()
				} else {
					a.activePatchMu.Lock()
					a.lastParams[paramID] = fVal
					a.activePatchMu.Unlock()
				}
			}
		}
	}

	// 4. Connect to screeny-studio WebSocket preview stream and capture frames.
	// For full renders (>= 5000ms), reserve ~1.2s of pipeline headroom so live capture +
	// lossless WebP encoding + WebSocket transmission finishes before the previous cycle's
	// dwell_secs expires on the device, preventing buffer starvation and frame looping.
	captureDurationMs := durationMs
	if durationMs >= 5000 {
		captureDurationMs = max(durationMs-1200, durationMs*85/100)
	}
	frames, err := a.captureFrames(ctx, fps, captureDurationMs, req.Width, req.Height, scaleFilter, transitionOccurred)
	if err != nil {
		a.activePatchMu.Lock()
		a.activePatchID = ""
		a.lastParams = make(map[string]float32)
		a.lastSeed = 0
		a.activePatchMu.Unlock()
		return nil, fmt.Errorf("capturing screeny frames: %w", err)
	}

	if len(frames) == 0 {
		a.activePatchMu.Lock()
		a.activePatchID = ""
		a.lastParams = make(map[string]float32)
		a.lastSeed = 0
		a.activePatchMu.Unlock()
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

// setSeed calls POST /api/v1/set_seed on screeny-studio.
func (a *ScreenyAdapter) setSeed(ctx context.Context, seed uint32) error {
	u := fmt.Sprintf("%s/api/v1/set_seed", a.studioURL)
	payload, err := json.Marshal(map[string]uint32{
		"seed": seed,
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
		return fmt.Errorf("set_seed status %d", resp.StatusCode)
	}

	return nil
}

// isPatchSeeded returns true if the specified patch supports seed-based procedural generation.
func (a *ScreenyAdapter) isPatchSeeded(ctx context.Context, patchID string) bool {
	a.cacheMu.RLock()
	patch, found := a.cachedPatch[patchID]
	a.cacheMu.RUnlock()

	if !found {
		if _, err := a.ListApps(ctx); err == nil {
			a.cacheMu.RLock()
			patch, found = a.cachedPatch[patchID]
			a.cacheMu.RUnlock()
		}
	}

	return found && patch.Seeded
}

// captureFrames opens a WebSocket to /api/v1/ws and collects frames for durationMs.
func (a *ScreenyAdapter) captureFrames(ctx context.Context, fps, durationMs, targetWidth, targetHeight int, scaleFilter string, discardTransition bool) ([]*image.RGBA, error) {
	u, err := url.Parse(a.studioURL)
	if err != nil {
		return nil, fmt.Errorf("parsing studio URL: %w", err)
	}

	wsScheme := "ws"
	if u.Scheme == "https" {
		wsScheme = "wss"
	}
	wsU := *u
	wsU.Scheme = wsScheme
	wsU.Path = strings.TrimRight(u.Path, "/") + "/api/v1/ws"
	wsU.RawQuery = fmt.Sprintf("fps=%d&repeat=true&overview=false", fps)
	wsURL := wsU.String()

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
	if discardTransition && a.transitionSkipSec > 0 {
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

		// Screeny Studio performs a 2.0-second crossfade when switching patches or setting seeds.
		// While timer < transitionSkipSec, discard blend frames only if a transition just occurred.
		if discardTransition && len(msgData) == PreviewPacketLen && a.transitionSkipSec > 0 {
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

// parseSeedValue extracts a user-configured seed if present, positive, and <= math.MaxUint32.
// Returns (seed, true) if a specific fixed seed is configured, or (0, false) if
// the seed should be randomized (e.g. empty, "0", "random", "auto", out-of-range, or omitted).
func parseSeedValue(val any) (uint32, bool) {
	if val == nil {
		return 0, false
	}
	switch v := val.(type) {
	case int:
		if v > 0 && int64(v) <= math.MaxUint32 {
			return uint32(v), true
		}
	case int32:
		if v > 0 {
			return uint32(v), true
		}
	case int64:
		if v > 0 && v <= math.MaxUint32 {
			return uint32(v), true
		}
	case uint:
		if v > 0 && uint64(v) <= math.MaxUint32 {
			return uint32(v), true
		}
	case uint32:
		if v > 0 {
			return v, true
		}
	case uint64:
		if v > 0 && v <= math.MaxUint32 {
			return uint32(v), true
		}
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v <= math.MaxUint32 && v == math.Trunc(v) {
			return uint32(v), true
		}
	case float32:
		f64 := float64(v)
		if !math.IsNaN(f64) && !math.IsInf(f64, 0) && f64 > 0 && f64 <= math.MaxUint32 && f64 == math.Trunc(f64) {
			return uint32(v), true
		}
	case json.Number:
		if n, err := strconv.ParseUint(string(v), 10, 32); err == nil && n > 0 {
			return uint32(n), true
		}
		if f, err := v.Float64(); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && f > 0 && f <= math.MaxUint32 && f == math.Trunc(f) {
			return uint32(f), true
		}
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" || strings.EqualFold(trimmed, "random") || strings.EqualFold(trimmed, "auto") {
			return 0, false
		}
		if n, err := strconv.ParseUint(trimmed, 10, 32); err == nil && n > 0 {
			return uint32(n), true
		}
	}
	return 0, false
}
