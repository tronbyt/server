package screeny

import (
	"encoding/binary"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildFramePacket creates a valid screeny UDP packet around a payload.
func buildFramePacket(codec byte, flags byte, seq uint16, payload []byte) []byte {
	pktLen := HeaderLen + len(payload)
	pkt := make([]byte, pktLen)
	pkt[0] = Magic
	pkt[1] = (ProtocolVersion << 4) | TypeFrame
	pkt[2] = codec
	pkt[3] = flags
	binary.LittleEndian.PutUint16(pkt[4:6], seq)
	binary.LittleEndian.PutUint16(pkt[6:8], uint16(len(payload)))
	copy(pkt[HeaderLen:], payload)
	return pkt
}

func checkImageMatchesRGB(t *testing.T, img *image.RGBA, expectedRGB []byte) {
	t.Helper()
	require.Equal(t, NBytes, len(expectedRGB))
	for p := range NPixels {
		x := p % PanelWidth
		y := p / PanelWidth
		c := img.RGBAAt(x, y)
		expR := expectedRGB[p*3]
		expG := expectedRGB[p*3+1]
		expB := expectedRGB[p*3+2]
		if c.R != expR || c.G != expG || c.B != expB || c.A != 255 {
			t.Fatalf("mismatch at pixel %d (%d,%d): got rgba(%d,%d,%d,%d), expected rgb(%d,%d,%d)",
				p, x, y, c.R, c.G, c.B, c.A, expR, expG, expB)
		}
	}
}

func TestDecodeScreenyFrame_GoldenVectors(t *testing.T) {
	testCases := []struct {
		name    string
		codec   byte
		binFile string
		rgbFile string
	}{
		{"Solid White", CodecSolid, "solid_white.bin", "solid_white.rgb"},
		{"Solid Teal", CodecSolid, "solid_teal.bin", "solid_teal.rgb"},
		{"Solid Black", CodecSolid, "solid_black.bin", "solid_black.rgb"},
		{"PAL5 Synth16", CodecPAL5, "pal5_synth16.bin", "pal5_synth16.rgb"},
		{"PAL4 LZ Synth16", CodecPAL4_LZ, "pal4_lz_synth16.bin", "pal4_lz_synth16.rgb"},
		{"PAL8 LZ Synth100", CodecPAL8_LZ, "pal8_lz_synth100.bin", "pal8_lz_synth100.rgb"},
		{"BC1 Dual Synth16", CodecBC1Dual, "bc1_dual_synth16.bin", "bc1_dual_synth16.rgb"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			binPath := filepath.Join("testdata", tc.binFile)
			rgbPath := filepath.Join("testdata", tc.rgbFile)

			payload, err := os.ReadFile(binPath)
			require.NoError(t, err)

			expectedRGB, err := os.ReadFile(rgbPath)
			require.NoError(t, err)

			// 1. Test wrapped in UDP Frame packet
			packet := buildFramePacket(tc.codec, FlagKey, 1234, payload)
			img, err := DecodeScreenyFrame(packet)
			require.NoError(t, err)
			require.NotNil(t, img)
			assert.Equal(t, PanelWidth, img.Bounds().Dx())
			assert.Equal(t, PanelHeight, img.Bounds().Dy())
			checkImageMatchesRGB(t, img, expectedRGB)

			// 2. Test wrapped in UDP Frame packet WITH timestamp (F_HAS_TS)
			tsPayload := make([]byte, 4+len(payload))
			binary.LittleEndian.PutUint32(tsPayload[:4], 0x12345678)
			copy(tsPayload[4:], payload)

			tsPacket := buildFramePacket(tc.codec, FlagKey|FlagHasTS, 1235, tsPayload)
			imgTS, err := DecodeScreenyFrame(tsPacket)
			require.NoError(t, err)
			require.NotNil(t, imgTS)
			checkImageMatchesRGB(t, imgTS, expectedRGB)
		})
	}
}

func TestDecodeScreenyFrame_StudioPreviewFormat(t *testing.T) {
	// 52-byte header + 6144 RGB888 bytes
	packet := make([]byte, PreviewPacketLen)
	// Fill header with dummy stats
	for i := range PreviewHeaderLen {
		packet[i] = byte(i)
	}
	// Fill preview RGB with a pattern
	for p := range NPixels {
		packet[PreviewHeaderLen+p*3] = byte(p & 0xFF)
		packet[PreviewHeaderLen+p*3+1] = byte((p >> 3) & 0xFF)
		packet[PreviewHeaderLen+p*3+2] = 128
	}

	img, err := DecodeScreenyFrame(packet)
	require.NoError(t, err)
	require.NotNil(t, img)
	assert.Equal(t, PanelWidth, img.Bounds().Dx())
	assert.Equal(t, PanelHeight, img.Bounds().Dy())

	// Verify sample pixel
	c0 := img.RGBAAt(0, 0)
	assert.Equal(t, color.RGBA{R: 0, G: 0, B: 128, A: 255}, c0)
}

func TestDecodeScreenyFrame_RawRGB888(t *testing.T) {
	rawRGB := make([]byte, NBytes)
	for p := range NPixels {
		rawRGB[p*3] = 42
		rawRGB[p*3+1] = 84
		rawRGB[p*3+2] = 126
	}

	img, err := DecodeScreenyFrame(rawRGB)
	require.NoError(t, err)
	require.NotNil(t, img)
	assert.Equal(t, color.RGBA{R: 42, G: 84, B: 126, A: 255}, img.RGBAAt(10, 10))
}

func TestDecodeScreenyFrame_Errors(t *testing.T) {
	// Empty data
	_, err := DecodeScreenyFrame(nil)
	assert.Error(t, err)

	// Bad magic
	badMagic := []byte{0x00, 0x10, 0x7F, 0x01, 0x00, 0x00, 0x03, 0x00, 1, 2, 3}
	_, err = DecodeScreenyFrame(badMagic)
	assert.Error(t, err)

	// Bad version
	badVer := []byte{Magic, 0x20, 0x7F, 0x01, 0x00, 0x00, 0x03, 0x00, 1, 2, 3}
	_, err = DecodeScreenyFrame(badVer)
	assert.Error(t, err)

	// Bad type
	badType := []byte{Magic, 0x12, 0x7F, 0x01, 0x00, 0x00, 0x03, 0x00, 1, 2, 3}
	_, err = DecodeScreenyFrame(badType)
	assert.Error(t, err)

	// Truncated packet
	truncated := []byte{Magic, 0x10, 0x7F, 0x01, 0x00, 0x00, 0x10, 0x00, 1, 2}
	_, err = DecodeScreenyFrame(truncated)
	assert.Error(t, err)

	// Unsupported codec
	badCodec := buildFramePacket(0x33, FlagKey, 1, []byte{1, 2, 3})
	_, err = DecodeScreenyFrame(badCodec)
	assert.Error(t, err)
}

func TestScale2x(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, PanelWidth, PanelHeight))
	src.SetRGBA(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	src.SetRGBA(1, 0, color.RGBA{R: 0, G: 255, B: 0, A: 255})

	scaled := Scale2x(src)
	require.Equal(t, 128, scaled.Bounds().Dx())
	require.Equal(t, 64, scaled.Bounds().Dy())

	// Top-left 2x2 pixels should be Red
	assert.Equal(t, color.RGBA{R: 255, G: 0, B: 0, A: 255}, scaled.RGBAAt(0, 0))
	assert.Equal(t, color.RGBA{R: 255, G: 0, B: 0, A: 255}, scaled.RGBAAt(1, 0))
	assert.Equal(t, color.RGBA{R: 255, G: 0, B: 0, A: 255}, scaled.RGBAAt(0, 1))
	assert.Equal(t, color.RGBA{R: 255, G: 0, B: 0, A: 255}, scaled.RGBAAt(1, 1))

	// Next 2x2 pixels should be Green
	assert.Equal(t, color.RGBA{R: 0, G: 255, B: 0, A: 255}, scaled.RGBAAt(2, 0))
	assert.Equal(t, color.RGBA{R: 0, G: 255, B: 0, A: 255}, scaled.RGBAAt(3, 0))
}
