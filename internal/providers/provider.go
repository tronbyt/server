package providers

import (
	"context"
	"net"

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

// IsBlockedMetadataIP returns true if the IP belongs to link-local, AWS IPv6 IMDS, or cloud metadata ranges.
func IsBlockedMetadataIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4.IsLinkLocalUnicast() || ip4.IsLinkLocalMulticast() {
			return true
		}
		if ip4.Equal(net.ParseIP("169.254.169.254")) || ip4.Equal(net.ParseIP("100.100.100.200")) {
			return true
		}
	}
	if ip.Equal(net.ParseIP("fd00:ec2::254")) {
		return true
	}
	return false
}
