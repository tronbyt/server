package screeny

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
)

const (
	// PanelWidth is the Screeny panel width in pixels.
	PanelWidth = 64
	// PanelHeight is the Screeny panel height in pixels.
	PanelHeight = 32
	// NPixels is total pixels per 64x32 frame.
	NPixels = PanelWidth * PanelHeight // 2048
	// NBytes is total raw RGB bytes per 64x32 frame.
	NBytes = NPixels * 3 // 6144

	// Magic is the Screeny wire protocol magic byte ('s' = 0x53).
	Magic            = 0x53 // 's'
	ProtocolVersion  = 1
	TypeFrame        = 0x0
	TypeControl      = 0x2
	HeaderLen        = 8
	PreviewHeaderLen = 52
	PreviewPacketLen = PreviewHeaderLen + NBytes // 6196

	// FlagKey marks a keyframe.
	FlagKey      = 0x01
	FlagStatsReq = 0x02
	FlagFinal    = 0x04
	FlagHasTS    = 0x08

	// CodecPAL5 represents 32-color indexed palette codec.
	CodecPAL5    = 0x02
	CodecPAL8_LZ = 0x10
	CodecPAL4_LZ = 0x11
	CodecBC1Dual = 0x28
	CodecSolid   = 0x7F

	PAL5Len    = 32*3 + NPixels/2 + NPixels/8 // 1376
	BC1DualLen = 16 + 128*10                  // 1296
	SolidLen   = 3
)

// DecodeScreenyFrame parses and decodes a Screeny frame payload into a 64x32 *image.RGBA.
func DecodeScreenyFrame(data []byte) (*image.RGBA, error) {
	return decodeScreenyFrame(data)
}

// decodeScreenyFrame decodes incoming frame packets (either UDP screeny-protocol wire format,
// Studio WebSocket preview format, or raw RGB888) into a 64x32 RGBA image.
func decodeScreenyFrame(data []byte) (*image.RGBA, error) {
	if len(data) == 0 {
		return nil, errors.New("empty packet")
	}

	// 1. Check for Studio WebSocket preview packet (52-byte header + 6144 RGB888 bytes)
	if len(data) == PreviewPacketLen {
		return decodeRGB888(data[PreviewHeaderLen:])
	}

	// 2. Check for raw 6144-byte RGB888 frame
	if len(data) == NBytes {
		return decodeRGB888(data)
	}

	// 3. Check for Screeny UDP Wire Protocol (magic 's' = 0x53)
	if len(data) >= HeaderLen && data[0] == Magic {
		ver := data[1] >> 4
		ty := data[1] & 0x0F
		if ver != ProtocolVersion {
			return nil, fmt.Errorf("bad version: got %d, expected %d", ver, ProtocolVersion)
		}
		if ty != TypeFrame {
			return nil, fmt.Errorf("bad packet type: %d", ty)
		}

		codec := data[2]
		flags := data[3]
		length := int(binary.LittleEndian.Uint16(data[6:8]))
		if HeaderLen+length > len(data) {
			return nil, fmt.Errorf("bad packet length: header+len %d > datagram %d", HeaderLen+length, len(data))
		}

		payload := data[HeaderLen : HeaderLen+length]
		if flags&FlagHasTS != 0 {
			if len(payload) < 4 {
				return nil, errors.New("short timestamp in packet")
			}
			payload = payload[4:]
		}

		return decodeCodec(codec, payload)
	}

	return nil, fmt.Errorf("unrecognized frame format (length %d)", len(data))
}

// decodeCodec decodes the pixel payload according to codec ID.
func decodeCodec(codec byte, payload []byte) (*image.RGBA, error) {
	switch codec {
	case CodecSolid:
		return decodeSolid(payload)
	case CodecPAL5:
		return decodePAL5(payload)
	case CodecPAL8_LZ:
		return decodePAL8LZ(payload)
	case CodecPAL4_LZ:
		return decodePAL4LZ(payload)
	case CodecBC1Dual:
		return decodeBC1Dual(payload)
	default:
		return nil, fmt.Errorf("unsupported codec: 0x%02x", codec)
	}
}

// decodeSolid decodes 3 RGB bytes into a solid color 64x32 frame.
func decodeSolid(src []byte) (*image.RGBA, error) {
	if len(src) < SolidLen {
		return nil, errors.New("solid codec: short payload")
	}
	if len(src) > SolidLen {
		return nil, errors.New("solid codec: long payload")
	}

	img := image.NewRGBA(image.Rect(0, 0, PanelWidth, PanelHeight))
	col := color.RGBA{R: src[0], G: src[1], B: src[2], A: 255}
	for y := range PanelHeight {
		for x := range PanelWidth {
			img.SetRGBA(x, y, col)
		}
	}
	return img, nil
}

// decodePAL5 decodes fixed 1376-byte adaptive palette payload.
func decodePAL5(src []byte) (*image.RGBA, error) {
	if len(src) < PAL5Len {
		return nil, errors.New("pal5: short payload")
	}
	if len(src) > PAL5Len {
		return nil, errors.New("pal5: long payload")
	}

	pal := src[:32*3]
	nib := src[32*3 : 32*3+NPixels/2]
	hi := src[32*3+NPixels/2:]

	img := image.NewRGBA(image.Rect(0, 0, PanelWidth, PanelHeight))
	for p := range NPixels {
		b := nib[p>>1]
		var low byte
		if p&1 == 0 {
			low = b >> 4
		} else {
			low = b & 0x0F
		}
		bit := (hi[p>>3] >> (7 - (p & 7))) & 1
		idx := int(low | (bit << 4)) // 0..31

		c := idx * 3
		img.SetRGBA(p%PanelWidth, p/PanelWidth, color.RGBA{
			R: pal[c],
			G: pal[c+1],
			B: pal[c+2],
			A: 255,
		})
	}
	return img, nil
}

// decodePAL8LZ decodes up to 256 colors with an LZ-compressed index plane.
func decodePAL8LZ(src []byte) (*image.RGBA, error) {
	if len(src) == 0 {
		return nil, errors.New("pal8_lz: short payload")
	}
	n := int(src[0]) + 1 // 1..256
	plen := n * 3
	if len(src) < 1+plen {
		return nil, errors.New("pal8_lz: short payload (palette truncated)")
	}

	pal := src[1 : 1+plen]
	stream := src[1+plen:]

	indices := make([]byte, NPixels)
	used, err := lzInflate(stream, indices, NPixels)
	if err != nil {
		return nil, fmt.Errorf("pal8_lz lz inflate: %w", err)
	}
	if used != len(stream) {
		return nil, errors.New("pal8_lz: long payload (trailing bytes after lz stream)")
	}

	img := image.NewRGBA(image.Rect(0, 0, PanelWidth, PanelHeight))
	for p := range NPixels {
		idx := int(indices[p])
		if idx >= n {
			return nil, fmt.Errorf("pal8_lz: index %d out of bounds (n=%d)", idx, n)
		}
		c := idx * 3
		img.SetRGBA(p%PanelWidth, p/PanelWidth, color.RGBA{
			R: pal[c],
			G: pal[c+1],
			B: pal[c+2],
			A: 255,
		})
	}
	return img, nil
}

// decodePAL4LZ decodes 16 colors with an LZ-compressed nibble plane.
func decodePAL4LZ(src []byte) (*image.RGBA, error) {
	const pal16 = 16 * 3
	if len(src) < pal16 {
		return nil, errors.New("pal4_lz: short payload")
	}

	pal := src[:pal16]
	stream := src[pal16:]

	nibbles := make([]byte, NPixels/2)
	used, err := lzInflate(stream, nibbles, NPixels/2)
	if err != nil {
		return nil, fmt.Errorf("pal4_lz lz inflate: %w", err)
	}
	if used != len(stream) {
		return nil, errors.New("pal4_lz: long payload (trailing bytes after lz stream)")
	}

	img := image.NewRGBA(image.Rect(0, 0, PanelWidth, PanelHeight))
	for p := range NPixels {
		b := nibbles[p>>1]
		var idx int
		if p&1 == 0 {
			idx = int(b >> 4)
		} else {
			idx = int(b & 0x0F)
		}
		c := idx * 3
		img.SetRGBA(p%PanelWidth, p/PanelWidth, color.RGBA{
			R: pal[c],
			G: pal[c+1],
			B: pal[c+2],
			A: 255,
		})
	}
	return img, nil
}

// decodeBC1Dual decodes 4x4 block compressed frames (1296 bytes).
func decodeBC1Dual(src []byte) (*image.RGBA, error) {
	if len(src) < BC1DualLen {
		return nil, errors.New("bc1_dual: short payload")
	}
	if len(src) > BC1DualLen {
		return nil, errors.New("bc1_dual: long payload")
	}

	const nb = (PanelWidth / 4) * (PanelHeight / 4) // 128 blocks
	const flagsLen = nb / 8                         // 16 bytes

	flags := src[:flagsLen]
	blocks := src[flagsLen:BC1DualLen]

	w4 := [4]uint32{0, 85, 171, 256}
	w8 := [8]uint32{0, 37, 73, 110, 146, 183, 219, 256}

	img := image.NewRGBA(image.Rect(0, 0, PanelWidth, PanelHeight))

	for by := range PanelHeight / 4 {
		for bx := range PanelWidth / 4 {
			n := by*(PanelWidth/4) + bx
			b := blocks[n*10 : (n+1)*10]
			e888 := (flags[n>>3]>>(7-(n&7)))&1 == 1

			if e888 {
				// Flag 1: RGB888 endpoints, 4 levels
				e0 := [3]byte{b[0], b[1], b[2]}
				e1 := [3]byte{b[3], b[4], b[5]}
				var ramp [4][3]byte
				for k := range 4 {
					w := w4[k]
					ramp[k] = [3]byte{
						lerp8(e0[0], e1[0], w),
						lerp8(e0[1], e1[1], w),
						lerp8(e0[2], e1[2], w),
					}
				}
				for j := range 16 {
					idx := int((b[6+(j>>2)] >> (6 - 2*(j&3))) & 3)
					col := ramp[idx]
					x := bx*4 + (j & 3)
					y := by*4 + (j >> 2)
					img.SetRGBA(x, y, color.RGBA{R: col[0], G: col[1], B: col[2], A: 255})
				}
			} else {
				// Flag 0: RGB565 endpoints, 8 levels
				e0 := rgb565(b[0], b[1])
				e1 := rgb565(b[2], b[3])
				var ramp [8][3]byte
				for k := range 8 {
					w := w8[k]
					ramp[k] = [3]byte{
						lerp8(e0[0], e1[0], w),
						lerp8(e0[1], e1[1], w),
						lerp8(e0[2], e1[2], w),
					}
				}
				for j := range 16 {
					byteIdx := j >> 3
					sh := 7 - (j & 7)
					b0 := (b[4+byteIdx] >> sh) & 1
					b1 := (b[6+byteIdx] >> sh) & 1
					b2 := (b[8+byteIdx] >> sh) & 1
					idx := int(b0 | (b1 << 1) | (b2 << 2))
					col := ramp[idx]
					x := bx*4 + (j & 3)
					y := by*4 + (j >> 2)
					img.SetRGBA(x, y, color.RGBA{R: col[0], G: col[1], B: col[2], A: 255})
				}
			}
		}
	}

	return img, nil
}

// decodeRGB888 decodes 6144 contiguous RGB bytes into a 64x32 image.
func decodeRGB888(src []byte) (*image.RGBA, error) {
	if len(src) < NBytes {
		return nil, fmt.Errorf("rgb888: expected %d bytes, got %d", NBytes, len(src))
	}

	img := image.NewRGBA(image.Rect(0, 0, PanelWidth, PanelHeight))
	for p := range NPixels {
		c := p * 3
		img.SetRGBA(p%PanelWidth, p/PanelWidth, color.RGBA{
			R: src[c],
			G: src[c+1],
			B: src[c+2],
			A: 255,
		})
	}
	return img, nil
}

// lzInflate inflates an LZ77 stream into dst until exactly want bytes are produced.
func lzInflate(src []byte, dst []byte, want int) (int, error) {
	if len(dst) < want {
		return 0, errors.New("corrupt: dst slice smaller than want")
	}

	var si, di int
	for di < want {
		if si >= len(src) {
			return 0, errors.New("short: unexpected end of stream")
		}
		flags := src[si]
		si++

		for k := range 8 {
			if di >= want {
				return si, nil
			}
			if (flags>>(7-k))&1 == 1 {
				// Literal
				if si >= len(src) {
					return 0, errors.New("short: missing literal byte")
				}
				dst[di] = src[si]
				si++
				di++
			} else {
				// Match
				if si+1 >= len(src) {
					return 0, errors.New("short: incomplete match bytes")
				}
				a := int(src[si])
				b := int(src[si+1])
				si += 2

				off := ((a << 4) | (b >> 4)) + 1 // 1..4096 backwards
				length := (b & 0x0F) + 3         // 3..18

				if off > di || di+length > want {
					return 0, errors.New("corrupt: match out of bounds")
				}
				for range length {
					dst[di] = dst[di-off]
					di++
				}
			}
		}
	}

	return si, nil
}

// Math helpers for BC1_DUAL.
func lerp8(a, b byte, w uint32) byte {
	return byte((uint32(a)*(256-w) + uint32(b)*w + 128) >> 8)
}

func x5(v uint32) byte {
	return byte((v << 3) | (v >> 2))
}

func x6(v uint32) byte {
	return byte((v << 2) | (v >> 4))
}

func rgb565(lo, hi byte) [3]byte {
	v := uint32(lo) | (uint32(hi) << 8)
	return [3]byte{
		x5((v >> 11) & 31),
		x6((v >> 5) & 63),
		x5(v & 31),
	}
}

// Scale2x scales a 64x32 image to 128x64 using pixel doubling.
func Scale2x(src *image.RGBA) *image.RGBA {
	return ScaleNearest2x(src)
}
