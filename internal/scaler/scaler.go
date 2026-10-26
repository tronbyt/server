package scaler

import (
	"image"
	"image/color"
	"strings"
)

// Supported 128x64 upscaling filters.
const (
	FilterBicubic  = "bicubic"
	FilterScale2x  = "scale2x"
	FilterBilinear = "bilinear"
	FilterNearest  = "nearest"
)

// ScaleFrame2x upscales a 64x32 frame to 128x64 using the specified filter algorithm.
// If the filter is unrecognized or empty, it defaults to FilterBicubic.
func ScaleFrame2x(src *image.RGBA, filter string) *image.RGBA {
	if src == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(filter)) {
	case FilterScale2x, "epx", "advmame2x":
		return ScaleEPX2x(src)
	case FilterBilinear:
		return ScaleBilinear2x(src)
	case FilterNearest:
		return ScaleNearest2x(src)
	case FilterBicubic, "":
		return ScaleBicubic2x(src)
	default:
		return ScaleBicubic2x(src)
	}
}

// ScaleNearest2x doubles each 64x32 pixel into a 2x2 block (classic retro LED look).
func ScaleNearest2x(src *image.RGBA) *image.RGBA {
	bounds := src.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w*2, h*2))

	for y := range h {
		for x := range w {
			c := src.RGBAAt(bounds.Min.X+x, bounds.Min.Y+y)
			dx := x * 2
			dy := y * 2
			dst.SetRGBA(dx, dy, c)
			dst.SetRGBA(dx+1, dy, c)
			dst.SetRGBA(dx, dy+1, c)
			dst.SetRGBA(dx+1, dy+1, c)
		}
	}
	return dst
}

// ScaleEPX2x applies the authentic EPX / Scale2x (AdvMAME2x) algorithm.
// It inspects orthogonal neighbors to smooth diagonal edges without introducing blur,
// keeping original palette colors crisp. Ideal for pixel art, clocks, and text.
func ScaleEPX2x(src *image.RGBA) *image.RGBA {
	bounds := src.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w*2, h*2))

	clampX := func(x int) int {
		if x < 0 {
			return 0
		}
		if x >= w {
			return w - 1
		}
		return x
	}
	clampY := func(y int) int {
		if y < 0 {
			return 0
		}
		if y >= h {
			return h - 1
		}
		return y
	}

	for y := range h {
		sy := bounds.Min.Y + y
		yUp := bounds.Min.Y + clampY(y-1)
		yDown := bounds.Min.Y + clampY(y+1)

		for x := range w {
			sx := bounds.Min.X + x
			xLeft := bounds.Min.X + clampX(x-1)
			xRight := bounds.Min.X + clampX(x+1)

			P := src.RGBAAt(sx, sy)
			A := src.RGBAAt(sx, yUp)
			C := src.RGBAAt(xLeft, sy)
			B := src.RGBAAt(xRight, sy)
			D := src.RGBAAt(sx, yDown)

			p1, p2, p3, p4 := P, P, P, P

			if C == A && C != D && A != B {
				p1 = A
			}
			if A == B && A != C && B != D {
				p2 = B
			}
			if D == C && D != B && C != A {
				p3 = C
			}
			if B == D && B != A && D != C {
				p4 = D
			}

			dx := x * 2
			dy := y * 2
			dst.SetRGBA(dx, dy, p1)
			dst.SetRGBA(dx+1, dy, p2)
			dst.SetRGBA(dx, dy+1, p3)
			dst.SetRGBA(dx+1, dy+1, p4)
		}
	}
	return dst
}

// ScaleBilinear2x upscales 64x32 to 128x64 using fixed-point integer bilinear interpolation.
// It creates smooth subpixel color blending between adjacent physical LEDs.
func ScaleBilinear2x(src *image.RGBA) *image.RGBA {
	bounds := src.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w*2, h*2))

	clampX := func(x int) int {
		if x < 0 {
			return 0
		}
		if x >= w {
			return w - 1
		}
		return x
	}
	clampY := func(y int) int {
		if y < 0 {
			return 0
		}
		if y >= h {
			return h - 1
		}
		return y
	}

	blend4 := func(c0, c1, c2, c3 color.RGBA) color.RGBA {
		// Weights: 9/16 for primary, 3/16 for adjacent, 1/16 for diagonal
		r := (uint32(c0.R)*9 + uint32(c1.R)*3 + uint32(c2.R)*3 + uint32(c3.R) + 8) >> 4
		g := (uint32(c0.G)*9 + uint32(c1.G)*3 + uint32(c2.G)*3 + uint32(c3.G) + 8) >> 4
		b := (uint32(c0.B)*9 + uint32(c1.B)*3 + uint32(c2.B)*3 + uint32(c3.B) + 8) >> 4
		a := (uint32(c0.A)*9 + uint32(c1.A)*3 + uint32(c2.A)*3 + uint32(c3.A) + 8) >> 4
		return color.RGBA{R: byte(r), G: byte(g), B: byte(b), A: byte(a)}
	}

	for y := range h {
		sy := bounds.Min.Y + y
		yUp := bounds.Min.Y + clampY(y-1)
		yDown := bounds.Min.Y + clampY(y+1)

		for x := range w {
			sx := bounds.Min.X + x
			xLeft := bounds.Min.X + clampX(x-1)
			xRight := bounds.Min.X + clampX(x+1)

			P := src.RGBAAt(sx, sy)
			up := src.RGBAAt(sx, yUp)
			down := src.RGBAAt(sx, yDown)
			left := src.RGBAAt(xLeft, sy)
			right := src.RGBAAt(xRight, sy)
			upLeft := src.RGBAAt(xLeft, yUp)
			upRight := src.RGBAAt(xRight, yUp)
			downLeft := src.RGBAAt(xLeft, yDown)
			downRight := src.RGBAAt(xRight, yDown)

			// Top-Left (x*2, y*2): leans toward up and left
			pTL := blend4(P, left, up, upLeft)
			// Top-Right (x*2+1, y*2): leans toward right and up
			pTR := blend4(P, right, up, upRight)
			// Bottom-Left (x*2, y*2+1): leans toward left and down
			pBL := blend4(P, left, down, downLeft)
			// Bottom-Right (x*2+1, y*2+1): leans toward right and down
			pBR := blend4(P, right, down, downRight)

			dx := x * 2
			dy := y * 2
			dst.SetRGBA(dx, dy, pTL)
			dst.SetRGBA(dx+1, dy, pTR)
			dst.SetRGBA(dx, dy+1, pBL)
			dst.SetRGBA(dx+1, dy+1, pBR)
		}
	}
	return dst
}

// ScaleBicubic2x upscales 64x32 to 128x64 using Catmull-Rom cubic spline interpolation.
// It preserves sharpness and curvature while eliminating blocky 2x2 stepping,
// making 3D raymarched scenes, lighting gradients, and smooth particle animations look
// high-resolution across all 8,192 LEDs on 128x64 matrix panels.
func ScaleBicubic2x(src *image.RGBA) *image.RGBA {
	bounds := src.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	clampX := func(x int) int {
		if x < 0 {
			return 0
		}
		if x >= w {
			return w - 1
		}
		return x
	}
	clampY := func(y int) int {
		if y < 0 {
			return 0
		}
		if y >= h {
			return h - 1
		}
		return y
	}

	clampByte := func(val int32) byte {
		if val < 0 {
			return 0
		}
		if val > 255 {
			return 255
		}
		return byte(val)
	}

	// Step 1: Horizontal bicubic interpolation: 64x32 -> 128x32
	tmpW := w * 2
	tmp := make([][4]int32, tmpW*h)

	for y := range h {
		sy := bounds.Min.Y + y
		rowOffset := y * tmpW
		for x := range w {
			sx := bounds.Min.X
			cM1 := src.RGBAAt(sx+clampX(x-1), sy)
			c0 := src.RGBAAt(sx+x, sy)
			c1 := src.RGBAAt(sx+clampX(x+1), sy)
			c2 := src.RGBAAt(sx+clampX(x+2), sy)

			// Subpixel left (2x)
			rL := (-18*int32(cM1.R) + 222*int32(c0.R) + 58*int32(c1.R) - 6*int32(c2.R) + 128) >> 8
			gL := (-18*int32(cM1.G) + 222*int32(c0.G) + 58*int32(c1.G) - 6*int32(c2.G) + 128) >> 8
			bL := (-18*int32(cM1.B) + 222*int32(c0.B) + 58*int32(c1.B) - 6*int32(c2.B) + 128) >> 8
			aL := (-18*int32(cM1.A) + 222*int32(c0.A) + 58*int32(c1.A) - 6*int32(c2.A) + 128) >> 8

			// Subpixel right (2x+1)
			rR := (-6*int32(cM1.R) + 58*int32(c0.R) + 222*int32(c1.R) - 18*int32(c2.R) + 128) >> 8
			gR := (-6*int32(cM1.G) + 58*int32(c0.G) + 222*int32(c1.G) - 18*int32(c2.G) + 128) >> 8
			bR := (-6*int32(cM1.B) + 58*int32(c0.B) + 222*int32(c1.B) - 18*int32(c2.B) + 128) >> 8
			aR := (-6*int32(cM1.A) + 58*int32(c0.A) + 222*int32(c1.A) - 18*int32(c2.A) + 128) >> 8

			// Anti-ringing: clamp to local neighborhood bounds to prevent overshoot/haloing
			minR := int32(min(cM1.R, c0.R, c1.R, c2.R))
			maxR := int32(max(cM1.R, c0.R, c1.R, c2.R))
			minG := int32(min(cM1.G, c0.G, c1.G, c2.G))
			maxG := int32(max(cM1.G, c0.G, c1.G, c2.G))
			minB := int32(min(cM1.B, c0.B, c1.B, c2.B))
			maxB := int32(max(cM1.B, c0.B, c1.B, c2.B))
			minA := int32(min(cM1.A, c0.A, c1.A, c2.A))
			maxA := int32(max(cM1.A, c0.A, c1.A, c2.A))

			rL = max(minR, min(maxR, rL))
			rR = max(minR, min(maxR, rR))
			gL = max(minG, min(maxG, gL))
			gR = max(minG, min(maxG, gR))
			bL = max(minB, min(maxB, bL))
			bR = max(minB, min(maxB, bR))
			aL = max(minA, min(maxA, aL))
			aR = max(minA, min(maxA, aR))

			idxL := rowOffset + x*2
			idxR := rowOffset + x*2 + 1
			tmp[idxL] = [4]int32{rL, gL, bL, aL}
			tmp[idxR] = [4]int32{rR, gR, bR, aR}
		}
	}

	// Step 2: Vertical bicubic interpolation: 128x32 -> 128x64
	dst := image.NewRGBA(image.Rect(0, 0, tmpW, h*2))

	for x := range tmpW {
		for y := range h {
			yM1 := clampY(y - 1)
			y0 := y
			y1 := clampY(y + 1)
			y2 := clampY(y + 2)

			pM1 := tmp[yM1*tmpW+x]
			p0 := tmp[y0*tmpW+x]
			p1 := tmp[y1*tmpW+x]
			p2 := tmp[y2*tmpW+x]

			// Top subpixel (2y)
			rT := (-18*pM1[0] + 222*p0[0] + 58*p1[0] - 6*p2[0] + 128) >> 8
			gT := (-18*pM1[1] + 222*p0[1] + 58*p1[1] - 6*p2[1] + 128) >> 8
			bT := (-18*pM1[2] + 222*p0[2] + 58*p1[2] - 6*p2[2] + 128) >> 8
			aT := (-18*pM1[3] + 222*p0[3] + 58*p1[3] - 6*p2[3] + 128) >> 8

			// Bottom subpixel (2y+1)
			rB := (-6*pM1[0] + 58*p0[0] + 222*p1[0] - 18*p2[0] + 128) >> 8
			gB := (-6*pM1[1] + 58*p0[1] + 222*p1[1] - 18*p2[1] + 128) >> 8
			bB := (-6*pM1[2] + 58*p0[2] + 222*p1[2] - 18*p2[2] + 128) >> 8
			aB := (-6*pM1[3] + 58*p0[3] + 222*p1[3] - 18*p2[3] + 128) >> 8

			minR := min(pM1[0], p0[0], p1[0], p2[0])
			maxR := max(pM1[0], p0[0], p1[0], p2[0])
			minG := min(pM1[1], p0[1], p1[1], p2[1])
			maxG := max(pM1[1], p0[1], p1[1], p2[1])
			minB := min(pM1[2], p0[2], p1[2], p2[2])
			maxB := max(pM1[2], p0[2], p1[2], p2[2])
			minA := min(pM1[3], p0[3], p1[3], p2[3])
			maxA := max(pM1[3], p0[3], p1[3], p2[3])

			rT = max(minR, min(maxR, rT))
			rB = max(minR, min(maxR, rB))
			gT = max(minG, min(maxG, gT))
			gB = max(minG, min(maxG, gB))
			bT = max(minB, min(maxB, bT))
			bB = max(minB, min(maxB, bB))
			aT = max(minA, min(maxA, aT))
			aB = max(minA, min(maxA, aB))

			dy := y * 2
			dst.SetRGBA(x, dy, color.RGBA{
				R: clampByte(rT),
				G: clampByte(gT),
				B: clampByte(bT),
				A: clampByte(aT),
			})
			dst.SetRGBA(x, dy+1, color.RGBA{
				R: clampByte(rB),
				G: clampByte(gB),
				B: clampByte(bB),
				A: clampByte(aB),
			})
		}
	}

	return dst
}

// Scale2x scales a 64x32 image to 128x64 using pixel doubling.
func Scale2x(src *image.RGBA) *image.RGBA {
	return ScaleNearest2x(src)
}
