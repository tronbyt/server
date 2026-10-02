package server

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/webp"
)

func TestRenderDeviceCodeImage(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
	}{
		{"standard 64x32", 64, 32},
		{"wide 128x64", 128, 64},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img, err := renderDeviceCodeImage(context.Background(), tc.width, tc.height,
				"github", "WDJB-MJHT", "https://github.com/login/device")
			require.NoError(t, err)
			require.NotEmpty(t, img)

			// It must be a real WebP the device can decode, at the size asked for.
			cfg, err := webp.DecodeConfig(bytes.NewReader(img))
			require.NoError(t, err, "device code image must be decodable WebP")
			assert.Equal(t, tc.width, cfg.Width)
			assert.Equal(t, tc.height, cfg.Height)
		})
	}
}

// TestRenderDeviceCodeImageLongCode guards against a code or URL that
// overflows the panel breaking the render outright.
func TestRenderDeviceCodeImageLongCode(t *testing.T) {
	img, err := renderDeviceCodeImage(context.Background(), 64, 32,
		"microsoft", "ABCDEFGHIJKLMNOP", "https://microsoft.com/devicelogin")
	require.NoError(t, err)
	assert.NotEmpty(t, img)
}

func TestTrimURLScheme(t *testing.T) {
	assert.Equal(t, "github.com/login/device", trimURLScheme("https://github.com/login/device"))
	assert.Equal(t, "trakt.tv/activate", trimURLScheme("http://trakt.tv/activate/"))
	assert.Equal(t, "example.com", trimURLScheme("example.com"))
}
