package chunker

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tronbyt/go-libwebp/webp"
)

func makeTestFrames(n int, width, height int) []*image.RGBA {
	frames := make([]*image.RGBA, n)
	for i := range n {
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		// Gradient so frames don't compress away to nothing
		for y := range height {
			for x := range width {
				img.SetRGBA(x, y, color.RGBA{
					R: uint8((x*4 + i*7) % 256),
					G: uint8((y*8 + i*3) % 256),
					B: uint8((x + y + i*11) % 256),
					A: 255,
				})
			}
		}
		frames[i] = img
	}
	return frames
}

func TestChunker_DecodeWebPAnimation(t *testing.T) {
	frames := makeTestFrames(15, 64, 32)
	encoded, err := EncodeFramesToWebP(frames, 100*time.Millisecond)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	decodedFrames, fps, err := DecodeWebPAnimation(encoded)
	require.NoError(t, err)
	assert.Len(t, decodedFrames, 15)
	assert.Greater(t, fps, 0)
}

func TestChunker_SplitWebPBytesIntoChunks_Unconstrained(t *testing.T) {
	frames := makeTestFrames(10, 64, 32)
	encoded, err := EncodeFramesToWebP(frames, 100*time.Millisecond)
	require.NoError(t, err)

	// maxChunkSize = 0 (unconstrained)
	chunks, err := SplitWebPBytesIntoChunks(encoded, 0, 0, 1000)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	assert.Equal(t, encoded, chunks[0].WebPBytes)
	assert.Equal(t, encoded, chunks[0].FullWebPBytes)
	assert.Equal(t, 1000, chunks[0].DurationMs)
}

func TestChunker_SplitWebPBytesIntoChunks_Partitioning(t *testing.T) {
	// 60 frames with distinct pixel noise
	frames := makeTestFrames(60, 64, 32)
	encoded, err := EncodeFramesToWebP(frames, 50*time.Millisecond)
	require.NoError(t, err)

	fullSize := len(encoded)
	require.Greater(t, fullSize, 1000)

	// Constrain chunk size to 1/2 of full size to force multi-chunk
	maxChunk := fullSize / 2
	chunks, err := SplitWebPBytesIntoChunks(encoded, maxChunk, 0, 3000)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(chunks), 2, "Expected at least 2 chunks")

	for i, ch := range chunks {
		assert.LessOrEqual(t, len(ch.WebPBytes), maxChunk, "Chunk %d size %d exceeds %d", i, len(ch.WebPBytes), maxChunk)
		assert.Greater(t, ch.DurationMs, 0)
	}
	assert.Equal(t, encoded, chunks[0].FullWebPBytes, "Original full WebP must be preserved in chunk 0")
}

func TestChunker_SplitWebPBytesIntoChunks_SingleFrame(t *testing.T) {
	frames := makeTestFrames(1, 64, 32)
	encoded, err := EncodeFramesToWebP(frames, 100*time.Millisecond)
	require.NoError(t, err)

	chunks, err := SplitWebPBytesIntoChunks(encoded, 100, 0, 5000)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	assert.Equal(t, encoded, chunks[0].WebPBytes)
	assert.Equal(t, 5000, chunks[0].DurationMs)
}

func TestChunker_NoIntermediateFreezesOrStragglers(t *testing.T) {
	// 90 frames @ 30 FPS = 3.0s total dwell
	frames := makeTestFrames(90, 64, 32)
	encoded, err := EncodeFramesToWebP(frames, 33*time.Millisecond)
	require.NoError(t, err)

	fullSize := len(encoded)
	maxChunk := fullSize / 2 // Forces at least 2 chunks

	chunks, err := SplitFramesIntoChunksWithDuration(frames, 30, maxChunk, 0, 3000)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(chunks), 2)

	// 1. Verify durations are balanced and sum to target
	durSum := 0
	for i, ch := range chunks {
		durSum += ch.DurationMs
		// No chunk should have a duration < 500ms
		assert.GreaterOrEqual(t, ch.DurationMs, 500, "Chunk %d is a straggler with duration %d ms", i, ch.DurationMs)
	}
	assert.Equal(t, 3000, durSum, "Sum of chunk durations must equal targetDurationMs")

	// 2. Decode intermediate chunk and verify it does NOT have holdLastFrameMs cushion
	// (its last frame duration should be normal frame duration, preventing freeze seams)
	if len(chunks) > 1 {
		chunk0Anim, err := webp.NewAnimationDecoder(chunks[0].WebPBytes)
		require.NoError(t, err)
		defer chunk0Anim.Close()
		anim, err := chunk0Anim.Decode()
		require.NoError(t, err)

		n := len(anim.Image)
		require.Greater(t, n, 10, "Chunk 0 should have substantial frames, not a tiny slice")

		// Timestamp difference between last two frames of Chunk 0
		lastFrameDur := anim.Timestamp[n-1] - anim.Timestamp[n-2]
		firstFrameDur := anim.Timestamp[1] - anim.Timestamp[0]

		// The last frame duration should match the normal frame duration (within 5ms jitter), NOT +250ms freeze
		assert.InDelta(t, firstFrameDur, lastFrameDur, 10, "Intermediate chunk must not have hold cushion freeze on last frame")
	}
}

func TestChunker_PreservesNaturalAnimationSpeed_NeverSquashes(t *testing.T) {
	// 60 frames at 100ms each = 6.0 seconds natural duration
	frames := makeTestFrames(60, 64, 32)
	encoded, err := EncodeFramesToWebP(frames, 100*time.Millisecond)
	require.NoError(t, err)

	fullSize := len(encoded)
	maxChunk := fullSize / 2 // Force split into 2 chunks

	// Target duration passed as 2000ms (smaller than 6000ms natural duration, e.g. device default dwell)
	// Chunker must NOT squash 6000ms into 2000ms (which would speed up playback by 3x)
	chunks, err := SplitWebPBytesIntoChunks(encoded, maxChunk, 0, 2000)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(chunks), 2)

	totalDur := 0
	for _, ch := range chunks {
		totalDur += ch.DurationMs
	}
	assert.GreaterOrEqual(t, totalDur, 5900, "Must preserve natural duration ~6000ms, not squashed to 2000ms")

	// Decode chunk 0 and check frame duration is still ~100ms, not 33ms
	chunk0Anim, err := webp.NewAnimationDecoder(chunks[0].WebPBytes)
	require.NoError(t, err)
	defer chunk0Anim.Close()
	anim, err := chunk0Anim.Decode()
	require.NoError(t, err)

	require.Greater(t, len(anim.Timestamp), 1)
	frameDur := anim.Timestamp[1] - anim.Timestamp[0]
	assert.InDelta(t, 100, frameDur, 10, "Frame duration must remain ~100ms, not squashed")
}
