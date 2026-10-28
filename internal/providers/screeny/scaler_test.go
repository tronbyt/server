package screeny

import (
	"image"
	"image/color"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScaleNearest2x(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	red := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	blue := color.RGBA{R: 0, G: 0, B: 255, A: 255}
	src.SetRGBA(0, 0, red)
	src.SetRGBA(1, 0, blue)
	src.SetRGBA(0, 1, blue)
	src.SetRGBA(1, 1, red)

	dst := ScaleNearest2x(src)
	require.Equal(t, 4, dst.Bounds().Dx())
	require.Equal(t, 4, dst.Bounds().Dy())

	// Top-left 2x2 block should all be red
	assert.Equal(t, red, dst.RGBAAt(0, 0))
	assert.Equal(t, red, dst.RGBAAt(1, 0))
	assert.Equal(t, red, dst.RGBAAt(0, 1))
	assert.Equal(t, red, dst.RGBAAt(1, 1))

	// Top-right 2x2 block should all be blue
	assert.Equal(t, blue, dst.RGBAAt(2, 0))
	assert.Equal(t, blue, dst.RGBAAt(3, 0))
	assert.Equal(t, blue, dst.RGBAAt(2, 1))
	assert.Equal(t, blue, dst.RGBAAt(3, 1))
}

func TestScaleEPX2x(t *testing.T) {
	// 3x3 pattern with a diagonal step
	// . W .
	// W B .
	// . . .
	src := image.NewRGBA(image.Rect(0, 0, 3, 3))
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	black := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	grey := color.RGBA{R: 50, G: 50, B: 50, A: 255}

	for y := range 3 {
		for x := range 3 {
			src.SetRGBA(x, y, black)
		}
	}
	src.SetRGBA(1, 0, white) // A
	src.SetRGBA(0, 1, white) // C
	src.SetRGBA(1, 1, grey)  // P
	src.SetRGBA(2, 1, grey)  // B
	src.SetRGBA(1, 2, black) // D

	dst := ScaleEPX2x(src)
	require.Equal(t, 6, dst.Bounds().Dx())
	require.Equal(t, 6, dst.Bounds().Dy())

	// For P at (1, 1), its subpixels are at (2, 2), (3, 2), (2, 3), (3, 3)
	// Because C == A (white), and C != D and A != B,
	// top-left subpixel (2, 2) must smooth into A (white)!
	assert.Equal(t, white, dst.RGBAAt(2, 2), "EPX should smooth diagonal corner to white")
	assert.Equal(t, grey, dst.RGBAAt(3, 2), "Other subpixels of P should remain P")
	assert.Equal(t, grey, dst.RGBAAt(2, 3), "Other subpixels of P should remain P")
	assert.Equal(t, grey, dst.RGBAAt(3, 3), "Other subpixels of P should remain P")
}

func TestScaleBilinear2x(t *testing.T) {
	// 2x2 with left half 0, right half 100
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		src.SetRGBA(0, y, color.RGBA{R: 0, G: 0, B: 0, A: 255})
		src.SetRGBA(1, y, color.RGBA{R: 100, G: 100, B: 100, A: 255})
	}

	dst := ScaleBilinear2x(src)
	require.Equal(t, 4, dst.Bounds().Dx())
	require.Equal(t, 4, dst.Bounds().Dy())

	// Values should interpolate smoothly from left to right
	r0 := dst.RGBAAt(0, 0).R
	r1 := dst.RGBAAt(1, 0).R
	r2 := dst.RGBAAt(2, 0).R
	r3 := dst.RGBAAt(3, 0).R

	assert.True(t, r0 <= r1 && r1 < r2 && r2 <= r3, "Bilinear should interpolate smoothly: %d, %d, %d, %d", r0, r1, r2, r3)
	assert.True(t, r1 > 0 && r2 < 100, "Intermediate LEDs should have blended colors")
}

func TestScaleBicubic2x(t *testing.T) {
	// Horizontal gradient: 0, 30, 70, 100
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := range 2 {
		src.SetRGBA(0, y, color.RGBA{R: 0, G: 0, B: 0, A: 255})
		src.SetRGBA(1, y, color.RGBA{R: 30, G: 30, B: 30, A: 255})
		src.SetRGBA(2, y, color.RGBA{R: 70, G: 70, B: 70, A: 255})
		src.SetRGBA(3, y, color.RGBA{R: 100, G: 100, B: 100, A: 255})
	}

	dst := ScaleBicubic2x(src)
	require.Equal(t, 8, dst.Bounds().Dx())
	require.Equal(t, 4, dst.Bounds().Dy())

	// Every subpixel should be monotonically increasing without overshoot outside [0, 100]
	var prev byte
	for x := range 8 {
		c := dst.RGBAAt(x, 0).R
		assert.GreaterOrEqual(t, c, prev)
		assert.LessOrEqual(t, c, byte(100))
		prev = c
	}
}

func TestScaleFrame2x_Dispatch(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 64, 32))
	assert.Nil(t, ScaleFrame2x(nil, FilterBicubic))

	dstBicubic := ScaleFrame2x(src, FilterBicubic)
	assert.Equal(t, 128, dstBicubic.Bounds().Dx())
	assert.Equal(t, 64, dstBicubic.Bounds().Dy())

	dstEPX := ScaleFrame2x(src, FilterScale2x)
	assert.Equal(t, 128, dstEPX.Bounds().Dx())

	dstBilinear := ScaleFrame2x(src, FilterBilinear)
	assert.Equal(t, 128, dstBilinear.Bounds().Dx())

	dstNearest := ScaleFrame2x(src, FilterNearest)
	assert.Equal(t, 128, dstNearest.Bounds().Dx())

	dstDefault := ScaleFrame2x(src, "")
	assert.Equal(t, 128, dstDefault.Bounds().Dx())
}
