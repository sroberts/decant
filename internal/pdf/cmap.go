package pdf

import (
	"unicode/utf16"
)

// codespace is one entry of a CMap begincodespacerange block. It fixes how
// many bytes a code occupies for byte sequences falling inside it.
type codespace struct {
	nbytes    int
	low, high uint32
}

// CMap maps byte codes to CIDs and/or Unicode. PDF uses the same syntax for
// two jobs: a /ToUnicode CMap produces text, and a Type0 /Encoding CMap
// produces CIDs for glyph selection. One type serves both.
type CMap struct {
	codespaces []codespace

	// single holds one-off code->value mappings from bfchar/cidchar.
	single map[uint32]cmapValue
	// ranges holds contiguous mappings from bfrange/cidrange.
	ranges []cmapRange

	// identity marks Identity-H/V, where CID equals code with 2-byte codes.
	identity bool
	// vertical marks a /V writing mode CMap.
	vertical bool
}

type cmapValue struct {
	cid  uint32
	text []rune // nil when the CMap carries only CIDs
}

type cmapRange struct {
	nbytes    int
	low, high uint32
	// dstCID is the CID assigned to low, incrementing across the range.
	dstCID uint32
	// dstText, when non-nil, is the text for low. The final rune increments
	// across the range, which is how bfrange encodes runs of characters.
	dstText []rune
	// texts, when non-nil, is an explicit per-code array from the
	// bfrange [ ... ] form.
	texts [][]rune
}

// IdentityCMap returns the Identity-H mapping: two-byte codes, CID == code.
func IdentityCMap() *CMap {
	return &CMap{
		identity:   true,
		codespaces: []codespace{{nbytes: 2, low: 0, high: 0xFFFF}},
		single:     map[uint32]cmapValue{},
	}
}

// Vertical reports whether the CMap selects vertical writing mode.
func (c *CMap) Vertical() bool { return c != nil && c.vertical }

// ParseCMap reads CMap syntax. It tolerates truncation and unknown operators,
// returning whatever mappings it recovered.
func ParseCMap(data []byte) *CMap {
	c := &CMap{single: map[uint32]cmapValue{}}
	l := newLexer(data)

	// operands holds the tokens seen since the last operator. CMap operators
	// are postfix like content stream operators.
	var operands []object

	for {
		o, ok := l.next()
		if !ok {
			break
		}
		if o.kind != kOp {
			if len(operands) < 512 {
				operands = append(operands, o)
			}
			continue
		}

		switch string(o.str) {
		case "begincodespacerange":
			c.parseCodespaces(l)
		case "beginbfchar":
			c.parseBFChar(l)
		case "beginbfrange":
			c.parseBFRange(l)
		case "begincidchar":
			c.parseCIDChar(l)
		case "begincidrange":
			c.parseCIDRange(l)
		case "usecmap":
			// A predefined base CMap. Identity-H/V are the only ones we
			// resolve; others fall back to the codespaces this CMap declares.
			if len(operands) > 0 {
				if n := operands[len(operands)-1].name(); n == "Identity-H" || n == "Identity-V" {
					c.identity = true
					c.vertical = n == "Identity-V"
					if len(c.codespaces) == 0 {
						c.codespaces = []codespace{{nbytes: 2, low: 0, high: 0xFFFF}}
					}
				}
			}
		case "def":
			// /WMode 1 def
			if len(operands) >= 2 && operands[len(operands)-2].name() == "WMode" {
				c.vertical = operands[len(operands)-1].float() == 1
			}
		}
		operands = operands[:0]
	}

	if len(c.codespaces) == 0 {
		// No declared codespace. Infer from the mappings we found; a CMap with
		// only 2-byte entries is almost always Identity-H shaped.
		c.codespaces = c.inferCodespaces()
	}
	return c
}

// inferCodespaces guesses byte widths when begincodespacerange is missing.
func (c *CMap) inferCodespaces() []codespace {
	wide := false
	for code := range c.single {
		if code > 0xFF {
			wide = true
			break
		}
	}
	if !wide {
		for _, r := range c.ranges {
			if r.high > 0xFF || r.nbytes == 2 {
				wide = true
				break
			}
		}
	}
	if wide {
		return []codespace{{nbytes: 2, low: 0, high: 0xFFFF}}
	}
	return []codespace{{nbytes: 1, low: 0, high: 0xFF}}
}

func bytesToCode(b []byte) uint32 {
	var v uint32
	// Codes wider than four bytes do not occur; clamp rather than overflow.
	if len(b) > 4 {
		b = b[:4]
	}
	for _, x := range b {
		v = v<<8 | uint32(x)
	}
	return v
}

// utf16BEToRunes decodes the big-endian UTF-16 that bfchar/bfrange use for
// destination text.
func utf16BEToRunes(b []byte) []rune {
	if len(b) == 0 {
		return nil
	}
	if len(b) == 1 {
		// Malformed but common in subsetted fonts; treat as Latin-1.
		return []rune{rune(b[0])}
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return utf16.Decode(u)
}

func (c *CMap) parseCodespaces(l *lexer) {
	for {
		lo, ok := l.next()
		if !ok || lo.isOp("endcodespacerange") {
			return
		}
		if lo.kind != kString {
			continue
		}
		hi, ok := l.next()
		if !ok || hi.isOp("endcodespacerange") {
			return
		}
		if hi.kind != kString {
			continue
		}
		n := len(lo.str)
		if n == 0 || n > 4 {
			continue
		}
		c.codespaces = append(c.codespaces, codespace{
			nbytes: n,
			low:    bytesToCode(lo.str),
			high:   bytesToCode(hi.str),
		})
	}
}

func (c *CMap) parseBFChar(l *lexer) {
	for {
		src, ok := l.next()
		if !ok || src.isOp("endbfchar") {
			return
		}
		if src.kind != kString {
			continue
		}
		dst, ok := l.next()
		if !ok || dst.isOp("endbfchar") {
			return
		}
		code := bytesToCode(src.str)
		switch dst.kind {
		case kString:
			c.single[code] = cmapValue{text: utf16BEToRunes(dst.str)}
		case kName:
			if r, ok := GlyphNameToRune(dst.name()); ok {
				c.single[code] = cmapValue{text: []rune{r}}
			}
		}
	}
}

func (c *CMap) parseBFRange(l *lexer) {
	for {
		lo, ok := l.next()
		if !ok || lo.isOp("endbfrange") {
			return
		}
		if lo.kind != kString {
			continue
		}
		hi, ok := l.next()
		if !ok || hi.isOp("endbfrange") {
			return
		}
		dst, ok := l.next()
		if !ok || dst.isOp("endbfrange") {
			return
		}

		r := cmapRange{
			nbytes: len(lo.str),
			low:    bytesToCode(lo.str),
			high:   bytesToCode(hi.str),
		}
		if r.high < r.low {
			r.high = r.low
		}
		switch dst.kind {
		case kString:
			r.dstText = utf16BEToRunes(dst.str)
		case kArray:
			texts := make([][]rune, 0, len(dst.arr))
			for _, e := range dst.arr {
				switch e.kind {
				case kString:
					texts = append(texts, utf16BEToRunes(e.str))
				case kName:
					if rr, ok := GlyphNameToRune(e.name()); ok {
						texts = append(texts, []rune{rr})
					} else {
						texts = append(texts, nil)
					}
				default:
					texts = append(texts, nil)
				}
			}
			r.texts = texts
		default:
			continue
		}
		c.ranges = append(c.ranges, r)
	}
}

func (c *CMap) parseCIDChar(l *lexer) {
	for {
		src, ok := l.next()
		if !ok || src.isOp("endcidchar") {
			return
		}
		if src.kind != kString {
			continue
		}
		dst, ok := l.next()
		if !ok || dst.isOp("endcidchar") {
			return
		}
		if dst.kind != kNum {
			continue
		}
		code := bytesToCode(src.str)
		v := c.single[code]
		v.cid = uint32(dst.num)
		c.single[code] = v
	}
}

func (c *CMap) parseCIDRange(l *lexer) {
	for {
		lo, ok := l.next()
		if !ok || lo.isOp("endcidrange") {
			return
		}
		if lo.kind != kString {
			continue
		}
		hi, ok := l.next()
		if !ok || hi.isOp("endcidrange") {
			return
		}
		dst, ok := l.next()
		if !ok || dst.isOp("endcidrange") {
			return
		}
		if hi.kind != kString || dst.kind != kNum {
			continue
		}
		r := cmapRange{
			nbytes: len(lo.str),
			low:    bytesToCode(lo.str),
			high:   bytesToCode(hi.str),
			dstCID: uint32(dst.num),
		}
		if r.high < r.low {
			r.high = r.low
		}
		c.ranges = append(c.ranges, r)
	}
}

// NextCode consumes one code from b, returning the code, how many bytes it
// used, and whether the read succeeded. A code matching no codespace consumes
// one byte so the caller always makes progress.
func (c *CMap) NextCode(b []byte) (code uint32, n int, ok bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	if c == nil || len(c.codespaces) == 0 {
		return uint32(b[0]), 1, true
	}

	// Try each declared width shortest-first, which is how a conforming
	// reader disambiguates mixed-width CMaps.
	for width := 1; width <= 4; width++ {
		if width > len(b) {
			break
		}
		v := bytesToCode(b[:width])
		for _, cs := range c.codespaces {
			if cs.nbytes == width && v >= cs.low && v <= cs.high {
				return v, width, true
			}
		}
	}

	// No codespace matched. Fall back to the most common declared width so a
	// slightly-out-of-range code still decodes at the right stride.
	w := c.codespaces[0].nbytes
	if w > len(b) {
		w = len(b)
	}
	if w < 1 {
		w = 1
	}
	return bytesToCode(b[:w]), w, true
}

// CID maps a code to a CID.
func (c *CMap) CID(code uint32) uint32 {
	if c == nil {
		return code
	}
	if v, ok := c.single[code]; ok && v.cid != 0 {
		return v.cid
	}
	for _, r := range c.ranges {
		if code >= r.low && code <= r.high && r.dstText == nil && r.texts == nil {
			return r.dstCID + (code - r.low)
		}
	}
	if c.identity {
		return code
	}
	return code
}

// Text maps a code to its Unicode expansion. ok is false when the CMap has no
// entry, which the caller treats as a decode failure.
func (c *CMap) Text(code uint32) (text []rune, ok bool) {
	if c == nil {
		return nil, false
	}
	if v, found := c.single[code]; found && v.text != nil {
		return v.text, true
	}
	for _, r := range c.ranges {
		if code < r.low || code > r.high {
			continue
		}
		idx := code - r.low
		if r.texts != nil {
			if int(idx) < len(r.texts) && r.texts[idx] != nil {
				return r.texts[idx], true
			}
			continue
		}
		if r.dstText == nil {
			continue
		}
		// The destination increments in its last rune across the range.
		out := make([]rune, len(r.dstText))
		copy(out, r.dstText)
		out[len(out)-1] += rune(idx)
		return out, true
	}
	return nil, false
}

// Empty reports whether the CMap carries no mappings at all.
func (c *CMap) Empty() bool {
	return c == nil || (len(c.single) == 0 && len(c.ranges) == 0 && !c.identity)
}
