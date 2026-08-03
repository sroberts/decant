package images

import (
	"image"
	"image/color"
	"testing"
)

// BenchmarkPixelDigest measures the scratch memory the dedup digest needs.
//
// Spec section 9 budgets memory tightly, and this runs on the source image
// before any scaling, so a large photograph is the worst case. Converting a
// row at a time rather than the whole image keeps the scratch proportional to
// the width instead of the area.
func BenchmarkPixelDigest(b *testing.B) {
	const w, h = 2000, 1500
	img := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	for i := range img.Y {
		img.Y[i] = uint8(i)
	}
	_ = color.YCbCr{}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pixelDigest(img)
	}
}
