package server

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/url"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
	"github.com/tronbyt/pixlet/encode"
	"github.com/tronbyt/pixlet/render"
)

// A device with no apps installed used to show a placeholder image, which
// tells whoever is looking at it nothing about how to fix that. Instead we
// draw a QR of this server's address next to the address in text: scan it, or
// type it, and you land on the page where apps get added.
//
// The address comes from the request the device itself made, so it is by
// construction one that reaches this server from the device's network — which
// is the same network as the phone about to scan it.

var (
	setupQRLight = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	setupQRDark  = color.RGBA{A: 0xff}
	setupTextFg  = color.RGBA{R: 0xff, G: 0xa5, B: 0x00, A: 0xff}
)

// renderSetupImage draws the QR and address for a device that has nothing to
// show yet. Returns an error if baseURL cannot be turned into something worth
// displaying, so callers can fall back to the placeholder.
func renderSetupImage(ctx context.Context, width, height int, baseURL string) ([]byte, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("setup image: unusable base URL %q", baseURL)
	}

	children := []render.Widget{}
	textWidth := width

	// The QR is optional: on a short panel a long address may not fit at even
	// one module per pixel, and half a QR is worse than none.
	const gap = 2
	if qrImage, side, ok := setupQRImage(baseURL, height); ok {
		encoded, err := encodePNG(qrImage)
		if err != nil {
			return nil, err
		}
		// HoldFrames must be at least 1: frameImg divides by it, and a
		// zero value panics rather than defaulting.
		qr := &render.Image{Src: encoded, Width: side, Height: side, HoldFrames: 1}
		// Image decodes Src only when initialized; painting an uninitialized
		// one panics rather than erroring.
		if err := qr.InitFromImage(encoded); err != nil {
			return nil, err
		}
		children = append(children, qr)
		textWidth = width - side - gap
	}

	// The address without its scheme: it is what someone types, and every
	// character costs pixels on a 64-wide panel.
	address := &render.WrappedText{
		Content: strings.TrimSuffix(parsed.Host, ":80"),
		Font:    setupFont(width),
		Color:   setupTextFg,
		Align:   "center",
		Width:   textWidth,
		// An address has no spaces to wrap on, so it has to break mid-token
		// or it renders as one clipped line.
		WordBreak: true,
	}
	// nil thread is safe because Font is set explicitly; the thread is only
	// consulted to look up a default font.
	if err := address.Init(nil); err != nil {
		return nil, err
	}
	children = append(children, &render.Padding{
		Pad:   render.Insets{Left: gap},
		Child: address,
	})

	root := render.Root{
		Child: &render.Box{
			Width:  width,
			Height: height,
			Child: &render.Row{
				MainAlign:  "start",
				CrossAlign: "center",
				Children:   children,
			},
		},
	}

	screens := encode.ScreensFromRoots([]render.Root{root}, width, height)
	return screens.EncodeWebP(ctx, 15*time.Second)
}

// setupFont picks the smallest legible face for the panel. tom-thumb is the
// narrowest pixlet ships, which is what makes an address fit beside a QR on a
// 64x32.
func setupFont(width int) string {
	if width >= 128 {
		return "6x13"
	}
	return "tom-thumb"
}

// setupQRImage renders content as a QR sized to fit maxSide pixels square,
// reporting false when it cannot be drawn legibly.
//
// The quiet zone is the widest that still fits. The spec asks for four
// modules; a 64x32 panel cannot always afford that, and a code drawn without
// any margin at all is one a phone will not read — so this steps down rather
// than either overflowing or silently dropping the margin.
func setupQRImage(content string, maxSide int) (image.Image, int, bool) {
	code, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		return nil, 0, false
	}
	code.DisableBorder = true

	modules := code.Bitmap()
	if len(modules) == 0 {
		return nil, 0, false
	}

	// Bigger modules matter more than a wider margin: a code drawn at one
	// pixel per module on a 64-tall panel is at the limit of what a phone
	// camera resolves, so maximise scale first and spend whatever height is
	// left on the quiet zone. The spec asks for four modules of margin; a
	// small panel cannot always afford that, and dropping it entirely makes a
	// code most readers refuse, so this takes the widest that still fits.
	quiet, scale := 0, 0
	for candidate := 4; candidate >= 1; candidate-- {
		fit := maxSide / (len(modules) + 2*candidate)
		if fit > scale {
			quiet, scale = candidate, fit
		}
	}
	if scale < 1 {
		return nil, 0, false
	}

	total := len(modules) + 2*quiet
	side := total * scale
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	// Standard dark-on-light: an inverted code is far less reliably scanned,
	// so the code's background is what gets lit on the panel.
	draw.Draw(img, img.Bounds(), &image.Uniform{C: setupQRLight}, image.Point{}, draw.Src)
	for y, row := range modules {
		for x, dark := range row {
			if !dark {
				continue
			}
			module := image.Rect(
				(x+quiet)*scale, (y+quiet)*scale,
				(x+quiet+1)*scale, (y+quiet+1)*scale,
			)
			draw.Draw(img, module, &image.Uniform{C: setupQRDark}, image.Point{}, draw.Src)
		}
	}
	return img, side, true
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
