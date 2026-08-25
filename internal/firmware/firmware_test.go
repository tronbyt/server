package firmware

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLooksLikeFirmware(t *testing.T) {
	ota := bytes.Repeat([]byte{0xFF}, 64)
	ota[0] = espImageMagic

	merged := bytes.Repeat([]byte{0xFF}, esp32BootloaderOffset+64)
	merged[esp32BootloaderOffset] = espImageMagic

	htmlLogin := []byte("<!DOCTYPE html><html><body>Sign in to GitHub</body></html>")

	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{name: "ota image", data: ota, want: true},
		{name: "merged image with bootloader at 0x1000", data: merged, want: true},
		{name: "html login page", data: htmlLogin, want: false},
		{name: "empty", data: nil, want: false},
		{name: "too short", data: []byte{espImageMagic, 0x01}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, LooksLikeFirmware(tt.data))
		})
	}
}
