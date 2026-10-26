package chunker

import (
	"errors"
	"fmt"
	"image"
	"time"

	"github.com/tronbyt/go-libwebp/webp"
)

// RenderChunk represents a single WebP chunk and its exact playback duration.
type RenderChunk struct {
	WebPBytes     []byte
	DurationMs    int    // Exact playback duration of this chunk's frames
	FullWebPBytes []byte // Complete animation across all chunks for web preview
}

// DefaultFPS is 30 frames per second.
const DefaultFPS = 30

// EncodeFramesToWebP encodes a sequence of RGBA images into an animated WebP binary.
func EncodeFramesToWebP(frames []*image.RGBA, frameDuration time.Duration) ([]byte, error) {
	if len(frames) == 0 {
		return nil, errors.New("cannot encode empty frame slice")
	}

	bounds := frames[0].Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	encoder, err := webp.NewAnimationEncoder(width, height, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("initializing webp animation encoder: %w", err)
	}
	defer encoder.Close()

	config, err := webp.ConfigLosslessPreset(6)
	if err != nil {
		return nil, fmt.Errorf("creating lossless config: %w", err)
	}

	for _, frame := range frames {
		if err := encoder.AddFrame(frame, frameDuration, config); err != nil {
			return nil, fmt.Errorf("adding frame to webp animation: %w", err)
		}
	}

	assembled, err := encoder.Assemble()
	if err != nil {
		return nil, fmt.Errorf("assembling webp animation: %w", err)
	}

	return assembled, nil
}

// EncodeFramesToWebPWithDuration encodes a sequence of RGBA images into an animated WebP binary
// whose frame timestamps span targetDurationMs evenly, with an optional holdLastFrameMs cushion
// added to the final frame so hardware decoders (ESP32 and Tronberry) never hit EOF and reset
// to frame 0 before dwell_secs expires.
func EncodeFramesToWebPWithDuration(frames []*image.RGBA, targetDurationMs int, holdLastFrameMs int) ([]byte, error) {
	if len(frames) == 0 {
		return nil, errors.New("cannot encode empty frame slice")
	}

	bounds := frames[0].Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	encoder, err := webp.NewAnimationEncoder(width, height, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("initializing webp animation encoder: %w", err)
	}
	defer encoder.Close()

	config, err := webp.ConfigLosslessPreset(6)
	if err != nil {
		return nil, fmt.Errorf("creating lossless config: %w", err)
	}

	n := len(frames)
	if targetDurationMs <= 0 {
		targetDurationMs = n * (1000 / DefaultFPS)
	}

	for i, frame := range frames {
		tStart := (i * targetDurationMs) / n
		tEnd := ((i + 1) * targetDurationMs) / n
		durMs := max(1, tEnd-tStart)
		if i == n-1 && n > 1 && holdLastFrameMs > 0 {
			durMs += holdLastFrameMs
		}
		if err := encoder.AddFrame(frame, time.Duration(durMs)*time.Millisecond, config); err != nil {
			return nil, fmt.Errorf("adding frame to webp animation: %w", err)
		}
	}

	assembled, err := encoder.Assemble()
	if err != nil {
		return nil, fmt.Errorf("assembling webp animation: %w", err)
	}

	return assembled, nil
}

// SplitFramesIntoChunks partitions sequential frames into animated WebP chunks,
// each fitting under maxChunkSize bytes and maxChunkDurationMs with exact playback DurationMs.
func SplitFramesIntoChunks(frames []*image.RGBA, fps int, maxChunkSize int, maxChunkDurationMs int) ([]RenderChunk, error) {
	return SplitFramesIntoChunksWithDuration(frames, fps, maxChunkSize, maxChunkDurationMs, 0)
}

// SplitFramesIntoChunksWithDuration partitions sequential frames into animated WebP chunks.
// When targetDurationMs > 0, frame timestamps are paced across exact integer-second dwell windows
// with a final-frame hold cushion so hardware decoders never loop back to frame 0 mid-dwell.
func SplitFramesIntoChunksWithDuration(frames []*image.RGBA, fps int, maxChunkSize int, maxChunkDurationMs int, targetDurationMs int) ([]RenderChunk, error) {
	if len(frames) == 0 {
		return nil, errors.New("no frames to split")
	}

	if fps <= 0 {
		fps = DefaultFPS
	}

	frameDurationMs := 1000 / fps
	if frameDurationMs <= 0 {
		frameDurationMs = 1
	}
	frameDuration := time.Duration(frameDurationMs) * time.Millisecond

	totalFrames := len(frames)
	totalSecs := 0
	holdMs := 0
	if targetDurationMs > 0 {
		totalSecs = max(1, (targetDurationMs+500)/1000)
		if targetDurationMs >= 5000 {
			holdMs = 250
		}
	}

	encodeRange := func(slice []*image.RGBA, durMs int, hold int) ([]byte, error) {
		if targetDurationMs > 0 {
			return EncodeFramesToWebPWithDuration(slice, durMs, hold)
		}
		return EncodeFramesToWebP(slice, frameDuration)
	}

	fullDurationMs := totalFrames * frameDurationMs
	if totalSecs > 0 {
		fullDurationMs = totalSecs * 1000
	}

	// 1. If maxChunkSize <= 0, don't chunk: encode everything into a single chunk.
	if maxChunkSize <= 0 {
		b, err := encodeRange(frames, fullDurationMs, holdMs)
		if err != nil {
			return nil, err
		}
		return []RenderChunk{
			{
				WebPBytes:     b,
				DurationMs:    fullDurationMs,
				FullWebPBytes: b,
			},
		}, nil
	}

	maxFramesPerChunk := 0
	if maxChunkDurationMs > 0 {
		maxFramesPerChunk = (maxChunkDurationMs * fps) / 1000
	}

	// 2. Try encoding the entire animation in one chunk first if it fits within bounds.
	allBytes, err := encodeRange(frames, fullDurationMs, holdMs)
	if err == nil && len(allBytes) <= maxChunkSize && (maxFramesPerChunk <= 0 || totalFrames <= maxFramesPerChunk) {
		return []RenderChunk{
			{
				WebPBytes:     allBytes,
				DurationMs:    fullDurationMs,
				FullWebPBytes: allBytes,
			},
		}, nil
	}

	// 3. Balanced Multi-Chunk Partitioning:
	// Find the minimum number of chunks K (>= 2) such that all chunks fit under maxChunkSize
	// and maxFramesPerChunk, with balanced frame counts and durations across all chunks.
	// This prevents "straggler / runt" last chunks that loop prematurely on hardware,
	// and eliminates holdLastFrameMs on intermediate chunks to avoid visible freezing seams.
	minFramesPerChunk := max(15, fps) // At least ~1-1.5s per chunk
	if totalFrames < minFramesPerChunk*2 {
		minFramesPerChunk = max(1, totalFrames/2)
	}

	maxPossibleK := max(2, totalFrames/minFramesPerChunk)
	if totalSecs > 0 {
		maxPossibleK = min(maxPossibleK, totalSecs)
	}

	startK := 2
	if maxFramesPerChunk > 0 {
		startK = max(startK, (totalFrames+maxFramesPerChunk-1)/maxFramesPerChunk)
	}
	if len(allBytes) > maxChunkSize {
		startK = max(startK, (len(allBytes)+maxChunkSize-1)/maxChunkSize)
	}

	// Try balanced K-way partitioning
	for k := startK; k <= maxPossibleK; k++ {
		valid := true
		candidateChunks := make([]RenderChunk, k)

		for i := range k {
			cStart := (i * totalFrames) / k
			cEnd := ((i + 1) * totalFrames) / k
			cSlice := frames[cStart:cEnd]

			var cDurMs int
			if totalSecs > 0 {
				tStart := (i * totalSecs) / k
				tEnd := ((i + 1) * totalSecs) / k
				cDurMs = max(1, tEnd-tStart) * 1000
			} else {
				cDurMs = (cEnd - cStart) * frameDurationMs
			}

			// ONLY the final chunk of the full dwell window receives the holdMs cushion!
			// Intermediate chunks must have hold = 0 so transitions are 100% seamless without pause.
			cHold := 0
			if i == k-1 {
				cHold = holdMs
			}

			b, err := encodeRange(cSlice, cDurMs, cHold)
			if err != nil || len(b) > maxChunkSize || (maxFramesPerChunk > 0 && len(cSlice) > maxFramesPerChunk) {
				valid = false
				break
			}

			candidateChunks[i] = RenderChunk{
				WebPBytes:  b,
				DurationMs: cDurMs,
			}
		}

		if valid {
			// Ensure sum of chunk durations equals target fullDurationMs
			if totalSecs > 0 && len(candidateChunks) > 0 {
				durSum := 0
				for _, ch := range candidateChunks {
					durSum += ch.DurationMs
				}
				diff := (totalSecs * 1000) - durSum
				if diff != 0 {
					candidateChunks[len(candidateChunks)-1].DurationMs += diff
				}
			}
			candidateChunks[0].FullWebPBytes = allBytes
			return candidateChunks, nil
		}
	}

	// Fallback: adaptive partitioning with runt defense if uniform K-way partitioning failed
	var chunks []RenderChunk
	start := 0
	remainingSecs := totalSecs

	chunkDurationFor := func(chunkStart, chunkEnd int) int {
		chunkFrames := chunkEnd - chunkStart
		if totalSecs <= 0 {
			return chunkFrames * frameDurationMs
		}
		remFrames := totalFrames - chunkStart
		if chunkEnd >= totalFrames || remainingSecs <= 1 || remFrames <= 0 {
			return max(1, remainingSecs) * 1000
		}
		secs := max(1, (chunkFrames*remainingSecs+remFrames/2)/remFrames)
		if secs >= remainingSecs {
			secs = max(1, remainingSecs-1)
		}
		return secs * 1000
	}

	for start < totalFrames {
		maxEnd := totalFrames
		if maxFramesPerChunk > 0 && start+maxFramesPerChunk < totalFrames {
			maxEnd = start + maxFramesPerChunk
		}

		// Runt defense: if remaining frames would leave an awkward straggler, split evenly
		remaining := totalFrames - start
		if remaining > minFramesPerChunk && remaining < minFramesPerChunk*2 {
			maxEnd = start + remaining/2
		}

		candDurMs := chunkDurationFor(start, maxEnd)
		cHold := 0
		if maxEnd >= totalFrames {
			cHold = holdMs
		}

		candidateAll := frames[start:maxEnd]
		encodedAll, err := encodeRange(candidateAll, candDurMs, cHold)
		if err == nil && len(encodedAll) <= maxChunkSize {
			chunks = append(chunks, RenderChunk{
				WebPBytes:  encodedAll,
				DurationMs: candDurMs,
			})
			if totalSecs > 0 {
				remainingSecs = max(1, remainingSecs-candDurMs/1000)
			}
			start = maxEnd
			continue
		}

		low := start + 1
		high := maxEnd
		bestEnd := low
		var bestBytes []byte
		bestDurMs := chunkDurationFor(start, bestEnd)

		for low <= high {
			mid := (low + high) / 2
			chunkCandidate := frames[start:mid]
			midDurMs := chunkDurationFor(start, mid)
			midHold := 0
			if mid >= totalFrames {
				midHold = holdMs
			}
			encoded, err := encodeRange(chunkCandidate, midDurMs, midHold)
			if err == nil && len(encoded) <= maxChunkSize {
				bestEnd = mid
				bestBytes = encoded
				bestDurMs = midDurMs
				low = mid + 1 // try to fit more frames
			} else {
				high = mid - 1 // too big or encode failed; try fewer frames
			}
		}

		if bestBytes == nil {
			// Even a single frame exceeded maxChunkSize!
			bestEnd = start + 1
			bestDurMs = chunkDurationFor(start, bestEnd)
			fallbackHold := 0
			if bestEnd >= totalFrames {
				fallbackHold = holdMs
			}
			bestBytes, err = encodeRange(frames[start:bestEnd], bestDurMs, fallbackHold)
			if err != nil {
				return nil, fmt.Errorf("encoding single-frame fallback at frame %d: %w", start, err)
			}
			if len(bestBytes) > maxChunkSize {
				return nil, fmt.Errorf("single frame %d is %d bytes, exceeding maximum chunk budget of %d bytes", start, len(bestBytes), maxChunkSize)
			}
		}

		chunks = append(chunks, RenderChunk{
			WebPBytes:  bestBytes,
			DurationMs: bestDurMs,
		})
		if totalSecs > 0 {
			remainingSecs = max(1, remainingSecs-bestDurMs/1000)
		}
		start = bestEnd
	}

	if len(chunks) > 0 {
		chunks[0].FullWebPBytes = allBytes
	}

	return chunks, nil
}

// DecodeWebPAnimation decodes an animated (or static) WebP binary into a slice of RGBA frames and calculates FPS.
func DecodeWebPAnimation(data []byte) ([]*image.RGBA, int, error) {
	if len(data) == 0 {
		return nil, 0, errors.New("cannot decode empty webp data")
	}

	decoder, err := webp.NewAnimationDecoder(data)
	if err != nil {
		return nil, 0, fmt.Errorf("initializing webp animation decoder: %w", err)
	}
	defer decoder.Close()

	anim, err := decoder.Decode()
	if err != nil {
		return nil, 0, fmt.Errorf("decoding webp animation: %w", err)
	}
	if len(anim.Image) == 0 {
		return nil, 0, errors.New("decoded zero frames from webp animation")
	}

	fps := DefaultFPS
	n := len(anim.Image)
	if n > 1 && len(anim.Timestamp) == n {
		totalMs := anim.Timestamp[n-1]
		if totalMs > 0 {
			fps = max(1, (n*1000)/totalMs)
		}
	}

	return anim.Image, fps, nil
}

// SplitWebPBytesIntoChunks inspects an encoded WebP binary (such as from Pixlet / Starlark).
// If the binary already fits within maxChunkSize and maxChunkDurationMs, it returns a single chunk.
// Otherwise, it decodes the animation into frames and partitions them into sequential chunks
// adhering to maxChunkSize and maxChunkDurationMs, preserving the original data in FullWebPBytes.
func SplitWebPBytesIntoChunks(data []byte, maxChunkSize int, maxChunkDurationMs int, targetDurationMs int) ([]RenderChunk, error) {
	if len(data) == 0 {
		return nil, errors.New("cannot chunk empty webp data")
	}

	durMs := targetDurationMs
	if durMs <= 0 {
		durMs = 15000
	}

	// If no chunk budget or the data already fits within budget and duration
	if (maxChunkSize <= 0 || len(data) <= maxChunkSize) && maxChunkDurationMs <= 0 {
		return []RenderChunk{
			{
				WebPBytes:     data,
				DurationMs:    durMs,
				FullWebPBytes: data,
			},
		}, nil
	}

	// Decode WebP to RGBA frames
	frames, fps, err := DecodeWebPAnimation(data)
	if err != nil {
		return nil, fmt.Errorf("decoding webp for chunking: %w", err)
	}

	// Single frame cannot be split across time
	if len(frames) <= 1 {
		return []RenderChunk{
			{
				WebPBytes:     data,
				DurationMs:    durMs,
				FullWebPBytes: data,
			},
		}, nil
	}

	// Partition frames into sequential chunks
	chunks, err := SplitFramesIntoChunksWithDuration(frames, fps, maxChunkSize, maxChunkDurationMs, targetDurationMs)
	if err != nil {
		return nil, err
	}

	// Preserve the original high-fidelity render in FullWebPBytes for web previews
	if len(chunks) > 0 && len(chunks[0].FullWebPBytes) == 0 {
		chunks[0].FullWebPBytes = data
	}

	return chunks, nil
}
