package chunker

import (
	"errors"
	"fmt"
	"image"
	"sync"
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

// EncodeFramesToWebPWithFrameDurations encodes a sequence of RGBA images into an animated WebP binary
// using exact per-frame durations, preserving natural animation speed and preventing hardware watchdog
// timeouts caused by hyper-speed frame pacing.
func EncodeFramesToWebPWithFrameDurations(frames []*image.RGBA, durations []time.Duration, holdLastFrameMs int) ([]byte, error) {
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
	for i, frame := range frames {
		dur := time.Duration(1000/DefaultFPS) * time.Millisecond
		if i < len(durations) && durations[i] > 0 {
			dur = durations[i]
		}
		if i == n-1 && n > 1 && holdLastFrameMs > 0 {
			dur += time.Duration(holdLastFrameMs) * time.Millisecond
		}
		if err := encoder.AddFrame(frame, dur, config); err != nil {
			return nil, fmt.Errorf("adding frame to webp animation: %w", err)
		}
	}

	assembled, err := encoder.Assemble()
	if err != nil {
		return nil, fmt.Errorf("assembling webp animation: %w", err)
	}

	return assembled, nil
}

// SplitFramesWithTiming partitions sequential frames into animated WebP chunks, preserving
// the exact per-frame durations of the source animation so playback never speeds up or slows down.
func SplitFramesWithTiming(frames []*image.RGBA, durations []time.Duration, maxChunkSize int, maxChunkDurationMs int) ([]RenderChunk, error) {
	if len(frames) == 0 {
		return nil, errors.New("no frames to split")
	}

	n := len(frames)
	if len(durations) != n {
		defDur := time.Duration(1000/DefaultFPS) * time.Millisecond
		durations = make([]time.Duration, n)
		for i := range n {
			durations[i] = defDur
		}
	}

	fullDurationMs := 0
	for _, d := range durations {
		fullDurationMs += int(d.Milliseconds())
	}
	if fullDurationMs <= 0 {
		fullDurationMs = n * (1000 / DefaultFPS)
	}

	// 1. If maxChunkSize <= 0, don't chunk: encode everything into a single chunk.
	if maxChunkSize <= 0 {
		b, err := EncodeFramesToWebPWithFrameDurations(frames, durations, 0)
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

	// 2. Try encoding the entire animation in one chunk first if it fits within bounds.
	// If the requested animation duration exceeds maxChunkDurationMs, a single chunk is impossible;
	// skip synchronous monolithic encoding and encode it concurrently with the chunks.
	var allBytes []byte
	singleChunkPossible := maxChunkDurationMs <= 0 || fullDurationMs <= maxChunkDurationMs

	if singleChunkPossible {
		var err error
		allBytes, err = EncodeFramesToWebPWithFrameDurations(frames, durations, 0)
		if err == nil && (maxChunkSize <= 0 || len(allBytes) <= maxChunkSize) {
			return []RenderChunk{
				{
					WebPBytes:     allBytes,
					DurationMs:    fullDurationMs,
					FullWebPBytes: allBytes,
				},
			}, nil
		}
	}

	// 3. Balanced Multi-Chunk Partitioning:
	// Find the minimum number of chunks K (>= 2) such that all chunks fit under maxChunkSize
	// and maxChunkDurationMs, with balanced frame counts across chunks.
	minFramesPerChunk := 15
	if n < minFramesPerChunk*2 {
		minFramesPerChunk = max(1, n/2)
	}

	maxPossibleK := max(2, n/minFramesPerChunk)

	startK := 2
	if maxChunkDurationMs > 0 {
		startK = max(startK, (fullDurationMs+maxChunkDurationMs-1)/maxChunkDurationMs)
	}
	if len(allBytes) > maxChunkSize && maxChunkSize > 0 {
		startK = max(startK, (len(allBytes)+maxChunkSize-1)/maxChunkSize)
	}

	// If single chunk was impossible upfront, encode allBytes concurrently with chunk partitioning
	// so FullWebPBytes is populated without blocking the chunk pipeline.
	var allWg sync.WaitGroup
	if !singleChunkPossible && len(allBytes) == 0 {
		allWg.Go(func() {
			allBytes, _ = EncodeFramesToWebPWithFrameDurations(frames, durations, 0)
		})
	}

	// Try balanced K-way partitioning (encoding chunks in parallel across available CPU cores)
	for k := startK; k <= maxPossibleK; k++ {
		valid := true
		candidateChunks := make([]RenderChunk, k)

		type chunkResult struct {
			index int
			b     []byte
			durMs int
			err   error
		}
		results := make([]chunkResult, k)
		var wg sync.WaitGroup
		for i := range k {
			cStart := (i * n) / k
			cEnd := ((i + 1) * n) / k
			cSlice := frames[cStart:cEnd]
			cDurations := durations[cStart:cEnd]

			cDurMs := 0
			for _, d := range cDurations {
				cDurMs += int(d.Milliseconds())
			}

			wg.Go(func() {
				b, err := EncodeFramesToWebPWithFrameDurations(cSlice, cDurations, 0)
				results[i] = chunkResult{index: i, b: b, durMs: cDurMs, err: err}
			})
		}
		wg.Wait()

		for _, res := range results {
			if res.err != nil || len(res.b) > maxChunkSize || (maxChunkDurationMs > 0 && res.durMs > maxChunkDurationMs) {
				valid = false
				break
			}
			candidateChunks[res.index] = RenderChunk{
				WebPBytes:  res.b,
				DurationMs: res.durMs,
			}
		}

		if valid {
			allWg.Wait()
			candidateChunks[0].FullWebPBytes = allBytes
			return candidateChunks, nil
		}
	}

	// Fallback: adaptive sequential partitioning
	var chunks []RenderChunk
	start := 0

	for start < n {
		maxEnd := n
		remaining := n - start
		if remaining > minFramesPerChunk && remaining < minFramesPerChunk*2 {
			maxEnd = start + remaining/2
		}

		candidateSlice := frames[start:maxEnd]
		candidateDurations := durations[start:maxEnd]
		candDurMs := 0
		for _, d := range candidateDurations {
			candDurMs += int(d.Milliseconds())
		}

		encodedAll, err := EncodeFramesToWebPWithFrameDurations(candidateSlice, candidateDurations, 0)
		if err == nil && len(encodedAll) <= maxChunkSize && (maxChunkDurationMs <= 0 || candDurMs <= maxChunkDurationMs) {
			chunks = append(chunks, RenderChunk{
				WebPBytes:  encodedAll,
				DurationMs: candDurMs,
			})
			start = maxEnd
			continue
		}

		// Binary search to find max frames fitting in budget
		low := start + 1
		high := maxEnd
		bestEnd := low
		var bestBytes []byte
		bestDurMs := int(durations[start].Milliseconds())

		for low <= high {
			mid := low + (high-low)/2
			subSlice := frames[start:mid]
			subDurations := durations[start:mid]
			subDurMs := 0
			for _, d := range subDurations {
				subDurMs += int(d.Milliseconds())
			}

			b, err := EncodeFramesToWebPWithFrameDurations(subSlice, subDurations, 0)
			if err == nil && len(b) <= maxChunkSize && (maxChunkDurationMs <= 0 || subDurMs <= maxChunkDurationMs) {
				bestEnd = mid
				bestBytes = b
				bestDurMs = subDurMs
				low = mid + 1
			} else {
				high = mid - 1
			}
		}

		if bestBytes == nil {
			bestEnd = start + 1
			bestDurMs = int(durations[start].Milliseconds())
			var err error
			bestBytes, err = EncodeFramesToWebPWithFrameDurations(frames[start:bestEnd], durations[start:bestEnd], 0)
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
		start = bestEnd
	}

	if len(chunks) > 0 {
		chunks[0].FullWebPBytes = allBytes
	}
	return chunks, nil
}

// SplitFramesIntoChunks partitions sequential frames into animated WebP chunks,
// each fitting under maxChunkSize bytes and maxChunkDurationMs with exact playback DurationMs.
func SplitFramesIntoChunks(frames []*image.RGBA, fps int, maxChunkSize int, maxChunkDurationMs int) ([]RenderChunk, error) {
	return SplitFramesIntoChunksWithDuration(frames, fps, maxChunkSize, maxChunkDurationMs, 0)
}

// SplitFramesIntoChunksWithDuration partitions sequential frames into animated WebP chunks.
// When targetDurationMs is greater than the frames' natural duration, frames are padded or paced
// to fill targetDurationMs. It never squashes frames below their natural duration, preventing hyper-speed
// playback and FreeRTOS task watchdog timeouts.
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

	n := len(frames)
	durations := make([]time.Duration, n)
	for i := range n {
		durations[i] = frameDuration
	}

	naturalDurMs := n * frameDurationMs
	if targetDurationMs > naturalDurMs {
		for i := range n {
			tStart := (i * targetDurationMs) / n
			tEnd := ((i + 1) * targetDurationMs) / n
			durMs := max(1, tEnd-tStart)
			durations[i] = time.Duration(durMs) * time.Millisecond
		}
	}

	return SplitFramesWithTiming(frames, durations, maxChunkSize, maxChunkDurationMs)
}

// DecodeWebPAnimationWithDurations decodes an animated (or static) WebP binary into a slice of RGBA frames,
// extracting each frame's exact duration and the overall FPS from the animation metadata.
func DecodeWebPAnimationWithDurations(data []byte) ([]*image.RGBA, []time.Duration, int, error) {
	if len(data) == 0 {
		return nil, nil, 0, errors.New("cannot decode empty webp data")
	}

	decoder, err := webp.NewAnimationDecoder(data)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("initializing webp animation decoder: %w", err)
	}
	defer decoder.Close()

	anim, err := decoder.Decode()
	if err != nil {
		return nil, nil, 0, fmt.Errorf("decoding webp animation: %w", err)
	}
	if len(anim.Image) == 0 {
		return nil, nil, 0, errors.New("decoded zero frames from webp animation")
	}

	n := len(anim.Image)
	fps := DefaultFPS
	durations := make([]time.Duration, n)

	if n > 1 && len(anim.Timestamp) == n {
		totalMs := anim.Timestamp[n-1]
		if totalMs > 0 {
			fps = max(1, (n*1000)/totalMs)
		}
		for i := range n {
			var durMs int
			if i == 0 {
				durMs = anim.Timestamp[0]
			} else {
				durMs = anim.Timestamp[i] - anim.Timestamp[i-1]
			}
			if durMs <= 0 {
				durMs = max(1, 1000/fps)
			}
			durations[i] = time.Duration(durMs) * time.Millisecond
		}
	} else {
		defDur := time.Duration(max(1, 1000/fps)) * time.Millisecond
		for i := range n {
			durations[i] = defDur
		}
	}

	return anim.Image, durations, fps, nil
}

// DecodeWebPAnimation decodes an animated (or static) WebP binary into a slice of RGBA frames and calculates FPS.
func DecodeWebPAnimation(data []byte) ([]*image.RGBA, int, error) {
	frames, _, fps, err := DecodeWebPAnimationWithDurations(data)
	return frames, fps, err
}

// SplitWebPBytesIntoChunks inspects an encoded WebP binary (such as from Pixlet / Starlark).
// If the binary already fits within maxChunkSize and maxChunkDurationMs, it returns a single chunk.
// Otherwise, it decodes the animation into frames and partitions them into sequential chunks
// adhering to maxChunkSize and maxChunkDurationMs, preserving the original frame timing so animations
// do not speed up or trigger hardware task watchdog timeouts.
func SplitWebPBytesIntoChunks(data []byte, maxChunkSize int, maxChunkDurationMs int, targetDurationMs int) ([]RenderChunk, error) {
	if len(data) == 0 {
		return nil, errors.New("cannot chunk empty webp data")
	}

	frames, durations, _, err := DecodeWebPAnimationWithDurations(data)
	if err != nil {
		return nil, fmt.Errorf("decoding webp for chunking: %w", err)
	}

	naturalDurMs := 0
	for _, d := range durations {
		naturalDurMs += int(d.Milliseconds())
	}
	totalDurMs := max(naturalDurMs, targetDurationMs)
	if totalDurMs <= 0 {
		totalDurMs = 15000
	}

	// If no chunk budget or the data already fits within budget and duration
	if (maxChunkSize <= 0 || len(data) <= maxChunkSize) && (maxChunkDurationMs <= 0 || totalDurMs <= maxChunkDurationMs) {
		return []RenderChunk{
			{
				WebPBytes:     data,
				DurationMs:    totalDurMs,
				FullWebPBytes: data,
			},
		}, nil
	}

	// Single frame cannot be split across time
	if len(frames) <= 1 {
		dur := targetDurationMs
		if dur <= 0 {
			dur = totalDurMs
		}
		return []RenderChunk{
			{
				WebPBytes:     data,
				DurationMs:    dur,
				FullWebPBytes: data,
			},
		}, nil
	}

	// If targetDurationMs is specified and LONGER than the animation's natural duration,
	// stretch frames to fill targetDurationMs. Never squash below natural duration!
	if targetDurationMs > naturalDurMs && len(frames) > 0 {
		n := len(frames)
		durations = make([]time.Duration, n)
		for i := range n {
			tStart := (i * targetDurationMs) / n
			tEnd := ((i + 1) * targetDurationMs) / n
			durations[i] = time.Duration(max(1, tEnd-tStart)) * time.Millisecond
		}
	}

	// Partition frames into sequential chunks preserving exact frame durations
	chunks, err := SplitFramesWithTiming(frames, durations, maxChunkSize, maxChunkDurationMs)
	if err != nil {
		return nil, err
	}

	// Preserve the original high-fidelity render in FullWebPBytes for web previews
	if len(chunks) > 0 && len(chunks[0].FullWebPBytes) == 0 {
		chunks[0].FullWebPBytes = data
	}

	return chunks, nil
}
