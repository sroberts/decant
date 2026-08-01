package decant

import (
	"errors"
	"fmt"
	"sort"

	"github.com/sroberts/decant/internal/images"
	"github.com/sroberts/decant/internal/pdf"
)

// Image is one picture carried into the EPUB.
type Image struct {
	// ID is the manifest id and the basename of the file, e.g. "img001".
	ID string
	// Data is the encoded bytes.
	Data []byte
	// MediaType is the manifest media type.
	MediaType string
	// Ext is the filename extension without a dot.
	Ext string
	// Width and Height are the final pixel dimensions.
	Width, Height int
	// Passthrough reports that the original JPEG bytes were kept unmodified.
	Passthrough bool
}

// Href returns the image's path relative to the OEBPS directory.
func (i Image) Href() string { return "images/" + i.ID + "." + i.Ext }

// imageSet collects processed images for a document and deduplicates them.
//
// Spec section 4.7 deduplicates by SHA-256 of decoded pixel data, so a logo
// repeated on every page becomes one manifest entry. Keying on the pixels
// rather than the object number also merges the same picture stored twice.
type imageSet struct {
	byDigest map[string]string // pixel digest -> image ID
	byObj    map[int]string    // PDF object number -> image ID
	// failed records objects that could not be decoded, so a repeated
	// reference does not retry and re-report.
	failed map[int]bool
	images []Image
}

func newImageSet() *imageSet {
	return &imageSet{
		byDigest: map[string]string{},
		byObj:    map[int]string{},
		failed:   map[int]bool{},
	}
}

// add processes an image and returns its ID, reusing an existing entry when
// the pixels match one already held.
func (s *imageSet) add(cfg images.Config, raw *pdf.RawImage) (string, error) {
	if id, ok := s.byObj[raw.ObjNr]; ok && raw.ObjNr != 0 {
		return id, nil
	}

	enc, err := images.Process(cfg, images.Source{
		Encoded:        raw.Encoded,
		Format:         raw.Format,
		DCTPassthrough: raw.DCTPassthrough,
	})
	if err != nil {
		return "", err
	}

	if id, ok := s.byDigest[enc.Digest]; ok {
		if raw.ObjNr != 0 {
			s.byObj[raw.ObjNr] = id
		}
		return id, nil
	}

	id := fmt.Sprintf("img%03d", len(s.images)+1)
	s.images = append(s.images, Image{
		ID:          id,
		Data:        enc.Data,
		MediaType:   enc.MediaType,
		Ext:         enc.Ext,
		Width:       enc.Width,
		Height:      enc.Height,
		Passthrough: enc.Passthrough,
	})
	s.byDigest[enc.Digest] = id
	if raw.ObjNr != 0 {
		s.byObj[raw.ObjNr] = id
	}
	return id, nil
}

// byID returns an image by its manifest id.
func (s *imageSet) byID(id string) (Image, bool) {
	for _, img := range s.images {
		if img.ID == id {
			return img, true
		}
	}
	return Image{}, false
}

// imagesConfig projects the public options onto the internal image config.
func (c *Converter) imagesConfig() images.Config {
	cfg := images.DefaultConfig()
	cfg.Mode = images.Mode(c.opts.Images)
	cfg.MaxWidth = c.opts.ImageMaxWidth

	if c.opts.Profile == ProfileCrossPoint {
		// Spec 5.1: quantize to 16 gray levels, dither, then JPEG at q90.
		// The panel is 4.3 inch E Ink with no front light, and the stock
		// firmware documents only JPG and BMP.
		cfg.Dither = true
		cfg.GrayLevels = 16
		cfg.DitherQuality = 90
		cfg.ForceJPEG = true
	}
	return cfg
}

// collectPageImages processes the images drawn on one page and returns the
// figures to place, keyed to their manifest ids.
func (c *Converter) collectPageImages(
	src *pdf.Document,
	idx int,
	figures []pdf.ImageDraw,
	set *imageSet,
	rep *Report,
) map[int]string {
	if c.opts.Images == ImagesDrop || len(figures) == 0 {
		return nil
	}
	cfg := c.imagesConfig()
	ids := make(map[int]string, len(figures))

	for _, d := range figures {
		if d.Inline || d.ObjNr == 0 {
			continue
		}
		if set.failed[d.ObjNr] {
			continue
		}
		if id, ok := set.byObj[d.ObjNr]; ok {
			ids[d.Order] = id
			continue
		}

		raw, err := src.LoadImage(d.ObjNr)
		if err != nil {
			set.failed[d.ObjNr] = true
			var und *pdf.ErrUndecodableImage
			if errors.As(err, &und) {
				rep.warn("images", idx, fmt.Sprintf(
					"dropped an image encoded as %s; no pure-Go decoder exists and "+
						"spec principle 2 rules out cgo", und.Format))
			} else {
				rep.warn("images", idx, fmt.Sprintf("dropped an image: %v", err))
			}
			continue
		}

		id, err := set.add(cfg, raw)
		if err != nil {
			set.failed[d.ObjNr] = true
			rep.warn("images", idx, fmt.Sprintf("dropped an image: %v", err))
			continue
		}
		ids[d.Order] = id
	}
	return ids
}

// sortImages orders the manifest deterministically by id.
func (s *imageSet) sorted() []Image {
	out := make([]Image, len(s.images))
	copy(out, s.images)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
