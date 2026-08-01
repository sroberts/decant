package pdf

import (
	"strings"
	"sync"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// FontDescriptor flag bits from PDF 32000-1 table 123.
const (
	flagFixedPitch = 1 << 0
	flagSerif      = 1 << 1
	flagSymbolic   = 1 << 2
	flagItalic     = 1 << 6
	flagForceBold  = 1 << 18
)

// FontRef identifies a font within a document. The object number makes fonts
// shared across pages compare equal, which the body-font computation in stage
// 6 depends on.
type FontRef struct {
	ObjNr int
	// Name is the resource-dictionary key, used only when the font dict is
	// direct and therefore has no object number.
	Name string
}

// Font holds everything the interpreter and the classifier need about one
// font: how to turn bytes into runes, how wide each glyph is, and what the
// face looks like.
type Font struct {
	Ref      FontRef
	BaseFont string
	Family   string // subset prefix and style suffix stripped
	Subtype  string

	Bold       bool
	Italic     bool
	FixedPitch bool
	Serif      bool
	Symbolic   bool

	// composite marks a Type0 font, whose codes are multi-byte and index
	// CIDs rather than the 256-entry encoding table.
	composite bool

	encCMap   *CMap // Type0 /Encoding
	toUnicode *CMap

	encoding  [256]rune       // simple font code->rune
	diffNames map[byte]string // /Differences glyph names, for sfnt fallback

	widths   map[uint32]float64 // by code (simple) or CID (composite)
	defWidth float64

	// base14 metrics, used when the font neither embeds a program nor
	// supplies /Widths.
	std    base14Metrics
	hasStd bool

	// sf is the embedded font program, when parseable. Only TrueType and
	// OpenType parse; bare CFF and Type1 do not, and fall back to metrics.
	sf      *sfnt.Font
	revOnce sync.Once
	revCmap map[sfnt.GlyphIndex]rune
	sfMu    sync.Mutex // guards the shared sfnt.Buffer
	sfBuf   sfnt.Buffer

	// cidToGID maps CIDs to glyph indices for composite fonts with an
	// explicit /CIDToGIDMap stream. Nil means the identity mapping.
	cidToGID []uint16
}

// CharCode is one decoded code from a show-text operand.
type CharCode struct {
	Code  uint32
	CID   uint32
	Runes []rune
	// Width is the horizontal displacement in 1/1000 text space units,
	// before font size and the text state parameters apply.
	Width  float64
	NBytes int
	// SingleByteSpace marks code 32 in a single-byte encoding, the only case
	// where the Tw word spacing parameter applies.
	SingleByteSpace bool
	// Missing marks a code that produced no usable rune. The caller emits
	// U+FFFD and counts a decode failure.
	Missing bool
}

// LoadFont builds a Font from a font dictionary.
func LoadFont(xref *model.XRefTable, ref FontRef, d types.Dict) *Font {
	f := &Font{Ref: ref, defWidth: 500, widths: map[uint32]float64{}}

	f.Subtype = nameOf(d, "Subtype")
	f.BaseFont = nameOf(d, "BaseFont")
	f.Family = normalizeFamily(f.BaseFont)
	f.composite = f.Subtype == "Type0"

	if m, ok := base14(f.BaseFont); ok {
		f.std, f.hasStd = m, true
		f.Bold, f.Italic = m.bold, m.italic
		f.FixedPitch, f.Serif = m.fixedPitch, m.serif
		if m.defWidth > 0 {
			f.defWidth = m.defWidth
		}
	}

	if f.composite {
		f.loadType0(xref, d)
	} else {
		f.loadSimple(xref, d)
	}

	if tu := f.loadCMapStream(xref, d, "ToUnicode"); tu != nil {
		f.toUnicode = tu
	}
	return f
}

// loadSimple handles Type1, TrueType, Type3, and MMType1 fonts, whose codes
// are always one byte.
func (f *Font) loadSimple(xref *model.XRefTable, d types.Dict) {
	desc := dictOf(xref, d, "FontDescriptor")
	f.applyDescriptor(xref, desc)

	// Base encoding. A symbolic font with no /Encoding uses its built-in
	// encoding, which for an embedded TrueType means the font's own cmap; we
	// leave the table empty there and let the sfnt fallback resolve codes.
	base := &StandardEncoding
	useBase := !f.Symbolic

	// TeX text fonts are the exception: they are symbolic Type1 programs
	// carrying no /Encoding and no /ToUnicode, and x/image/font/sfnt cannot
	// parse Type1 to recover their built-in encoding. Nothing in the PDF says
	// how to read them, so without OT1 every ligature and typographic quote
	// in a LaTeX document becomes U+FFFD.
	if isTeXTextFont(f.BaseFont) {
		base, useBase = &OT1Encoding, true
	}

	encObj, _ := xref.Dereference(d["Encoding"])
	switch e := encObj.(type) {
	case types.Name:
		if t, ok := encodingByName(e.Value()); ok {
			base, useBase = t, true
		}
	case types.Dict:
		if bn := nameOf(e, "BaseEncoding"); bn != "" {
			if t, ok := encodingByName(bn); ok {
				base, useBase = t, true
			}
		} else if !f.Symbolic {
			useBase = true
		}
		if useBase {
			f.encoding = *base
		}
		f.applyDifferences(xref, e)
		f.loadSimpleWidths(xref, d, desc)
		return
	}

	if useBase {
		f.encoding = *base
	}
	f.loadSimpleWidths(xref, d, desc)
}

// applyDifferences overlays an /Encoding /Differences array.
func (f *Font) applyDifferences(xref *model.XRefTable, enc types.Dict) {
	arr, err := xref.DereferenceArray(enc["Differences"])
	if err != nil || arr == nil {
		return
	}
	if f.diffNames == nil {
		f.diffNames = map[byte]string{}
	}
	code := 0
	for _, o := range arr {
		o, _ = xref.Dereference(o)
		switch v := o.(type) {
		case types.Integer:
			code = v.Value()
		case types.Float:
			code = int(v.Value())
		case types.Name:
			if code >= 0 && code < 256 {
				gn := v.Value()
				f.diffNames[byte(code)] = gn
				if r, ok := GlyphNameToRune(gn); ok {
					f.encoding[code] = r
				} else {
					// Keep the name for the sfnt reverse path; clear any
					// inherited base-encoding rune so we do not report a
					// wrong character with confidence.
					f.encoding[code] = 0
				}
			}
			code++
		}
	}
}

func (f *Font) loadSimpleWidths(xref *model.XRefTable, d, desc types.Dict) {
	first := intOf(xref, d, "FirstChar", 0)
	arr, err := xref.DereferenceArray(d["Widths"])
	if err == nil && len(arr) > 0 {
		for i, o := range arr {
			w, ok := numOf(xref, o)
			if !ok {
				continue
			}
			code := first + i
			if code < 0 || code > 255 {
				continue
			}
			f.widths[uint32(code)] = w
		}
	}
	if desc != nil {
		if mw, ok := numOf(xref, desc["MissingWidth"]); ok && mw > 0 {
			f.defWidth = mw
		}
	}
}

// loadType0 handles composite fonts: a Type0 wrapper with an /Encoding CMap
// and a single CIDFont descendant carrying the metrics.
func (f *Font) loadType0(xref *model.XRefTable, d types.Dict) {
	// /Encoding is either a predefined CMap name or a stream.
	encObj, _ := xref.Dereference(d["Encoding"])
	switch e := encObj.(type) {
	case types.Name:
		n := e.Value()
		if n == "Identity-H" || n == "Identity-V" {
			f.encCMap = IdentityCMap()
			f.encCMap.vertical = n == "Identity-V"
		} else if cm := f.loadCMapStream(xref, d, "Encoding"); cm != nil {
			f.encCMap = cm
		} else {
			// A predefined CJK CMap we do not ship. Two-byte codes are the
			// overwhelmingly common shape; assume that so the stride is right
			// even though the CIDs will be wrong.
			f.encCMap = IdentityCMap()
		}
	default:
		if cm := f.loadCMapStream(xref, d, "Encoding"); cm != nil {
			f.encCMap = cm
		} else {
			f.encCMap = IdentityCMap()
		}
	}

	// Descendant CIDFont.
	descArr, err := xref.DereferenceArray(d["DescendantFonts"])
	if err != nil || len(descArr) == 0 {
		f.defWidth = 1000
		return
	}
	cidFont, err := xref.DereferenceDict(descArr[0])
	if err != nil || cidFont == nil {
		f.defWidth = 1000
		return
	}

	f.applyDescriptor(xref, dictOf(xref, cidFont, "FontDescriptor"))

	f.defWidth = 1000
	if dw, ok := numOf(xref, cidFont["DW"]); ok && dw > 0 {
		f.defWidth = dw
	}
	f.loadCIDWidths(xref, cidFont)
	f.loadCIDToGID(xref, cidFont)
}

// loadCIDWidths parses the /W array, whose entries take two forms:
//
//	c [w1 w2 ...]   widths for c, c+1, ...
//	cFirst cLast w  one width across the whole range
func (f *Font) loadCIDWidths(xref *model.XRefTable, cidFont types.Dict) {
	arr, err := xref.DereferenceArray(cidFont["W"])
	if err != nil || arr == nil {
		return
	}
	// A pathological /W can declare an enormous range; cap total entries.
	const maxWidthEntries = 1 << 20

	for i := 0; i < len(arr); {
		start, ok := numOf(xref, arr[i])
		if !ok {
			i++
			continue
		}
		if i+1 >= len(arr) {
			break
		}
		next, _ := xref.Dereference(arr[i+1])
		if sub, isArr := next.(types.Array); isArr {
			for j, o := range sub {
				w, ok := numOf(xref, o)
				if !ok {
					continue
				}
				if len(f.widths) >= maxWidthEntries {
					return
				}
				f.widths[uint32(int(start)+j)] = w
			}
			i += 2
			continue
		}
		if i+2 >= len(arr) {
			break
		}
		end, ok1 := numOf(xref, arr[i+1])
		w, ok2 := numOf(xref, arr[i+2])
		if ok1 && ok2 && end >= start {
			if int(end-start) > maxWidthEntries {
				end = start + maxWidthEntries
			}
			for c := int(start); c <= int(end); c++ {
				if len(f.widths) >= maxWidthEntries {
					return
				}
				f.widths[uint32(c)] = w
			}
		}
		i += 3
	}
}

func (f *Font) loadCIDToGID(xref *model.XRefTable, cidFont types.Dict) {
	o, _ := xref.Dereference(cidFont["CIDToGIDMap"])
	sd, ok := o.(types.StreamDict)
	if !ok {
		return
	}
	if err := sd.Decode(); err != nil {
		return
	}
	b := sd.Content
	f.cidToGID = make([]uint16, len(b)/2)
	for i := range f.cidToGID {
		f.cidToGID[i] = uint16(b[i*2])<<8 | uint16(b[i*2+1])
	}
}

// applyDescriptor reads style flags and the embedded font program.
func (f *Font) applyDescriptor(xref *model.XRefTable, desc types.Dict) {
	if desc == nil {
		// Fall back to the name, which is all a non-embedded font offers.
		lower := strings.ToLower(f.BaseFont)
		f.Bold = f.Bold || strings.Contains(lower, "bold")
		f.Italic = f.Italic || strings.Contains(lower, "italic") ||
			strings.Contains(lower, "oblique")
		return
	}

	flags := 0
	if v, ok := numOf(xref, desc["Flags"]); ok {
		flags = int(v)
	}
	f.FixedPitch = f.FixedPitch || flags&flagFixedPitch != 0
	f.Serif = f.Serif || flags&flagSerif != 0
	f.Symbolic = flags&flagSymbolic != 0
	f.Italic = f.Italic || flags&flagItalic != 0

	// Weight: ForceBold, then /StemV, then the name. StemV above 120 is the
	// conventional bold threshold.
	if flags&flagForceBold != 0 {
		f.Bold = true
	}
	if sv, ok := numOf(xref, desc["StemV"]); ok && sv >= 120 {
		f.Bold = true
	}
	if fw, ok := numOf(xref, desc["FontWeight"]); ok && fw >= 600 {
		f.Bold = true
	}
	lower := strings.ToLower(f.BaseFont)
	if strings.Contains(lower, "bold") || strings.Contains(lower, "black") ||
		strings.Contains(lower, "heavy") {
		f.Bold = true
	}
	if strings.Contains(lower, "italic") || strings.Contains(lower, "oblique") {
		f.Italic = true
	}
	if ia, ok := numOf(xref, desc["ItalicAngle"]); ok && ia != 0 {
		f.Italic = true
	}

	f.loadFontProgram(xref, desc)
}

// loadFontProgram parses an embedded TrueType or OpenType program. Bare CFF
// and Type1 programs are not parseable by x/image/font/sfnt; those fonts rely
// on /Widths and the encoding tables instead.
func (f *Font) loadFontProgram(xref *model.XRefTable, desc types.Dict) {
	for _, key := range []string{"FontFile2", "FontFile3", "FontFile"} {
		o, err := xref.Dereference(desc[key])
		if err != nil || o == nil {
			continue
		}
		sd, ok := o.(types.StreamDict)
		if !ok {
			continue
		}
		if key == "FontFile3" {
			// Only the OpenType subtype wraps an SFNT container.
			if st := nameOf(sd.Dict, "Subtype"); st != "OpenType" {
				continue
			}
		}
		if key == "FontFile" {
			continue // Type1, not SFNT
		}
		if err := sd.Decode(); err != nil || len(sd.Content) == 0 {
			continue
		}
		sf, err := sfnt.Parse(sd.Content)
		if err != nil {
			continue
		}
		f.sf = sf
		return
	}
}

// loadCMapStream reads and parses a CMap held in a stream at d[key].
func (f *Font) loadCMapStream(xref *model.XRefTable, d types.Dict, key string) *CMap {
	o, err := xref.Dereference(d[key])
	if err != nil || o == nil {
		return nil
	}
	sd, ok := o.(types.StreamDict)
	if !ok {
		return nil
	}
	if err := sd.Decode(); err != nil || len(sd.Content) == 0 {
		return nil
	}
	cm := ParseCMap(sd.Content)
	if cm.Empty() {
		return nil
	}
	return cm
}

// Decode turns a show-text operand into character codes with widths and text.
func (f *Font) Decode(b []byte) []CharCode {
	if len(b) == 0 {
		return nil
	}
	out := make([]CharCode, 0, len(b))

	for i := 0; i < len(b); {
		var code uint32
		var n int
		if f.composite {
			var ok bool
			code, n, ok = f.encCMap.NextCode(b[i:])
			if !ok || n <= 0 {
				break
			}
		} else {
			code, n = uint32(b[i]), 1
		}
		i += n

		cc := CharCode{Code: code, NBytes: n}
		cc.SingleByteSpace = n == 1 && code == 32

		if f.composite {
			cc.CID = f.encCMap.CID(code)
		} else {
			cc.CID = code
		}

		cc.Runes, cc.Missing = f.runesFor(code, cc.CID)
		cc.Width = f.widthFor(code, cc.CID)
		out = append(out, cc)
	}
	return out
}

// runesFor resolves a code to text following the precedence in spec 4.2:
// /ToUnicode, then /Encoding with /Differences, then the standard encoding
// tables, then Adobe Glyph List name lookup, then the embedded font's cmap.
func (f *Font) runesFor(code, cid uint32) ([]rune, bool) {
	if rs, ok := f.toUnicode.Text(code); ok && len(rs) > 0 && !allZero(rs) {
		return rs, false
	}

	if !f.composite && code < 256 {
		// /Differences names, then the resolved encoding table. Both were
		// folded into f.encoding at load time; diffNames survives for the
		// sfnt path below.
		if r := f.encoding[code]; r != 0 {
			return []rune{r}, false
		}
		if gn, ok := f.diffNames[byte(code)]; ok {
			if r, ok := GlyphNameToRune(gn); ok {
				return []rune{r}, false
			}
		}
	}

	// Embedded font program: map the glyph back to a rune, first via its
	// PostScript name, then via a reverse cmap scan.
	if f.sf != nil {
		gid := f.glyphIndex(cid)
		if r, ok := f.runeFromGlyph(gid); ok {
			return []rune{r}, false
		}
	}

	// A symbolic simple font with no usable encoding still often carries
	// plain ASCII in its code points.
	if !f.composite && code >= 32 && code < 127 {
		return []rune{rune(code)}, false
	}

	return []rune{'�'}, true
}

func allZero(rs []rune) bool {
	for _, r := range rs {
		if r != 0 {
			return false
		}
	}
	return true
}

// glyphIndex maps a CID to a glyph index in the embedded program.
func (f *Font) glyphIndex(cid uint32) sfnt.GlyphIndex {
	if f.cidToGID != nil {
		if int(cid) < len(f.cidToGID) {
			return sfnt.GlyphIndex(f.cidToGID[cid])
		}
		return 0
	}
	return sfnt.GlyphIndex(cid)
}

// runeFromGlyph reverses a glyph index to a rune.
func (f *Font) runeFromGlyph(gid sfnt.GlyphIndex) (rune, bool) {
	if f.sf == nil || int(gid) >= f.sf.NumGlyphs() {
		return 0, false
	}

	f.sfMu.Lock()
	name, err := f.sf.GlyphName(&f.sfBuf, gid)
	f.sfMu.Unlock()
	if err == nil && name != "" {
		if r, ok := GlyphNameToRune(name); ok {
			return r, true
		}
	}

	f.buildReverseCmap()
	r, ok := f.revCmap[gid]
	return r, ok
}

// buildReverseCmap inverts the font's cmap once, lazily. Fonts reach this
// path only when glyph names are absent, so the scan cost is paid rarely.
func (f *Font) buildReverseCmap() {
	f.revOnce.Do(func() {
		m := make(map[sfnt.GlyphIndex]rune, f.sf.NumGlyphs())
		var buf sfnt.Buffer
		for r := rune(0x20); r <= 0xFFFF; r++ {
			// Surrogates are never mapped.
			if r >= 0xD800 && r <= 0xDFFF {
				continue
			}
			gid, err := f.sf.GlyphIndex(&buf, r)
			if err != nil || gid == 0 {
				continue
			}
			// Keep the lowest rune for a glyph, which favors the base
			// character over presentation forms.
			if _, exists := m[gid]; !exists {
				m[gid] = r
			}
		}
		f.revCmap = m
	})
}

// widthFor returns the glyph advance in 1/1000 text space units.
func (f *Font) widthFor(code, cid uint32) float64 {
	key := code
	if f.composite {
		key = cid
	}
	if w, ok := f.widths[key]; ok {
		return w
	}
	if f.hasStd && f.std.widths != nil && !f.composite && code < 256 {
		if r := f.encoding[code]; r != 0 {
			if w, ok := f.std.widths.lookup(r); ok {
				return w
			}
		}
	}
	if f.sf != nil {
		if w, ok := f.advanceFromProgram(cid); ok {
			return w
		}
	}
	return f.defWidth
}

// advanceFromProgram reads the advance from the embedded font, normalized to
// 1/1000 em.
//
// Passing ppem = Int26_6(unitsPerEm) makes sfnt return font units directly in
// the raw fixed-point value, per the package documentation, so no /64 applies.
func (f *Font) advanceFromProgram(cid uint32) (float64, bool) {
	gid := f.glyphIndex(cid)
	if int(gid) >= f.sf.NumGlyphs() {
		return 0, false
	}
	upem := f.sf.UnitsPerEm()
	if upem == 0 {
		return 0, false
	}
	f.sfMu.Lock()
	adv, err := f.sf.GlyphAdvance(&f.sfBuf, gid, fixed.Int26_6(upem), font.HintingNone)
	f.sfMu.Unlock()
	if err != nil {
		return 0, false
	}
	return float64(adv) * 1000 / float64(upem), true
}

// SpaceWidth returns the width of the space glyph in 1/1000 units. ok is
// false when the font has no space glyph, in which case line assembly falls
// back to the median advance.
func (f *Font) SpaceWidth() (float64, bool) {
	if !f.composite {
		if w, ok := f.widths[32]; ok && w > 0 {
			return w, true
		}
		if f.hasStd && f.std.widths != nil {
			if w, ok := f.std.widths.lookup(' '); ok {
				return w, true
			}
		}
		if f.encoding[32] == ' ' && f.defWidth > 0 {
			return f.defWidth, true
		}
		return 0, false
	}
	// Composite fonts rarely encode a space at a predictable CID. Identity
	// encodings often do map code 32 through to a space glyph.
	if w, ok := f.widths[32]; ok && w > 0 {
		return w, true
	}
	return 0, false
}

// Vertical reports whether the font selects vertical writing mode.
func (f *Font) Vertical() bool { return f.composite && f.encCMap.Vertical() }

// normalizeFamily strips the subset prefix and trailing style qualifiers so
// that "ABCDEF+Minion-BoldItalic" and "Minion-Regular" share a family.
func normalizeFamily(base string) string {
	n := stripSubsetPrefix(base)
	if i := strings.IndexAny(n, ",-"); i > 0 {
		n = n[:i]
	}
	for _, suffix := range []string{"MT", "PS", "Std", "Pro"} {
		n = strings.TrimSuffix(n, suffix)
	}
	if n == "" {
		return base
	}
	return n
}

// --- pdfcpu dictionary helpers ---

func nameOf(d types.Dict, key string) string {
	if d == nil {
		return ""
	}
	if n, ok := d[key].(types.Name); ok {
		return n.Value()
	}
	return ""
}

func dictOf(xref *model.XRefTable, d types.Dict, key string) types.Dict {
	if d == nil {
		return nil
	}
	sub, err := xref.DereferenceDict(d[key])
	if err != nil {
		return nil
	}
	return sub
}

func intOf(xref *model.XRefTable, d types.Dict, key string, def int) int {
	if d == nil {
		return def
	}
	if v, ok := numOf(xref, d[key]); ok {
		return int(v)
	}
	return def
}

// numOf dereferences and coerces a numeric object.
func numOf(xref *model.XRefTable, o types.Object) (float64, bool) {
	if o == nil {
		return 0, false
	}
	o, err := xref.Dereference(o)
	if err != nil || o == nil {
		return 0, false
	}
	switch v := o.(type) {
	case types.Integer:
		return float64(v.Value()), true
	case types.Float:
		return v.Value(), true
	}
	return 0, false
}
