package screeny

import (
	"image"

	"tronbyt-server/internal/scaler"
)

// Supported 128x64 upscaling filters (re-exported from internal/scaler for backward compatibility).
const (
	FilterBicubic  = scaler.FilterBicubic
	FilterScale2x  = scaler.FilterScale2x
	FilterBilinear = scaler.FilterBilinear
	FilterNearest  = scaler.FilterNearest
)

// ScaleFrame2x upscales a 64x32 frame to 128x64 using the specified filter algorithm.
func ScaleFrame2x(src *image.RGBA, filter string) *image.RGBA {
	return scaler.ScaleFrame2x(src, filter)
}

// ScaleNearest2x doubles each 64x32 pixel into a 2x2 block (classic retro LED look).
func ScaleNearest2x(src *image.RGBA) *image.RGBA {
	return scaler.ScaleNearest2x(src)
}

// ScaleEPX2x applies the authentic EPX / Scale2x (AdvMAME2x) algorithm.
func ScaleEPX2x(src *image.RGBA) *image.RGBA {
	return scaler.ScaleEPX2x(src)
}

// ScaleBilinear2x upscales 64x32 to 128x64 using fixed-point integer bilinear interpolation.
func ScaleBilinear2x(src *image.RGBA) *image.RGBA {
	return scaler.ScaleBilinear2x(src)
}

// ScaleBicubic2x upscales 64x32 to 128x64 using Catmull-Rom cubic spline interpolation.
func ScaleBicubic2x(src *image.RGBA) *image.RGBA {
	return scaler.ScaleBicubic2x(src)
}
