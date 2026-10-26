package providers

import (
	"context"

	"tronbyt-server/internal/chunker"
)

type contextKey string

// Supports2xKey is the context key for passing 2x display support capability to providers.
const Supports2xKey contextKey = "supports2x"

// RenderChunk is an alias to chunker.RenderChunk for provider implementations.
type RenderChunk = chunker.RenderChunk

// RenderRequest specifies the rendering parameters for an external app.
type RenderRequest struct {
	AppID              string
	Width              int
	Height             int
	FPS                int
	DurationMs         int
	MaxChunkSize       int // e.g., 5MB (5242880) for tronberry/RPi, 450KB for S3/Wide, 200KB (200000) for Gen 1
	MaxChunkDurationMs int // Maximum duration in ms per chunk (e.g. 5000ms for ESP32 devices)
	Timezone           string
	Config             map[string]any
}

// ExternalAppMeta describes metadata for an external provider's app.
type ExternalAppMeta struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Summary    string   `json:"summary"`
	Desc       string   `json:"desc"`
	Author     string   `json:"author"`
	Provider   string   `json:"provider"`
	Supports2x bool     `json:"supports2x"`
	Tags       []string `json:"tags"`
	PreviewURL string   `json:"preview_url"`
}

// ExternalAppProvider defines the pluggable interface for third-party app stores / renderers.
type ExternalAppProvider interface {
	Name() string
	ListApps(ctx context.Context) ([]ExternalAppMeta, error)
	GetAppSchema(ctx context.Context, appID string) ([]byte, error)
	RenderAppChunks(ctx context.Context, req RenderRequest) ([]RenderChunk, error)
}
