package screeny

import (
	"image"
	"time"

	"tronbyt-server/internal/chunker"
	"tronbyt-server/internal/providers"
)

// DefaultFPS is 30 frames per second.
const DefaultFPS = chunker.DefaultFPS

// EncodeFramesToWebP encodes a sequence of RGBA images into an animated WebP binary.
func EncodeFramesToWebP(frames []*image.RGBA, frameDuration time.Duration) ([]byte, error) {
	return chunker.EncodeFramesToWebP(frames, frameDuration)
}

// EncodeFramesToWebPWithDuration encodes a sequence of RGBA images into an animated WebP binary
// whose frame timestamps span targetDurationMs evenly, with an optional holdLastFrameMs cushion
// added to the final frame so hardware decoders (ESP32 and Tronberry) never hit EOF and reset
// to frame 0 before dwell_secs expires.
func EncodeFramesToWebPWithDuration(frames []*image.RGBA, targetDurationMs int, holdLastFrameMs int) ([]byte, error) {
	return chunker.EncodeFramesToWebPWithDuration(frames, targetDurationMs, holdLastFrameMs)
}

// SplitFramesIntoChunks partitions sequential frames into animated WebP chunks,
// each fitting under maxChunkSize bytes and maxChunkDurationMs with exact playback DurationMs.
func SplitFramesIntoChunks(frames []*image.RGBA, fps int, maxChunkSize int, maxChunkDurationMs int) ([]providers.RenderChunk, error) {
	return chunker.SplitFramesIntoChunks(frames, fps, maxChunkSize, maxChunkDurationMs)
}

// SplitFramesIntoChunksWithDuration partitions sequential frames into animated WebP chunks.
// When targetDurationMs > 0, frame timestamps are paced across exact integer-second dwell windows
// with a final-frame hold cushion so hardware decoders never loop back to frame 0 mid-dwell.
func SplitFramesIntoChunksWithDuration(frames []*image.RGBA, fps int, maxChunkSize int, maxChunkDurationMs int, targetDurationMs int) ([]providers.RenderChunk, error) {
	return chunker.SplitFramesIntoChunksWithDuration(frames, fps, maxChunkSize, maxChunkDurationMs, targetDurationMs)
}
