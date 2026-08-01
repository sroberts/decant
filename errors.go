package decant

import "fmt"

// EncryptedError reports a PDF carrying an /Encrypt dictionary. Spec section
// 1 puts decryption out of scope for v1; the CLI maps this to exit code 3.
type EncryptedError struct {
	// Handler names the security handler, e.g. "Standard".
	Handler string
	// Revision is the /R value identifying the algorithm generation.
	Revision int
}

func (e *EncryptedError) Error() string {
	h := e.Handler
	if h == "" {
		h = "unknown"
	}
	return fmt.Sprintf("encrypted PDF (security handler %s, revision %d): decant does not support decryption",
		h, e.Revision)
}

// NoTextLayerError reports a scanned document. decant does not OCR; spec
// section 6 fails fast and points at an external OCR pass. The CLI maps this
// to exit code 4.
type NoTextLayerError struct {
	// MedianGlyphs is the median glyph count across sampled pages.
	MedianGlyphs float64
	// SampledPages is how many pages were examined.
	SampledPages int
	// ImagePageFraction is the fraction of sampled pages covered by
	// page-scale images.
	ImagePageFraction float64
}

func (e *NoTextLayerError) Error() string {
	return fmt.Sprintf(
		"no usable text layer (median %.0f glyphs/page across %d sampled pages, "+
			"%.0f%% of pages covered by full-page images)\n"+
			"       decant does not perform OCR. Run the file through an OCR pass that\n"+
			"       writes a text layer (ocrmypdf, tesseract --pdf), then convert the result.",
		e.MedianGlyphs, e.SampledPages, e.ImagePageFraction*100)
}

// MalformedError reports a PDF damaged beyond what xref reconstruction can
// recover. The CLI maps this to exit code 6.
type MalformedError struct {
	Detail string
	Err    error
}

func (e *MalformedError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("malformed PDF: %s: %v", e.Detail, e.Err)
	}
	return "malformed PDF: " + e.Detail
}

func (e *MalformedError) Unwrap() error { return e.Err }

// UsageError reports an invalid option combination. The CLI maps this to
// exit code 2.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }

func (e *UsageError) Unwrap() error { return e.Err }
