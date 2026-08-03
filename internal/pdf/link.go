package pdf

import (
	"math"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// maxLinksPerPage bounds the annotation walk. A hostile file can declare an
// unbounded /Annots array, and spec principle 2's memory budget does not
// stretch to holding it.
const maxLinksPerPage = 4096

// Link is a /Link annotation resolving to a destination inside the document.
//
// Spec section 4.9 rewrites these to href fragments. External actions (/URI,
// /Launch, /GoToR) are not collected: section 1 puts annotations out of
// scope, and section 4.9 asks only for the internal case.
type Link struct {
	// Rect is the clickable region in page space, y-down from the crop box's
	// top-left with /Rotate applied, so it compares directly against block
	// and line bounds.
	Rect Rect

	// TargetPage is the zero-based destination page.
	TargetPage int
	// TargetY is the destination's vertical position in PDF *user* space, or
	// NaN when the destination names no position. Like OutlineItem.Y this
	// must be converted through the target page's base CTM before it can be
	// compared against block bounds.
	TargetY float64
}

// Links returns the page's internal link annotations.
//
// A damaged or hostile /Annots array yields whatever was collected rather
// than failing the page: a broken annotation is not a reason to lose the
// page's text.
func (d *Document) Links(p *Page) (links []Link) {
	defer func() {
		if r := recover(); r != nil {
			return
		}
	}()
	if p == nil || p.annots == nil {
		return nil
	}

	xref := d.ctx.XRefTable
	for _, a := range p.annots {
		if len(links) >= maxLinksPerPage {
			break
		}
		annot, err := xref.DereferenceDict(a)
		if err != nil || annot == nil {
			continue
		}
		if nameOf(annot, "Subtype") != "Link" {
			continue
		}

		page, y, ok := d.resolveDest(annot)
		if !ok || page < 0 {
			continue
		}

		rect, ok := rectOf(xref, annot["Rect"])
		if !ok {
			continue
		}
		// The annotation rectangle is in user space; page space is what every
		// consumer compares against.
		x0, y0 := p.baseCTM.Apply(rect.MinX, rect.MinY)
		x1, y1 := p.baseCTM.Apply(rect.MaxX, rect.MaxY)

		links = append(links, Link{
			Rect: Rect{
				MinX: math.Min(x0, x1), MaxX: math.Max(x0, x1),
				MinY: math.Min(y0, y1), MaxY: math.Max(y0, y1),
			},
			TargetPage: page,
			TargetY:    y,
		})
	}
	return links
}

// rectOf reads a four-number array as a rectangle, normalizing the corners.
// The PDF spec does not require /Rect to be stored lower-left to upper-right.
func rectOf(xref *model.XRefTable, v types.Object) (Rect, bool) {
	arr, err := xref.DereferenceArray(v)
	if err != nil || len(arr) < 4 {
		return Rect{}, false
	}
	var n [4]float64
	for i := 0; i < 4; i++ {
		f, ok := numOf(xref, arr[i])
		if !ok {
			return Rect{}, false
		}
		n[i] = f
	}
	return Rect{
		MinX: math.Min(n[0], n[2]), MinY: math.Min(n[1], n[3]),
		MaxX: math.Max(n[0], n[2]), MaxY: math.Max(n[1], n[3]),
	}, true
}
