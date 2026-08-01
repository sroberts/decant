package pdf

import "testing"

func TestStandardEncodingQuotes(t *testing.T) {
	// StandardEncoding differs from WinAnsi exactly where it matters most for
	// text quality: the quote characters.
	if got := StandardEncoding[39]; got != '’' {
		t.Errorf("StandardEncoding[39] = %q, want a right single quote", got)
	}
	if got := StandardEncoding[96]; got != '‘' {
		t.Errorf("StandardEncoding[96] = %q, want a left single quote", got)
	}
	if got := WinAnsiEncoding[39]; got != '\'' {
		t.Errorf("WinAnsiEncoding[39] = %q, want an apostrophe", got)
	}
	if got := WinAnsiEncoding[96]; got != '`' {
		t.Errorf("WinAnsiEncoding[96] = %q, want a backtick", got)
	}
}

func TestWinAnsiHighRange(t *testing.T) {
	cases := map[byte]rune{
		128: '€', 133: '…', 145: '‘', 146: '’',
		147: '“', 148: '”', 150: '–', 151: '—',
		169: '©', 174: '®', 233: 'é', 252: 'ü', 255: 'ÿ',
	}
	for code, want := range cases {
		if got := WinAnsiEncoding[code]; got != want {
			t.Errorf("WinAnsiEncoding[%d] = %q, want %q", code, got, want)
		}
	}
}

func TestMacRomanHighRange(t *testing.T) {
	cases := map[byte]rune{
		128: 'Ä', 165: '•', 208: '–', 209: '—',
		210: '“', 213: '’', 222: 'ﬁ', 223: 'ﬂ',
		// PDF's MacRomanEncoding puts currency at 219 where the platform
		// encoding has a euro sign.
		219: '¤',
	}
	for code, want := range cases {
		if got := MacRomanEncoding[code]; got != want {
			t.Errorf("MacRomanEncoding[%d] = %q, want %q", code, got, want)
		}
	}
}

func TestASCIIRangeIsConsistent(t *testing.T) {
	// All three encodings agree across the alphanumeric range; a divergence
	// there would be a transcription error in the tables.
	for c := byte('0'); c <= '9'; c++ {
		checkAll(t, c, rune(c))
	}
	for c := byte('A'); c <= 'Z'; c++ {
		checkAll(t, c, rune(c))
	}
	for c := byte('a'); c <= 'z'; c++ {
		checkAll(t, c, rune(c))
	}
	checkAll(t, ' ', ' ')
}

func checkAll(t *testing.T, code byte, want rune) {
	t.Helper()
	for name, tab := range map[string]*[256]rune{
		"Standard": &StandardEncoding,
		"WinAnsi":  &WinAnsiEncoding,
		"MacRoman": &MacRomanEncoding,
		"PDFDoc":   &PDFDocEncoding,
	} {
		if got := tab[code]; got != want {
			t.Errorf("%sEncoding[%d] = %q, want %q", name, code, got, want)
		}
	}
}

func TestGlyphNameToRune(t *testing.T) {
	cases := []struct {
		name string
		want rune
		ok   bool
	}{
		{"A", 'A', true},
		{"space", ' ', true},
		{"eacute", 'é', true},
		{"quotedblleft", '“', true},
		{"fi", 'ﬁ', true},
		{"Euro", '€', true},
		// Algorithmic forms.
		{"uni0041", 'A', true},
		{"uni20AC", '€', true},
		{"u1F600", 0x1F600, true},
		// Suffixed and ligature-joined variants fall back to the base name.
		{"a.sc", 'a', true},
		{"one.oldstyle", '1', true},
		{"f_i", 'f', true},
		// Names carrying no semantic value.
		{"", 0, false},
		{"g42", 0, false},
		{"cid123", 0, false},
	}
	for _, c := range cases {
		got, ok := GlyphNameToRune(c.name)
		if ok != c.ok {
			t.Errorf("GlyphNameToRune(%q) ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("GlyphNameToRune(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestStripSubsetPrefix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ABCDEF+Times-Bold", "Times-Bold"},
		{"Times-Bold", "Times-Bold"},
		// Only six uppercase letters followed by a plus qualifies.
		{"abcdef+Times", "abcdef+Times"},
		{"ABC+Times", "ABC+Times"},
		{"", ""},
	}
	for _, c := range cases {
		if got := stripSubsetPrefix(c.in); got != c.want {
			t.Errorf("stripSubsetPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBase14Resolution(t *testing.T) {
	cases := []struct {
		name       string
		wantOK     bool
		bold       bool
		italic     bool
		fixedPitch bool
		serif      bool
	}{
		{name: "Helvetica", wantOK: true},
		{name: "Helvetica-Bold", wantOK: true, bold: true},
		{name: "Helvetica-Oblique", wantOK: true, italic: true},
		{name: "Times-Roman", wantOK: true, serif: true},
		{name: "Times-BoldItalic", wantOK: true, bold: true, italic: true, serif: true},
		{name: "Courier", wantOK: true, fixedPitch: true},
		{name: "ABCDEF+Times-Bold", wantOK: true, bold: true, serif: true},
		{name: "Arial", wantOK: true},
		{name: "ZapfDingbats", wantOK: false},
	}
	for _, c := range cases {
		m, ok := base14(c.name)
		if ok != c.wantOK {
			t.Errorf("base14(%q) ok = %v, want %v", c.name, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if m.bold != c.bold || m.italic != c.italic ||
			m.fixedPitch != c.fixedPitch || m.serif != c.serif {
			t.Errorf("base14(%q) = bold=%v italic=%v fixed=%v serif=%v, want %v/%v/%v/%v",
				c.name, m.bold, m.italic, m.fixedPitch, m.serif,
				c.bold, c.italic, c.fixedPitch, c.serif)
		}
	}
}

func TestBase14Widths(t *testing.T) {
	// Spot-check against the Adobe AFM metrics.
	h, _ := base14("Helvetica")
	for r, want := range map[rune]float64{' ': 278, 'A': 667, 'a': 556, 'i': 222, 'W': 944} {
		got, ok := h.widths.lookup(r)
		if !ok || got != want {
			t.Errorf("Helvetica width of %q = %v (ok=%v), want %v", r, got, ok, want)
		}
	}

	tr, _ := base14("Times-Roman")
	for r, want := range map[rune]float64{' ': 250, 'A': 722, 'a': 444, 'i': 278} {
		got, ok := tr.widths.lookup(r)
		if !ok || got != want {
			t.Errorf("Times-Roman width of %q = %v (ok=%v), want %v", r, got, ok, want)
		}
	}

	// Courier is monospaced at 600 everywhere and carries no table.
	c, _ := base14("Courier")
	if c.defWidth != 600 {
		t.Errorf("Courier default width = %v, want 600", c.defWidth)
	}
}

func TestNormalizeFamily(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ABCDEF+Minion-BoldItalic", "Minion"},
		{"Times-Roman", "Times"},
		{"Helvetica", "Helvetica"},
		{"ArialMT", "Arial"},
		{"Arial,Bold", "Arial"},
	}
	for _, c := range cases {
		if got := normalizeFamily(c.in); got != c.want {
			t.Errorf("normalizeFamily(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsTeXTextFont(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// Text families use OT1.
		{"CMR10", true},
		{"NYYIGP+CMR10", true},
		{"CMBX12", true},
		{"CMTI10", true},
		{"CMTT9", true},
		{"CMSS17", true},
		{"LMRoman10-Regular", true},
		{"LMMono10-Regular", true},
		// Math families use OML, OMS, or OMX and must be excluded, including
		// the ones sharing a prefix with a text family.
		{"CMMI10", false},
		{"CMSY10", false},
		{"CMEX10", false},
		{"CMBSY10", false},
		{"MSAM10", false},
		{"MSBM10", false},
		{"DZGOUJ+CMMI10", false},
		// Not TeX at all.
		{"Helvetica", false},
		{"Times-Roman", false},
		{"ArialMT", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isTeXTextFont(c.name); got != c.want {
			t.Errorf("isTeXTextFont(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOT1Encoding(t *testing.T) {
	// The f-ligature block is the reason this table exists: without it every
	// ligature in a LaTeX document decodes to U+FFFD.
	for code, want := range map[byte]rune{
		0x0B: 'ﬀ', 0x0C: 'ﬁ', 0x0D: 'ﬂ', 0x0E: 'ﬃ', 0x0F: 'ﬄ',
		0x10: 'ı', 0x11: 'ȷ',
		// TeX diverges from ASCII at these positions.
		0x22: '”', 0x27: '’', 0x3C: '¡', 0x3E: '¿',
		0x5C: '“', 0x60: '‘', 0x7B: '–', 0x7C: '—',
	} {
		if got := OT1Encoding[code]; got != want {
			t.Errorf("OT1Encoding[0x%02X] = %q, want %q", code, got, want)
		}
	}

	// Letters and digits sit where ASCII puts them.
	for c := byte('a'); c <= 'z'; c++ {
		if got := OT1Encoding[c]; got != rune(c) {
			t.Errorf("OT1Encoding[%q] = %q, want %q", c, got, rune(c))
		}
	}
	for c := byte('0'); c <= '9'; c++ {
		if got := OT1Encoding[c]; got != rune(c) {
			t.Errorf("OT1Encoding[%q] = %q, want %q", c, got, rune(c))
		}
	}
	// Every position must be defined; a gap would decode to U+FFFD.
	for i := 0; i < 128; i++ {
		if OT1Encoding[i] == 0 {
			t.Errorf("OT1Encoding[0x%02X] is undefined", i)
		}
	}
}
