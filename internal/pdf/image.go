package pdf

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // registers the JPEG decoder
	_ "image/png"  // registers the PNG decoder
	"io"

	// hhrutter/tiff rather than x/image/tiff: pdfcpu renders CMYK images to
	// TIFF and the upstream decoder rejects that colour model.
	_ "github.com/hhrutter/tiff"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// ErrUndecodableImage reports an image in a format no pure-Go decoder
// handles. Spec principle 2 rules out cgo, which is what a JPEG 2000 or
// JBIG2 decoder would require.
type ErrUndecodableImage struct {
	// Format is the encoding pdfcpu identified, e.g. "jpx" or "jbig2".
	Format string
	ObjNr  int
}

func (e *ErrUndecodableImage) Error() string {
	return fmt.Sprintf("image object %d is %s, which has no pure-Go decoder",
		e.ObjNr, e.Format)
}

// RawImage is an image XObject as it sits in the PDF, before any processing.
type RawImage struct {
	ObjNr int
	// Encoded is the image's bytes in a standard container.
	Encoded []byte
	// Format is "jpg", "png", or "tif".
	Format string
	// Width and Height are the pixel dimensions.
	Width, Height int
	// DCTPassthrough marks a JPEG whose bytes came straight out of the PDF
	// with no decode. Spec section 4.7 keeps those unmodified when no scaling
	// applies, which preserves quality and skips a decode-encode cycle.
	DCTPassthrough bool
	// HasAlpha reports an /SMask that was composited during decode.
	HasAlpha bool
}

// Decode returns the image as a Go image.Image.
func (r *RawImage) Decode() (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(r.Encoded))
	if err != nil {
		return nil, fmt.Errorf("decoding %s image object %d: %w", r.Format, r.ObjNr, err)
	}
	return img, nil
}

// maxImagePixels caps decoded image size. A hostile or damaged file can
// declare enormous dimensions; 80 megapixels is far above any page image and
// well inside the memory budget in spec section 9.
const maxImagePixels = 80 << 20

// LoadImage extracts one image XObject.
//
// Decoding runs through pdfcpu, which resolves the filter pipeline, the
// colour space, and any /SMask. It hands back a standard container: JPEG
// straight through for DCT-encoded images, PNG for everything it can raster,
// and TIFF for some CMYK cases.
func (d *Document) LoadImage(objNr int) (img *RawImage, err error) {
	defer recoverMalformed(fmt.Sprintf("decoding image object %d", objNr), &err)

	// The value type, not the pointer NewIndirectRef returns: pdfcpu's
	// Dereference switches on types.IndirectRef and hands a *IndirectRef
	// straight back unresolved, which looks like success and fails later.
	o, err := d.ctx.Dereference(*types.NewIndirectRef(objNr, 0))
	if err != nil || o == nil {
		return nil, fmt.Errorf("image object %d not found", objNr)
	}
	sd, ok := o.(types.StreamDict)
	if !ok {
		return nil, fmt.Errorf("object %d is a %T, not an image stream", objNr, o)
	}
	if nameOf(sd.Dict, "Subtype") != "Image" {
		return nil, fmt.Errorf("object %d is not an image XObject", objNr)
	}

	w := intOf(d.ctx.XRefTable, sd.Dict, "Width", 0)
	h := intOf(d.ctx.XRefTable, sd.Dict, "Height", 0)
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("image object %d has no usable dimensions", objNr)
	}
	if w*h > maxImagePixels {
		return nil, fmt.Errorf("image object %d is %dx%d, above the %d pixel cap",
			objNr, w, h, maxImagePixels)
	}

	// ExtractImage rather than RenderImage: it runs the filter-pipeline
	// preparation that RenderImage assumes has already happened. Calling
	// RenderImage directly yields an empty reader on Flate-encoded images.
	extracted, err := pdfcpu.ExtractImage(d.ctx, &sd, false, "", objNr, false)
	if err != nil {
		return nil, fmt.Errorf("extracting image object %d: %w", objNr, err)
	}
	if extracted == nil || extracted.Reader == nil {
		return nil, fmt.Errorf("image object %d could not be rendered", objNr)
	}
	reader, format := extracted.Reader, extracted.FileType

	switch format {
	case "jpx", "jbig2":
		return nil, &ErrUndecodableImage{Format: format, ObjNr: objNr}
	case "jpg", "png", "tif":
	default:
		return nil, &ErrUndecodableImage{Format: format, ObjNr: objNr}
	}

	data, err := io.ReadAll(io.LimitReader(reader, maxImageBytes))
	if err != nil {
		return nil, fmt.Errorf("reading image object %d: %w", objNr, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("image object %d decoded to nothing", objNr)
	}

	_, hasSMask := sd.Dict.Find("SMask")

	return &RawImage{
		ObjNr:   objNr,
		Encoded: data,
		Format:  format,
		Width:   w,
		Height:  h,
		// pdfcpu returns DCT streams verbatim except for CMYK, which it
		// rasters to PNG.
		DCTPassthrough: format == "jpg",
		HasAlpha:       hasSMask,
	}, nil
}

// maxImageBytes caps how much encoded image data is read for one object.
const maxImageBytes = 256 << 20
