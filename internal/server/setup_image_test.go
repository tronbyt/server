package server

import (
	"bytes"
	"context"
	"image/color"
	"strings"
	"testing"

	"tronbyt-server/internal/data"
	"tronbyt-server/web"

	"github.com/skip2/go-qrcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tronbyt/pixlet/render"
	"gorm.io/gorm"
)

// The drawn code must be the encoder's code — a QR that is merely
// QR-shaped scans as nothing.
func TestSetupQRImageMatchesTheEncoder(t *testing.T) {
	const content = "http://192.168.1.155:8000"

	img, side, ok := setupQRImage(content, 32)
	if !ok {
		t.Fatal("expected a QR to fit a 32px panel")
	}

	expected, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		t.Fatal(err)
	}
	expected.DisableBorder = true
	modules := expected.Bitmap()

	quiet := (side/scaleOf(side, len(modules)) - len(modules)) / 2
	scale := scaleOf(side, len(modules))
	for y, row := range modules {
		for x, dark := range row {
			px := img.At((x+quiet)*scale, (y+quiet)*scale)
			if isDark(px) != dark {
				t.Fatalf("module (%d,%d): drawn dark=%v, encoder says %v",
					x, y, isDark(px), dark)
			}
		}
	}
}

// Standard dark-on-light polarity: inverted codes are read far less
// reliably, so the margin has to be the lit colour, not the panel's black.
func TestSetupQRImageHasALightQuietZone(t *testing.T) {
	img, side, ok := setupQRImage("http://192.168.1.155:8000", 32)
	if !ok {
		t.Fatal("expected a QR to fit")
	}
	for _, p := range [][2]int{{0, 0}, {side - 1, 0}, {0, side - 1}, {side - 1, side - 1}} {
		if isDark(img.At(p[0], p[1])) {
			t.Errorf("corner %v is dark; the quiet zone must be light", p)
		}
	}
}

// A code that cannot be drawn at even one pixel per module is worse than no
// code at all, so it is declined rather than clipped.
func TestSetupQRImageDeclinesWhatCannotFit(t *testing.T) {
	if _, _, ok := setupQRImage("http://192.168.1.155:8000", 8); ok {
		t.Error("expected a QR to be declined on an 8px panel")
	}
}

func TestRenderSetupImageDrawsEveryPanelSize(t *testing.T) {
	for _, c := range []struct{ w, h int }{{64, 32}, {128, 64}, {64, 64}} {
		img, err := renderSetupImage(context.Background(), c.w, c.h, "http://192.168.1.155:8000")
		require.NoErrorf(t, err, "%dx%d", c.w, c.h)
		require.NotEmptyf(t, img, "%dx%d: empty image", c.w, c.h)
		assert.Truef(t, bytes.HasPrefix(img, []byte("RIFF")), "%dx%d: not a WebP", c.w, c.h)
	}
}

func TestRenderSetupImageRejectsAnAddressItCannotUse(t *testing.T) {
	if _, err := renderSetupImage(context.Background(), 64, 32, "::not a url"); err == nil {
		t.Error("expected an error for an unusable address")
	}
}

// The point of the change: a device with nothing installed is told where to
// go, instead of being shown a placeholder it cannot act on.
func TestGetNextAppImageShowsSetupWhenThereAreNoApps(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	user := data.User{Username: "testuser"}
	if err := gorm.G[data.User](s.DB).Create(ctx, &user); err != nil {
		t.Fatal(err)
	}
	device := data.Device{ID: "dev1", Username: user.Username, Name: "Dev", Brightness: 50}
	if err := gorm.G[data.Device](s.DB).Create(ctx, &device); err != nil {
		t.Fatal(err)
	}

	placeholder, err := web.Assets.ReadFile("static/images/default.webp")
	if err != nil {
		t.Fatal(err)
	}

	withURL, _, err := s.GetNextAppImage(ctx, &device, &user, "http://192.168.1.155:8000")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(withURL, placeholder) {
		t.Error("expected the setup screen, got the placeholder")
	}

	// Without an address there is nothing to point at, so the old behaviour
	// stands rather than drawing a code that leads nowhere.
	withoutURL, _, err := s.GetNextAppImage(ctx, &device, &user, "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(withoutURL, placeholder) {
		t.Error("expected the placeholder when no address is known")
	}
}

// A panel wider than it is tall has room for the address beside the QR.
func TestSetupImageDrawsTheAddressBesideTheQROnAWidePanel(t *testing.T) {
	for _, c := range []struct{ w, h int }{{64, 32}, {128, 64}} {
		root, err := setupImageRoot(c.w, c.h, "http://192.168.1.155:8000")
		require.NoErrorf(t, err, "%dx%d", c.w, c.h)
		layout := setupImageLayout(t, root)
		row, ok := layout.(*render.Row)
		require.Truef(t, ok, "%dx%d: expected the address beside the QR, got %T", c.w, c.h, layout)
		assertAddressHasWidth(t, row.Children)
	}
}

// A square panel has none: a QR sized to the full height is also the full
// width, which leaves the address nothing to wrap into. Getting this wrong is
// silent — the QR still draws and the address is simply squeezed off the
// panel — so this asserts the layout, not just that something rendered.
func TestSetupImageStacksTheAddressOnASquarePanel(t *testing.T) {
	root, err := setupImageRoot(64, 64, "http://192.168.1.155:8000")
	require.NoError(t, err)
	layout := setupImageLayout(t, root)
	column, ok := layout.(*render.Column)
	require.Truef(t, ok, "expected the address stacked under the QR, got %T", layout)
	assertAddressHasWidth(t, column.Children)
}

func TestSetupFontFitsThePanel(t *testing.T) {
	if f := setupFont(64); !strings.Contains(f, "tom-thumb") {
		t.Errorf("64px panel should use the narrowest face, got %q", f)
	}
	if f := setupFont(128); f == "tom-thumb" {
		t.Error("128px panel has room for a larger face")
	}
}

// helpers

// setupImageLayout unwraps the sizing Box every panel is drawn into, returning
// the widget that arranges the QR and the address.
func setupImageLayout(t *testing.T, root render.Root) render.Widget {
	t.Helper()
	box, ok := root.Child.(*render.Box)
	require.Truef(t, ok, "expected the panel-sized Box, got %T", root.Child)
	return box.Child
}

func assertAddressHasWidth(t *testing.T, children []render.Widget) {
	t.Helper()
	for _, child := range children {
		if padding, ok := child.(*render.Padding); ok {
			child = padding.Child
		}
		if text, ok := child.(*render.WrappedText); ok {
			assert.Greaterf(t, text.Width, 0, "the address was left %d px to draw in", text.Width)
			return
		}
	}
	assert.Fail(t, "no address was drawn")
}

func scaleOf(side, modules int) int {
	for quiet := 4; quiet >= 1; quiet-- {
		total := modules + 2*quiet
		if total*(side/total) == side {
			return side / total
		}
	}
	return 1
}

func isDark(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r < 0x8000 && g < 0x8000 && b < 0x8000
}
