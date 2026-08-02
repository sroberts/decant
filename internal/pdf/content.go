package pdf

import (
	"math"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// maxFormDepth bounds Form XObject recursion. Legitimate nesting is shallow;
// a cycle in a damaged file would otherwise spin forever.
const maxFormDepth = 12

// maxGlyphsPerPage caps emission so a hostile content stream cannot exhaust
// memory. A dense page carries roughly 5,000 glyphs; this is three orders of
// magnitude above that.
const maxGlyphsPerPage = 2_000_000

// gstate is the subset of PDF graphics and text state that affects glyph
// position and appearance.
type gstate struct {
	ctm       Matrix
	font      *Font
	fontID    FontID
	fontSize  float64
	charSp    float64 // Tc
	wordSp    float64 // Tw
	hscale    float64 // Tz / 100
	leading   float64 // TL
	rise      float64 // Ts
	render    uint8   // Tr
	lineWidth float64 // w
}

func newGState(ctm Matrix) gstate {
	// A line width of 1 is the PDF default; 0 means the thinnest line the
	// device can render, which for rule detection behaves the same way.
	return gstate{ctm: ctm, hscale: 1, fontID: NoFont, lineWidth: 1}
}

// interpreter walks one page's content streams and emits glyphs.
type interpreter struct {
	xref  *model.XRefTable
	fonts *FontCache

	gs    gstate
	stack []gstate

	// tm is the text matrix, tlm the text line matrix. Both are meaningful
	// only between BT and ET.
	tm, tlm Matrix

	glyphs []Glyph
	images []ImageDraw

	// path accumulates the extent of the path under construction, reset at
	// each painting operator. subpaths holds each segment's own extent, so a
	// path built from several lines contributes several candidate rules
	// rather than one box spanning them all.
	path      Rect
	pathEmpty bool
	subpaths  []Rect
	// cur tracks the extent of the subpath being built.
	cur      Rect
	curEmpty bool

	vectorPaints int
	vectorBounds Rect
	rules        []Rule

	// fontTable is the per-page list FontID indexes into.
	fontTable []*Font
	fontIndex map[FontRef]FontID

	// truncated records that emission hit maxGlyphsPerPage.
	truncated bool
}

// PageContent is the result of interpreting one page.
type PageContent struct {
	Glyphs []Glyph
	Fonts  []*Font
	// Images are the image XObjects drawn on the page, in draw order, with
	// the placement rectangle taken from the CTM at draw time.
	Images []ImageDraw
	// Truncated reports that the glyph cap was hit and output is incomplete.
	Truncated bool
	// Recovered reports that interpretation aborted on a parser panic and
	// the page's text is incomplete or absent.
	Recovered bool

	// VectorPaints counts painted path operations on the page, and
	// VectorBounds is the union of their extents in page space.
	//
	// decant does not render vector artwork; spec section 1 puts conversion to
	// SVG out of scope for v1 and section 13 keeps rasterization open. These
	// exist so the loss is reported rather than silent, which spec principle 3
	// requires.
	VectorPaints int
	VectorBounds Rect

	// Rules are the axis-aligned thin painted segments on the page, which is
	// what table ruling lines look like in a content stream. Spec section 4.8
	// keys grid detection on them.
	Rules []Rule
}

// Rule is one axis-aligned line segment painted on the page.
//
// Spec section 4.8 detects tables from `re` and `l` operators with a stroke
// width under 2 pt. Both shapes reduce to the same thing once the CTM has been
// applied: a thin segment with a length, a position, and an orientation. A
// filled rectangle a fraction of a point tall is a rule drawn the other common
// way, so fills are recorded on the same footing as strokes.
type Rule struct {
	// Bounds is the segment's extent in page space.
	Bounds Rect
	// Horizontal reports the orientation. A segment thinner than it is long
	// runs along its longer axis.
	Horizontal bool
	// Thickness is the segment's smaller dimension in points, which is the
	// stroke width for a stroked line and the height or width for a filled
	// rectangle.
	Thickness float64
	// Filled distinguishes a filled rectangle from a stroked path.
	Filled bool
}

// Length returns the segment's extent along its own axis.
func (r Rule) Length() float64 {
	if r.Horizontal {
		return r.Bounds.Width()
	}
	return r.Bounds.Height()
}

// ImageDraw is one image painted on a page.
//
// Spec section 4.7 needs the placement rectangle, which exists only at draw
// time: a PDF image XObject is always the unit square, and the CTM in force
// when Do executes is what gives it a size and position on the page.
type ImageDraw struct {
	// ObjNr is the image XObject's object number, and the key images are
	// deduplicated by. Zero for a direct or inline image.
	ObjNr int
	// Name is the resource dictionary key.
	Name string
	// Rect is the placement rectangle in page space.
	Rect Rect
	// Rotation is the placement angle in degrees.
	Rotation float64
	// GlyphsBefore is how many glyphs had been emitted when this image was
	// drawn. Zero on a page that also carries text identifies a background
	// or watermark painted beneath it.
	GlyphsBefore int
	// Order is the draw index within the page, which keeps sorting stable
	// when two images share a position.
	Order int
	// Inline marks a BI/ID/EI inline image. Its placement is recorded but its
	// data is not extracted.
	Inline bool
}

// Width returns the placement width in points.
func (d ImageDraw) Width() float64 { return d.Rect.Width() }

// Height returns the placement height in points.
func (d ImageDraw) Height() float64 { return d.Rect.Height() }

// FontCache memoizes font loading across pages of one document. Fonts are
// routinely shared by every page, and rebuilding a CMap per page dominates
// parse time on large documents.
type FontCache struct {
	byObj map[int]*Font
}

// NewFontCache returns an empty cache.
func NewFontCache() *FontCache { return &FontCache{byObj: map[int]*Font{}} }

// Interpret runs the content stream for a page and returns its glyphs.
//
// baseCTM maps PDF user space to the output page space, which flips the y
// axis so that increasing y runs down the page. Downstream stages sort top to
// bottom and assume that orientation.
func Interpret(xref *model.XRefTable, fc *FontCache, content []byte, res types.Dict, baseCTM Matrix) *PageContent {
	ip := &interpreter{
		xref:      xref,
		fonts:     fc,
		gs:        newGState(baseCTM),
		fontIndex: map[FontRef]FontID{},
		pathEmpty: true,
		curEmpty:  true,
	}
	ip.run(content, res, 0)
	return &PageContent{
		Glyphs:       ip.glyphs,
		Fonts:        ip.fontTable,
		Images:       ip.images,
		Truncated:    ip.truncated,
		VectorPaints: ip.vectorPaints,
		VectorBounds: ip.vectorBounds,
		Rules:        ip.rules,
	}
}

func (ip *interpreter) run(content []byte, res types.Dict, depth int) {
	l := newLexer(content)
	var operands []object

	// operandLimit bounds the operand buffer. TJ arrays are the only long
	// operand and they arrive as a single array token.
	const operandLimit = 32

	for {
		o, ok := l.next()
		if !ok {
			return
		}
		if o.kind != kOp {
			if len(operands) < operandLimit {
				operands = append(operands, o)
			}
			continue
		}

		ip.exec(string(o.str), operands, res, depth, l)
		operands = operands[:0]

		if ip.truncated {
			return
		}
	}
}

//nolint:gocyclo // A flat switch over the operator set is the clearest form.
func (ip *interpreter) exec(op string, args []object, res types.Dict, depth int, l *lexer) {
	switch op {

	// --- graphics state ---
	case "q":
		if len(ip.stack) < 256 {
			ip.stack = append(ip.stack, ip.gs)
		}
	case "Q":
		if n := len(ip.stack); n > 0 {
			ip.gs = ip.stack[n-1]
			ip.stack = ip.stack[:n-1]
		}
	case "cm":
		if len(args) >= 6 {
			m := Matrix{
				A: args[0].float(), B: args[1].float(),
				C: args[2].float(), D: args[3].float(),
				E: args[4].float(), F: args[5].float(),
			}
			ip.gs.ctm = m.Mul(ip.gs.ctm)
		}
	case "gs":
		if len(args) >= 1 {
			ip.applyExtGState(args[len(args)-1].name(), res)
		}
	case "w":
		if len(args) >= 1 {
			ip.gs.lineWidth = args[len(args)-1].float()
		}

	// --- text objects ---
	case "BT":
		ip.tm, ip.tlm = Identity, Identity
	case "ET":
		ip.tm, ip.tlm = Identity, Identity

	case "Tf":
		if len(args) >= 2 {
			ip.setFont(args[len(args)-2].name(), args[len(args)-1].float(), res)
		}
	case "Td":
		if len(args) >= 2 {
			ip.nextLine(args[len(args)-2].float(), args[len(args)-1].float())
		}
	case "TD":
		if len(args) >= 2 {
			ty := args[len(args)-1].float()
			ip.gs.leading = -ty
			ip.nextLine(args[len(args)-2].float(), ty)
		}
	case "Tm":
		if len(args) >= 6 {
			ip.tlm = Matrix{
				A: args[0].float(), B: args[1].float(),
				C: args[2].float(), D: args[3].float(),
				E: args[4].float(), F: args[5].float(),
			}
			ip.tm = ip.tlm
		}
	case "T*":
		ip.nextLine(0, -ip.gs.leading)
	case "TL":
		if len(args) >= 1 {
			ip.gs.leading = args[len(args)-1].float()
		}
	case "Tc":
		if len(args) >= 1 {
			ip.gs.charSp = args[len(args)-1].float()
		}
	case "Tw":
		if len(args) >= 1 {
			ip.gs.wordSp = args[len(args)-1].float()
		}
	case "Tz":
		if len(args) >= 1 {
			ip.gs.hscale = args[len(args)-1].float() / 100
		}
	case "Ts":
		if len(args) >= 1 {
			ip.gs.rise = args[len(args)-1].float()
		}
	case "Tr":
		if len(args) >= 1 {
			m := args[len(args)-1].float()
			if m >= 0 && m <= 7 {
				ip.gs.render = uint8(m)
			}
		}

	// --- show text ---
	case "Tj":
		if len(args) >= 1 && args[len(args)-1].kind == kString {
			ip.showText(args[len(args)-1].str)
		}
	case "'":
		ip.nextLine(0, -ip.gs.leading)
		if len(args) >= 1 && args[len(args)-1].kind == kString {
			ip.showText(args[len(args)-1].str)
		}
	case "\"":
		if len(args) >= 3 {
			ip.gs.wordSp = args[len(args)-3].float()
			ip.gs.charSp = args[len(args)-2].float()
			ip.nextLine(0, -ip.gs.leading)
			if args[len(args)-1].kind == kString {
				ip.showText(args[len(args)-1].str)
			}
		}
	case "TJ":
		if len(args) >= 1 && args[len(args)-1].kind == kArray {
			for _, e := range args[len(args)-1].arr {
				switch e.kind {
				case kString:
					ip.showText(e.str)
				case kNum:
					// A positive adjustment moves left, hence the negation.
					tx := -e.num / 1000 * ip.gs.fontSize * ip.gs.hscale
					ip.tm = Translate(tx, 0).Mul(ip.tm)
				}
			}
		}

	// --- path construction ---
	//
	// Coordinates are collected only to bound the artwork for the dropped-
	// vector diagnostic. A curve's control points bound its curve, so taking
	// them verbatim is a safe over-estimate.
	case "m":
		if len(args) >= 2 {
			// A move ends the current subpath and starts a new one.
			ip.endSubpath()
			ip.addPathPoint(args[len(args)-2].float(), args[len(args)-1].float())
		}
	case "l":
		if len(args) >= 2 {
			ip.addPathPoint(args[len(args)-2].float(), args[len(args)-1].float())
		}
	case "c":
		if len(args) >= 6 {
			for i := 0; i+1 < 6; i += 2 {
				ip.addPathPoint(args[i].float(), args[i+1].float())
			}
		}
	case "v", "y":
		if len(args) >= 4 {
			for i := 0; i+1 < 4; i += 2 {
				ip.addPathPoint(args[i].float(), args[i+1].float())
			}
		}
	case "re":
		if len(args) >= 4 {
			// A rectangle is a complete subpath of its own.
			ip.endSubpath()
			x, y := args[0].float(), args[1].float()
			w, h := args[2].float(), args[3].float()
			ip.addPathPoint(x, y)
			ip.addPathPoint(x+w, y)
			ip.addPathPoint(x+w, y+h)
			ip.addPathPoint(x, y+h)
			ip.endSubpath()
		}
	case "h":
		// Close the subpath; adds no new geometry.

	// --- path painting ---
	//
	// n paints nothing. It is the second half of the "W n" clipping idiom, so
	// counting it would report every clip region as dropped artwork.
	case "S", "s":
		ip.paintPath(false)
	case "f", "F", "f*", "B", "B*", "b", "b*":
		// The B family both fills and strokes. Recording them as filled is
		// the conservative reading: a filled rule's thickness is its own
		// geometry rather than the pen width.
		ip.paintPath(true)
	case "n":
		ip.resetPath()

	// --- XObjects ---
	case "Do":
		if len(args) >= 1 {
			ip.doXObject(args[len(args)-1].name(), res, depth)
		}
	case "BI":
		// Inline image. The payload is binary and not tokenizable, so only
		// its placement is recorded; spec section 4.7 extracts image
		// XObjects, and inline images are small by construction.
		ip.recordImage(0, "", true)
		l.skipInlineImage()
	}
}

// nextLine implements Td: move to the start of the next line, offset from the
// current line's origin.
func (ip *interpreter) nextLine(tx, ty float64) {
	ip.tlm = Translate(tx, ty).Mul(ip.tlm)
	ip.tm = ip.tlm
}

// setFont resolves a font resource name and installs it in the graphics state.
func (ip *interpreter) setFont(name string, size float64, res types.Dict) {
	ip.gs.fontSize = size
	if name == "" || res == nil {
		return
	}
	fonts := dictOf(ip.xref, res, "Font")
	if fonts == nil {
		return
	}
	raw, found := fonts[name]
	if !found {
		return
	}

	ref := FontRef{Name: name}
	if ind, ok := raw.(types.IndirectRef); ok {
		ref.ObjNr = ind.ObjectNumber.Value()
		ref.Name = ""
	}

	// Document-level cache, keyed by object number. Direct font dicts have no
	// object number and are re-parsed per reference, which is rare.
	if ref.ObjNr != 0 {
		if f, ok := ip.fonts.byObj[ref.ObjNr]; ok {
			ip.gs.font = f
			ip.gs.fontID = ip.internFont(f)
			return
		}
	}

	d, err := ip.xref.DereferenceDict(raw)
	if err != nil || d == nil {
		return
	}
	f := LoadFont(ip.xref, ref, d)
	if ref.ObjNr != 0 {
		ip.fonts.byObj[ref.ObjNr] = f
	}
	ip.gs.font = f
	ip.gs.fontID = ip.internFont(f)
}

// internFont assigns the page-local FontID for a font.
func (ip *interpreter) internFont(f *Font) FontID {
	if id, ok := ip.fontIndex[f.Ref]; ok {
		return id
	}
	if len(ip.fontTable) >= int(NoFont) {
		return NoFont
	}
	id := FontID(len(ip.fontTable))
	ip.fontTable = append(ip.fontTable, f)
	ip.fontIndex[f.Ref] = id
	return id
}

// applyExtGState handles the subset of /ExtGState that affects text: the
// /Font entry, which sets font and size together.
func (ip *interpreter) applyExtGState(name string, res types.Dict) {
	if name == "" || res == nil {
		return
	}
	egs := dictOf(ip.xref, res, "ExtGState")
	if egs == nil {
		return
	}
	g := dictOf(ip.xref, egs, name)
	if g == nil {
		return
	}
	arr, err := ip.xref.DereferenceArray(g["Font"])
	if err != nil || len(arr) < 2 {
		return
	}
	size, _ := numOf(ip.xref, arr[1])
	ip.gs.fontSize = size

	ref := FontRef{}
	if ind, ok := arr[0].(types.IndirectRef); ok {
		ref.ObjNr = ind.ObjectNumber.Value()
		if f, ok := ip.fonts.byObj[ref.ObjNr]; ok {
			ip.gs.font = f
			ip.gs.fontID = ip.internFont(f)
			return
		}
	}
	d, err := ip.xref.DereferenceDict(arr[0])
	if err != nil || d == nil {
		return
	}
	f := LoadFont(ip.xref, ref, d)
	if ref.ObjNr != 0 {
		ip.fonts.byObj[ref.ObjNr] = f
	}
	ip.gs.font = f
	ip.gs.fontID = ip.internFont(f)
}

// showText emits one glyph per decoded code and advances the text matrix.
func (ip *interpreter) showText(s []byte) {
	f := ip.gs.font
	if f == nil || len(s) == 0 {
		return
	}
	// A zero font size emits nothing visible and would produce a degenerate
	// text rendering matrix.
	if ip.gs.fontSize == 0 {
		return
	}

	vertical := f.Vertical()

	for _, cc := range f.Decode(s) {
		if len(ip.glyphs) >= maxGlyphsPerPage {
			ip.truncated = true
			return
		}

		// Text rendering matrix: the font-size parameters, then the text
		// matrix, then the CTM.
		params := Matrix{
			A: ip.gs.fontSize * ip.gs.hscale,
			D: ip.gs.fontSize,
			F: ip.gs.rise,
		}
		trm := params.Mul(ip.tm).Mul(ip.gs.ctm)
		x, y := trm.Translation()
		_, sy := trm.ScaleXY()

		// Horizontal displacement in unscaled text space.
		w0 := cc.Width / 1000
		tx := (w0*ip.gs.fontSize + ip.gs.charSp)
		if cc.SingleByteSpace {
			tx += ip.gs.wordSp
		}
		tx *= ip.gs.hscale

		// The advance in page space is the text-space displacement scaled by
		// the linear part of tm x ctm.
		lin := ip.tm.Mul(ip.gs.ctm)
		advance := tx * math.Hypot(lin.A, lin.B)

		r := '�'
		if len(cc.Runes) > 0 {
			r = cc.Runes[0]
		}

		ip.glyphs = append(ip.glyphs, Glyph{
			X:          x,
			Y:          y,
			Advance:    advance,
			Size:       sy,
			Rise:       ip.gs.rise,
			Rotation:   trm.Rotation(),
			Rune:       r,
			FontID:     ip.gs.fontID,
			RenderMode: ip.gs.render,
			Missing:    cc.Missing,
		})

		// A multi-rune expansion (a ligature mapped through /ToUnicode)
		// contributes its remaining runes at zero advance so no text is lost.
		for _, extra := range cc.Runes[1:] {
			if len(ip.glyphs) >= maxGlyphsPerPage {
				ip.truncated = true
				return
			}
			ip.glyphs = append(ip.glyphs, Glyph{
				X: x, Y: y, Advance: 0, Size: sy,
				Rise: ip.gs.rise, Rotation: trm.Rotation(),
				Rune: extra, FontID: ip.gs.fontID,
				RenderMode: ip.gs.render,
			})
		}

		if vertical {
			// Vertical writing advances down. Spec section 1 defers real
			// vertical layout; the advance still has to be applied so glyph
			// positions stay correct for the warning path.
			ip.tm = Translate(0, -(w0*ip.gs.fontSize + ip.gs.charSp)).Mul(ip.tm)
		} else {
			ip.tm = Translate(tx, 0).Mul(ip.tm)
		}
	}
}

// maxSubpathsPerPath bounds how many subpaths one path contributes, so a
// pathological content stream cannot exhaust memory before the paint operator
// arrives.
const maxSubpathsPerPath = 4096

// addPathPoint folds a construction coordinate into the current subpath and
// the whole path's extent, transformed into page space.
func (ip *interpreter) addPathPoint(x, y float64) {
	px, py := ip.gs.ctm.Apply(x, y)
	box := Rect{MinX: px, MinY: py, MaxX: px, MaxY: py}

	if ip.pathEmpty {
		ip.path, ip.pathEmpty = box, false
	} else {
		ip.path = ip.path.Union(box)
	}
	if ip.curEmpty {
		ip.cur, ip.curEmpty = box, false
	} else {
		ip.cur = ip.cur.Union(box)
	}
}

// endSubpath closes the subpath under construction.
func (ip *interpreter) endSubpath() {
	if !ip.curEmpty && len(ip.subpaths) < maxSubpathsPerPath {
		ip.subpaths = append(ip.subpaths, ip.cur)
	}
	ip.cur, ip.curEmpty = Rect{}, true
}

// maxRulesPerPage bounds rule recording.
const maxRulesPerPage = 16384

// paintPath records a painted path: its contribution to the dropped-artwork
// counters, and any subpath thin enough to be a ruling line.
func (ip *interpreter) paintPath(filled bool) {
	ip.endSubpath()

	if !ip.pathEmpty {
		ip.vectorPaints++
		ip.vectorBounds = ip.vectorBounds.Union(ip.path)
	}

	// The stroke width in page space. A stroked line's visible thickness is
	// the pen width scaled by the CTM, not the raw w operand.
	sx, sy := ip.gs.ctm.ScaleXY()
	penScale := (sx + sy) / 2
	pen := ip.gs.lineWidth * penScale
	if pen <= 0 {
		// Width 0 means the thinnest renderable line.
		pen = 0.1
	}

	for _, sp := range ip.subpaths {
		if len(ip.rules) >= maxRulesPerPage {
			break
		}
		if r, ok := ruleFromSubpath(sp, pen, filled); ok {
			ip.rules = append(ip.rules, r)
		}
	}
	ip.resetPath()
}

// ruleFromSubpath reduces a subpath to a rule when it is axis-aligned and
// thin, which is what a table's ruling lines look like.
//
// A stroked segment has zero extent across its own axis, so its thickness is
// the pen width; a filled rectangle carries its thickness in its geometry.
// Anything appreciably thick in both directions is a box or a shape, not a
// rule, and is left alone.
func ruleFromSubpath(sp Rect, pen float64, filled bool) (Rule, bool) {
	w, h := sp.Width(), sp.Height()

	var horizontal bool
	var thickness, length float64
	switch {
	case w >= h:
		horizontal, length = true, w
		thickness = h
	default:
		horizontal, length = false, h
		thickness = w
	}
	if !filled {
		// A stroke adds the pen width across the segment.
		thickness += pen
	}

	// Too short to bound a cell, or too thick to be a line.
	if length < minRuleLength || thickness > maxRuleThickness {
		return Rule{}, false
	}
	// A filled square is a glyph-sized mark, not a rule.
	if length <= thickness*2 {
		return Rule{}, false
	}
	return Rule{
		Bounds:     sp,
		Horizontal: horizontal,
		Thickness:  thickness,
		Filled:     filled,
	}, true
}

const (
	// minRuleLength is the shortest segment that can bound a table cell.
	// Shorter marks are dashes, tick marks, and glyph decoration.
	minRuleLength = 4.0
	// maxRuleThickness is the stroke width above which a painted segment is a
	// bar or a rectangle rather than a ruling line. Spec section 4.8 sets it
	// at 2 pt.
	maxRuleThickness = 2.0
)

func (ip *interpreter) resetPath() {
	ip.path, ip.pathEmpty = Rect{}, true
	ip.cur, ip.curEmpty = Rect{}, true
	ip.subpaths = ip.subpaths[:0]
}

// maxImagesPerPage bounds image recording so a hostile content stream cannot
// exhaust memory by repeating Do.
const maxImagesPerPage = 4096

// recordImage captures an image's placement from the current CTM.
//
// An image XObject occupies the unit square in its own space, so the CTM at
// draw time is the entire placement: transforming the unit square's corners
// and taking their bounding box gives the rectangle on the page.
func (ip *interpreter) recordImage(objNr int, name string, inline bool) {
	if len(ip.images) >= maxImagesPerPage {
		return
	}
	m := ip.gs.ctm

	var r Rect
	first := true
	for _, c := range [4][2]float64{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		x, y := m.Apply(c[0], c[1])
		box := Rect{MinX: x, MinY: y, MaxX: x, MaxY: y}
		if first {
			r, first = box, false
			continue
		}
		r = r.Union(box)
	}

	ip.images = append(ip.images, ImageDraw{
		ObjNr:        objNr,
		Name:         name,
		Rect:         r,
		Rotation:     m.Rotation(),
		GlyphsBefore: len(ip.glyphs),
		Order:        len(ip.images),
		Inline:       inline,
	})
}

// doXObject dispatches an XObject reference: an image records its placement,
// a form recurses.
func (ip *interpreter) doXObject(name string, res types.Dict, depth int) {
	if name == "" || res == nil || depth >= maxFormDepth {
		return
	}
	xobjs := dictOf(ip.xref, res, "XObject")
	if xobjs == nil {
		return
	}
	o, err := ip.xref.Dereference(xobjs[name])
	if err != nil || o == nil {
		return
	}
	sd, ok := o.(types.StreamDict)
	if !ok {
		return
	}

	if nameOf(sd.Dict, "Subtype") == "Image" {
		objNr := 0
		if ind, ok := xobjs[name].(types.IndirectRef); ok {
			objNr = ind.ObjectNumber.Value()
		}
		ip.recordImage(objNr, name, false)
		return
	}
	if nameOf(sd.Dict, "Subtype") != "Form" {
		return
	}
	if err := sd.Decode(); err != nil || len(sd.Content) == 0 {
		return
	}

	// A form runs in its own graphics state with its own matrix and
	// resources, inheriting the page's when it declares none.
	saved := ip.gs
	savedStack := len(ip.stack)
	savedTm, savedTlm := ip.tm, ip.tlm

	if arr, err := ip.xref.DereferenceArray(sd.Dict["Matrix"]); err == nil && len(arr) >= 6 {
		var m [6]float64
		valid := true
		for i := 0; i < 6; i++ {
			v, ok := numOf(ip.xref, arr[i])
			if !ok {
				valid = false
				break
			}
			m[i] = v
		}
		if valid {
			fm := Matrix{A: m[0], B: m[1], C: m[2], D: m[3], E: m[4], F: m[5]}
			ip.gs.ctm = fm.Mul(ip.gs.ctm)
		}
	}

	formRes := dictOf(ip.xref, sd.Dict, "Resources")
	if formRes == nil {
		formRes = res
	}

	ip.run(sd.Content, formRes, depth+1)

	// Restore, discarding any unbalanced q from inside the form.
	if len(ip.stack) > savedStack {
		ip.stack = ip.stack[:savedStack]
	}
	ip.gs = saved
	ip.tm, ip.tlm = savedTm, savedTlm
}
