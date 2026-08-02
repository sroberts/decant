package pdf

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// ErrEncrypted reports a PDF carrying an /Encrypt dictionary. Spec section 1
// puts decryption out of scope for v1; the CLI maps this to exit code 3.
type ErrEncrypted struct {
	// Handler names the security handler, e.g. "Standard".
	Handler string
	// Revision is the /R value, which identifies the algorithm generation.
	Revision int
}

func (e *ErrEncrypted) Error() string {
	h := e.Handler
	if h == "" {
		h = "unknown"
	}
	return fmt.Sprintf("encrypted PDF (security handler %s, revision %d): decant does not support decryption", h, e.Revision)
}

// ErrMalformed reports a PDF damaged beyond what xref reconstruction can
// recover. The CLI maps this to exit code 6.
type ErrMalformed struct {
	Detail string
	Err    error
}

func (e *ErrMalformed) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("malformed PDF: %s: %v", e.Detail, e.Err)
	}
	return "malformed PDF: " + e.Detail
}

func (e *ErrMalformed) Unwrap() error { return e.Err }

// Info holds document-level metadata drawn from /Info, the catalog, and XMP.
type Info struct {
	Title    string
	Author   string
	Subject  string
	Keywords string
	Creator  string
	Producer string
	Language string
	Created  time.Time
	Modified time.Time
}

// OutlineItem is one node of the PDF outline (bookmark) tree.
type OutlineItem struct {
	Title string
	// Page is the zero-based page index the destination resolves to, or -1
	// when the destination could not be resolved.
	Page int
	// Y is the destination's vertical position in PDF *user* space, or NaN
	// when the destination does not specify one. Stage 6 matches an outline
	// entry to the nearest block below it and must convert this through the
	// target page's base CTM first, since page space runs y-down.
	Y float64

	Children []OutlineItem
}

// Page carries everything stage 2 needs to interpret one page.
type Page struct {
	// Index is zero-based.
	Index int
	// Width and Height are the displayed page dimensions in points, after
	// /Rotate applies.
	Width, Height float64
	// Rotate is the page's /Rotate value, normalized to 0, 90, 180, or 270.
	Rotate int

	content []byte
	res     types.Dict
	baseCTM Matrix
}

// ToPageSpace converts a point from PDF user space to this page's space,
// applying the crop box offset, the y-axis flip, and /Rotate.
//
// Outline destinations are the main caller: OutlineItem.Y is in user space
// and has to come through here before it can be compared against block
// bounds, which are in page space.
func (p *Page) ToPageSpace(x, y float64) (float64, float64) {
	return p.baseCTM.Apply(x, y)
}

// Document is an opened PDF.
type Document struct {
	ctx   *model.Context
	fonts *FontCache

	pageCount int
	info      Info
	outline   []OutlineItem
}

// recoverMalformed converts a panic into an *ErrMalformed stored through
// errp.
//
// Spec section 10 requires the parser to survive hostile input without
// panicking, but object model and xref handling run through pdfcpu, which
// offers no such guarantee: FuzzOpen reaches a nil dereference inside
// EnsurePageCount within seconds. Recovering at each entry point keeps that
// contract for callers, who see a normal malformed-PDF error and exit code 6.
func recoverMalformed(detail string, errp *error) {
	if r := recover(); r != nil {
		*errp = &ErrMalformed{
			Detail: detail,
			Err:    fmt.Errorf("parser panic: %v", r),
		}
	}
}

func init() {
	// Cut pdfcpu off from the user's config directory.
	//
	// NewDefaultConfiguration otherwise reads $XDG_CONFIG_HOME/pdfcpu/config.yml
	// and creates it when absent, caching the result in an unsynchronized
	// package global and calling fault.Fail (a panic) on any problem. Three
	// things follow that decant cannot accept. Two goroutines opening
	// documents at once race on that global and on the file itself, which is
	// how CI first saw "config problem: EOF" from a half-written file.
	// Behaviour would depend on a file outside the input, against the
	// determinism guarantee. And a stray config would turn every conversion
	// on the machine into ErrMalformed.
	//
	// "disable" is pdfcpu's own sentinel for the built-in defaults.
	model.ConfigPath = "disable"
}

// Open reads a PDF. It returns *ErrEncrypted for encrypted files and
// *ErrMalformed when the xref cannot be recovered.
func Open(r io.ReaderAt, size int64) (doc *Document, err error) {
	defer recoverMalformed("reading document structure", &err)

	rs := io.NewSectionReader(r, 0, size)

	conf := model.NewDefaultConfiguration()
	// Validation relaxed: the corpus includes files that trip strict checks
	// but still yield usable text. Spec principle 3 prefers a diagnostic over
	// a refusal.
	conf.ValidationMode = model.ValidationRelaxed
	conf.WriteXRefStream = false

	ctx, err := api.ReadContext(rs, conf)
	if err != nil {
		// pdfcpu reports encryption as a read error for some handlers, so
		// check before concluding the file is malformed.
		if enc := sniffEncryption(r, size); enc != nil {
			return nil, enc
		}
		return nil, &ErrMalformed{Detail: "cannot read xref", Err: err}
	}
	if ctx == nil || ctx.XRefTable == nil {
		return nil, &ErrMalformed{Detail: "no cross-reference table"}
	}

	if e := encryptionError(ctx); e != nil {
		return nil, e
	}

	// EnsurePageCount dereferences the page tree root without checking it
	// resolved, so confirm the catalog actually names one first.
	if root, perr := ctx.XRefTable.Pages(); perr != nil || root == nil {
		return nil, &ErrMalformed{Detail: "catalog names no page tree", Err: perr}
	}
	if err := ctx.EnsurePageCount(); err != nil {
		return nil, &ErrMalformed{Detail: "cannot resolve page tree", Err: err}
	}
	if ctx.PageCount <= 0 {
		return nil, &ErrMalformed{Detail: "document has no pages"}
	}

	d := &Document{
		ctx:       ctx,
		fonts:     NewFontCache(),
		pageCount: ctx.PageCount,
	}
	d.info = d.readInfo()
	d.outline = d.readOutline()
	return d, nil
}

// encryptionError returns an *ErrEncrypted when the context carries an
// /Encrypt dictionary.
func encryptionError(ctx *model.Context) error {
	if ctx.XRefTable.Encrypt == nil {
		return nil
	}
	e := &ErrEncrypted{}
	if d, err := ctx.DereferenceDict(*ctx.XRefTable.Encrypt); err == nil && d != nil {
		e.Handler = nameOf(d, "Filter")
		if v, ok := numOf(ctx.XRefTable, d["R"]); ok {
			e.Revision = int(v)
		}
	}
	return e
}

// sniffEncryption looks for an /Encrypt entry in the raw bytes when the
// normal read path failed before the trailer was available. It scans the tail
// of the file, where trailers live.
func sniffEncryption(r io.ReaderAt, size int64) error {
	const window = 8 << 10
	off := size - window
	n := int64(window)
	if off < 0 {
		off, n = 0, size
	}
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	if !strings.Contains(string(buf), "/Encrypt") {
		return nil
	}
	return &ErrEncrypted{Handler: "Standard"}
}

// PageCount returns the number of pages.
func (d *Document) PageCount() int { return d.pageCount }

// Info returns document metadata.
func (d *Document) Info() Info { return d.info }

// Outline returns the bookmark tree, empty when the document has none.
func (d *Document) Outline() []OutlineItem { return d.outline }

// Page loads page i, zero-based.
func (d *Document) Page(i int) (p *Page, err error) {
	if i < 0 || i >= d.pageCount {
		return nil, fmt.Errorf("page %d out of range (document has %d)", i+1, d.pageCount)
	}
	defer recoverMalformed(fmt.Sprintf("reading page %d", i+1), &err)

	// pdfcpu numbers pages from one.
	dict, _, attrs, err := d.ctx.PageDict(i+1, false)
	if err != nil {
		return nil, &ErrMalformed{Detail: fmt.Sprintf("page %d dictionary", i+1), Err: err}
	}
	if dict == nil {
		return nil, &ErrMalformed{Detail: fmt.Sprintf("page %d has no dictionary", i+1)}
	}

	box := attrs.CropBox
	if box == nil {
		box = attrs.MediaBox
	}
	if box == nil {
		// US Letter, the least-surprising default for a page that declares
		// no boundary at all.
		box = types.NewRectangle(0, 0, 612, 792)
	}

	rot := ((attrs.Rotate % 360) + 360) % 360
	rot -= rot % 90

	p = &Page{
		Index:   i,
		Rotate:  rot,
		res:     attrs.Resources,
		baseCTM: baseCTM(box, rot),
	}
	w, h := box.Width(), box.Height()
	if rot == 90 || rot == 270 {
		p.Width, p.Height = h, w
	} else {
		p.Width, p.Height = w, h
	}

	// A page with no content stream is legal and renders blank.
	content, err := d.ctx.PageContent(dict, i+1)
	if err == nil {
		p.content = content
	}
	return p, nil
}

// baseCTM maps PDF user space to page space: origin at the top-left of the
// crop box, y increasing downward, with /Rotate applied.
func baseCTM(box *types.Rectangle, rotate int) Matrix {
	minX, minY := box.LL.X, box.LL.Y
	maxX, maxY := box.UR.X, box.UR.Y

	switch rotate {
	case 90:
		return Matrix{A: 0, B: 1, C: 1, D: 0, E: -minY, F: -minX}
	case 180:
		return Matrix{A: -1, B: 0, C: 0, D: 1, E: maxX, F: -minY}
	case 270:
		return Matrix{A: 0, B: -1, C: -1, D: 0, E: maxY, F: maxX}
	default:
		return Matrix{A: 1, B: 0, C: 0, D: -1, E: -minX, F: maxY}
	}
}

// Glyphs interprets the page's content stream.
//
// A parser panic while resolving fonts or form XObjects yields whatever was
// extracted so far with Recovered set, rather than taking down the process.
func (d *Document) Glyphs(p *Page) (pc *PageContent) {
	if len(p.content) == 0 {
		return &PageContent{}
	}
	defer func() {
		if r := recover(); r != nil {
			pc = &PageContent{Recovered: true}
		}
	}()
	return Interpret(d.ctx.XRefTable, d.fonts, p.content, p.res, p.baseCTM)
}

// readInfo pulls metadata from the /Info dictionary and the catalog.
func (d *Document) readInfo() Info {
	var inf Info
	// Metadata is optional; a damaged /Info must not fail the whole open.
	defer func() { _ = recover() }()
	xref := d.ctx.XRefTable

	if xref.Info != nil {
		if dict, err := xref.DereferenceDict(*xref.Info); err == nil && dict != nil {
			inf.Title = textString(xref, dict, "Title")
			inf.Author = textString(xref, dict, "Author")
			inf.Subject = textString(xref, dict, "Subject")
			inf.Keywords = textString(xref, dict, "Keywords")
			inf.Creator = textString(xref, dict, "Creator")
			inf.Producer = textString(xref, dict, "Producer")
			inf.Created = parsePDFDate(textString(xref, dict, "CreationDate"))
			inf.Modified = parsePDFDate(textString(xref, dict, "ModDate"))
		}
	}

	if cat, err := xref.Catalog(); err == nil && cat != nil {
		if l, err := xref.DereferenceText(cat["Lang"]); err == nil && l != "" {
			inf.Language = strings.TrimSpace(l)
		}
	}
	return inf
}

// textString reads a PDF text string, handling the UTF-16BE BOM form and the
// PDFDocEncoding default.
func textString(xref *model.XRefTable, d types.Dict, key string) string {
	raw, err := xref.DereferenceStringEntryBytes(d, key)
	if err != nil || len(raw) == 0 {
		// Some producers store these as names rather than strings.
		return strings.TrimSpace(nameOf(d, key))
	}
	return strings.TrimSpace(decodeTextString(raw))
}

// decodeTextString converts PDF text string bytes to a Go string.
func decodeTextString(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		u := make([]uint16, 0, (len(b)-2)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		}
		return string(utf16.Decode(u))
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		u := make([]uint16, 0, (len(b)-2)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
		}
		return string(utf16.Decode(u))
	}
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		if r := PDFDocEncoding[c]; r != 0 {
			sb.WriteRune(r)
		} else if c >= 0x20 {
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}

// parsePDFDate parses the D:YYYYMMDDHHmmSSOHH'mm' form. It returns the zero
// time on any failure; callers fall back to other timestamp sources.
func parsePDFDate(s string) time.Time {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "D:")
	if len(s) < 4 {
		return time.Time{}
	}
	// Normalize the timezone tail: PDF writes +HH'mm' where Go wants +HHmm.
	s = strings.ReplaceAll(s, "'", "")

	layouts := []string{
		"20060102150405-0700",
		"20060102150405Z",
		"20060102150405",
		"200601021504",
		"2006010215",
		"20060102",
		"200601",
		"2006",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
		// Trailing "Z" with no offset, and offsets given as just "+HH".
		if len(s) > len(l) {
			if t, err := time.Parse(l, s[:len(l)]); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// readOutline walks the /Outlines tree.
func (d *Document) readOutline() (items []OutlineItem) {
	// The outline is optional; a damaged one must not fail the whole open.
	defer func() {
		if r := recover(); r != nil {
			items = nil
		}
	}()
	xref := d.ctx.XRefTable
	cat, err := xref.Catalog()
	if err != nil || cat == nil {
		return nil
	}
	root, err := xref.DereferenceDict(cat["Outlines"])
	if err != nil || root == nil {
		return nil
	}
	seen := map[int]bool{}
	return d.outlineChildren(root, seen, 0)
}

// maxOutlineDepth bounds recursion into a possibly cyclic outline tree.
const maxOutlineDepth = 32

func (d *Document) outlineChildren(node types.Dict, seen map[int]bool, depth int) []OutlineItem {
	if depth >= maxOutlineDepth {
		return nil
	}
	xref := d.ctx.XRefTable

	var items []OutlineItem
	cur := node["First"]

	for i := 0; i < 4096; i++ { // bound on siblings
		if cur == nil {
			break
		}
		if ind, ok := cur.(types.IndirectRef); ok {
			objNr := ind.ObjectNumber.Value()
			if seen[objNr] {
				break
			}
			seen[objNr] = true
		}
		item, err := xref.DereferenceDict(cur)
		if err != nil || item == nil {
			break
		}

		oi := OutlineItem{
			Title: textString(xref, item, "Title"),
			Page:  -1,
			Y:     math.NaN(),
		}
		if pg, y, ok := d.resolveDest(item); ok {
			oi.Page, oi.Y = pg, y
		}
		oi.Children = d.outlineChildren(item, seen, depth+1)

		if oi.Title != "" || len(oi.Children) > 0 {
			items = append(items, oi)
		}

		cur = item["Next"]
	}
	return items
}

// resolveDest resolves an outline item's destination to a page index and a y
// position in page space.
func (d *Document) resolveDest(item types.Dict) (int, float64, bool) {
	xref := d.ctx.XRefTable

	dest := item["Dest"]
	if dest == nil {
		// An /A action with /S /GoTo carries the destination instead.
		if act, err := xref.DereferenceDict(item["A"]); err == nil && act != nil {
			if nameOf(act, "S") == "GoTo" {
				dest = act["D"]
			}
		}
	}
	if dest == nil {
		return -1, 0, false
	}

	resolved, err := xref.Dereference(dest)
	if err != nil {
		return -1, 0, false
	}

	switch v := resolved.(type) {
	case types.Array:
		return d.destFromArray(v)
	case types.Name:
		arr, err := xref.DereferenceDestArray(v.Value())
		if err != nil || arr == nil {
			return -1, 0, false
		}
		return d.destFromArray(arr)
	case types.StringLiteral:
		arr, err := xref.DereferenceDestArray(v.Value())
		if err != nil || arr == nil {
			return -1, 0, false
		}
		return d.destFromArray(arr)
	case types.HexLiteral:
		arr, err := xref.DereferenceDestArray(v.Value())
		if err != nil || arr == nil {
			return -1, 0, false
		}
		return d.destFromArray(arr)
	}
	return -1, 0, false
}

// destFromArray reads [pageRef /XYZ left top zoom] and its siblings.
func (d *Document) destFromArray(arr types.Array) (int, float64, bool) {
	if len(arr) == 0 {
		return -1, 0, false
	}
	ind, ok := arr[0].(types.IndirectRef)
	if !ok {
		return -1, 0, false
	}
	pageNr, err := d.ctx.XRefTable.PageNumber(ind.ObjectNumber.Value())
	if err != nil || pageNr <= 0 {
		return -1, 0, false
	}
	page := pageNr - 1

	// The vertical coordinate sits at a different index per destination type:
	// [page /XYZ left top zoom] versus [page /FitH top].
	y := math.NaN()
	if len(arr) >= 2 {
		switch nameOf2(arr[1]) {
		case "XYZ":
			if len(arr) >= 4 {
				if v, ok := numOf(d.ctx.XRefTable, arr[3]); ok {
					y = v
				}
			}
		case "FitH", "FitBH":
			if len(arr) >= 3 {
				if v, ok := numOf(d.ctx.XRefTable, arr[2]); ok {
					y = v
				}
			}
		}
	}
	return page, y, true
}

func nameOf2(o types.Object) string {
	if n, ok := o.(types.Name); ok {
		return n.Value()
	}
	return ""
}
