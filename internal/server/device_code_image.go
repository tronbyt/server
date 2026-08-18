package server

import (
	"context"
	"image/color"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tronbyt-server/internal/data"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/tronbyt/pixlet/encode"
	"github.com/tronbyt/pixlet/render"
)

// Device-code display: while a device-authorization flow is pending we
// render the user code as a WebP and push it to the user's displays, so
// the code shows up on the matrix itself — read it off the shelf, approve
// on your phone. The image is pushed as an *ephemeral* frame (a "__"
// prefixed file), which GetNextAppImage serves once and deletes.

// deviceCodeImageName is a fixed ephemeral filename so repeated pushes
// overwrite one another instead of queueing a backlog of stale codes.
// The "__" prefix marks it ephemeral for GetNextAppImage.
const deviceCodeImageName = "__device_code.webp"

// Colors chosen for legibility on an LED matrix: amber label, white code.
var (
	deviceCodeLabelColor = color.RGBA{R: 0xff, G: 0xa5, B: 0x00, A: 0xff}
	deviceCodeCodeColor  = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	deviceCodeURLColor   = color.RGBA{R: 0x88, G: 0xcc, B: 0xff, A: 0xff}
)

// renderDeviceCodeImage draws "<PROVIDER> / <USER-CODE> / <verification
// url>" at the given size and encodes it as an animated WebP (the URL
// line scrolls when it doesn't fit). Sizes follow the render pipeline:
// 64x32, or 128x64 for wide devices.
func renderDeviceCodeImage(ctx context.Context, width, height int, providerName, userCode, verificationURI string) ([]byte, error) {
	labelFonts := []string{"tom-thumb"}
	codeFonts := []string{"6x13", "5x8", "tom-thumb"}
	urlFont := "tom-thumb"
	if width >= 128 {
		labelFonts = []string{"6x13", "tom-thumb"}
		codeFonts = []string{"10x20", "6x13", "5x8"}
		urlFont = "6x13"
	}

	label, err := fitText(strings.ToUpper(providerName), labelFonts, width, deviceCodeLabelColor)
	if err != nil {
		return nil, err
	}

	// The code is the one thing that must never be clipped — step down
	// through narrower fonts until it fits the panel.
	code, err := fitText(userCode, codeFonts, width, deviceCodeCodeColor)
	if err != nil {
		return nil, err
	}

	url, err := initText(&render.Text{
		Content: trimURLScheme(verificationURI),
		Font:    urlFont,
		Color:   deviceCodeURLColor,
	})
	if err != nil {
		return nil, err
	}

	root := render.Root{
		Delay: 50,
		Child: &render.Box{
			Width:  width,
			Height: height,
			Child: &render.Column{
				MainAlign:  "space_evenly",
				CrossAlign: "center",
				Children: []render.Widget{
					label,
					code,
					// The verification URL rarely fits; Marquee scrolls it
					// when needed and centers it when it does fit.
					&render.Marquee{Width: width, Align: "center", Child: url},
				},
			},
		},
	}

	screens := encode.ScreensFromRoots([]render.Root{root}, width, height)
	return screens.EncodeWebP(ctx, 15*time.Second)
}

// initText initializes a Text widget's glyph image. Font is always set
// explicitly here, so the nil starlark thread is never dereferenced (it
// is only consulted to pick a default font).
func initText(t *render.Text) (*render.Text, error) {
	if err := t.Init(nil); err != nil {
		return nil, err
	}
	return t, nil
}

// fitText renders content in the first font of the ladder that fits
// maxWidth, falling back to the narrowest if none do.
func fitText(content string, fonts []string, maxWidth int, c color.Color) (*render.Text, error) {
	var last *render.Text
	for _, font := range fonts {
		t, err := initText(&render.Text{Content: content, Font: font, Color: c})
		if err != nil {
			return nil, err
		}
		w, _ := t.Size()
		if w <= maxWidth {
			return t, nil
		}
		last = t
	}
	return last, nil
}

// trimURLScheme drops "https://" so more of the URL fits on the panel.
func trimURLScheme(u string) string {
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	return strings.TrimSuffix(u, "/")
}

// pushDeviceCodeToDisplays renders the code and pushes it to every device
// the user owns. Best-effort: a failure to reach one display must not
// break the browser flow, since the page shows the same code.
func (s *Server) pushDeviceCodeToDisplays(ctx context.Context, user *data.User, providerName, userCode, verificationURI string) {
	for _, device := range user.Devices {
		width, height := 64, 32
		if device.Type.Supports2x() {
			width, height = 128, 64
		}

		img, err := renderDeviceCodeImage(ctx, width, height, providerName, userCode, verificationURI)
		if err != nil {
			slog.Warn("Device code: render failed", "device", device.ID, "error", err)
			continue
		}

		// Websocket-connected devices show it immediately; everything else
		// picks it up on its next poll.
		s.Broadcaster.Notify(device.ID, img)
		if err := s.writeEphemeralPush(device.ID, img); err != nil {
			slog.Warn("Device code: push failed", "device", device.ID, "error", err)
		}
	}
}

// clearDeviceCodeFromDisplays removes a pending code frame once the flow
// finishes, so an approved (or expired) code doesn't surface later.
func (s *Server) clearDeviceCodeFromDisplays(user *data.User) {
	for _, device := range user.Devices {
		path, err := s.deviceCodeImagePath(device.ID)
		if err != nil {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			slog.Warn("Device code: clear failed", "device", device.ID, "error", err)
		}
	}
}

// deviceCodeImagePath resolves the ephemeral push path for a device.
func (s *Server) deviceCodeImagePath(deviceID string) (string, error) {
	dir, err := s.ensureDeviceImageDir(deviceID)
	if err != nil {
		return "", err
	}
	return securejoin.SecureJoin(filepath.Join(dir, "pushed"), deviceCodeImageName)
}

// writeEphemeralPush writes the code frame under a stable ephemeral name,
// replacing any previous one for this device.
func (s *Server) writeEphemeralPush(deviceID string, img []byte) error {
	path, err := s.deviceCodeImagePath(deviceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, img, 0644)
}
