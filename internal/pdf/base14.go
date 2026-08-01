package pdf

import "strings"

// Widths for the standard 14 fonts, which PDFs may reference without
// embedding and without a /Widths array. Without these, advance and space
// detection fall back to a flat guess and line assembly degrades badly on
// older documents.
//
// Values are AFM widths in 1/1000 em, indexed by rune over the printable
// ASCII range. Runes outside the table fall back to the face's default.
// Sources: Adobe Core 14 AFM metrics.

// asciiWidths is 95 entries covering U+0020 through U+007E.
type asciiWidths [95]uint16

func (w *asciiWidths) lookup(r rune) (float64, bool) {
	if r < 0x20 || r > 0x7E {
		return 0, false
	}
	return float64(w[r-0x20]), true
}

var helveticaWidths = asciiWidths{
	278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333,
	278, 278, 556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278,
	584, 584, 584, 556, 1015, 667, 667, 722, 722, 667, 611, 778, 722, 278,
	500, 667, 556, 833, 722, 778, 667, 778, 722, 667, 611, 722, 667, 944,
	667, 667, 611, 278, 278, 278, 469, 556, 333, 556, 556, 500, 556, 556,
	278, 556, 556, 222, 222, 500, 222, 833, 556, 556, 556, 556, 333, 500,
	278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584,
}

var helveticaBoldWidths = asciiWidths{
	278, 333, 474, 556, 556, 889, 722, 238, 333, 333, 389, 584, 278, 333,
	278, 278, 556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 333, 333,
	584, 584, 584, 611, 975, 722, 722, 722, 722, 667, 611, 778, 722, 278,
	556, 722, 611, 833, 722, 778, 667, 778, 722, 667, 611, 722, 667, 944,
	667, 667, 611, 333, 278, 333, 584, 556, 333, 556, 611, 556, 611, 556,
	333, 611, 611, 278, 278, 556, 278, 889, 611, 611, 611, 611, 389, 556,
	333, 611, 556, 778, 556, 556, 500, 389, 280, 389, 584,
}

var timesRomanWidths = asciiWidths{
	250, 333, 408, 500, 500, 833, 778, 180, 333, 333, 500, 564, 250, 333,
	250, 278, 500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 278, 278,
	564, 564, 564, 444, 921, 722, 667, 667, 722, 611, 556, 722, 722, 333,
	389, 722, 611, 889, 722, 722, 556, 722, 667, 556, 611, 722, 722, 944,
	722, 722, 611, 333, 278, 333, 469, 500, 333, 444, 500, 444, 500, 444,
	333, 500, 500, 278, 278, 500, 278, 778, 500, 500, 500, 500, 333, 389,
	278, 500, 500, 722, 500, 500, 444, 480, 200, 480, 541,
}

var timesBoldWidths = asciiWidths{
	250, 333, 555, 500, 500, 1000, 833, 278, 333, 333, 500, 570, 250, 333,
	250, 278, 500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 333, 333,
	570, 570, 570, 500, 930, 722, 667, 722, 722, 667, 611, 778, 778, 389,
	500, 778, 667, 944, 722, 778, 611, 778, 722, 556, 667, 722, 722, 1000,
	722, 722, 667, 333, 278, 333, 581, 500, 333, 500, 556, 444, 556, 444,
	333, 500, 556, 278, 333, 556, 278, 833, 556, 500, 556, 556, 444, 389,
	333, 556, 500, 722, 500, 500, 444, 394, 220, 394, 520,
}

var timesItalicWidths = asciiWidths{
	250, 333, 420, 500, 500, 833, 778, 214, 333, 333, 500, 675, 250, 333,
	250, 278, 500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 333, 333,
	675, 675, 675, 500, 920, 611, 611, 667, 722, 611, 611, 722, 722, 333,
	444, 667, 556, 833, 667, 722, 611, 722, 611, 500, 556, 722, 611, 833,
	611, 556, 556, 389, 278, 389, 422, 500, 333, 500, 500, 444, 500, 444,
	278, 500, 500, 278, 278, 444, 278, 722, 500, 500, 500, 500, 389, 389,
	278, 500, 444, 667, 444, 444, 389, 400, 275, 400, 541,
}

var timesBoldItalicWidths = asciiWidths{
	250, 389, 555, 500, 500, 833, 778, 278, 333, 333, 500, 570, 250, 333,
	250, 278, 500, 500, 500, 500, 500, 500, 500, 500, 500, 500, 333, 333,
	570, 570, 570, 500, 832, 667, 667, 667, 722, 667, 667, 722, 778, 389,
	500, 667, 611, 889, 722, 722, 611, 722, 667, 556, 611, 722, 667, 889,
	667, 611, 611, 333, 278, 333, 570, 500, 333, 500, 500, 444, 500, 444,
	333, 500, 556, 278, 278, 500, 278, 778, 556, 500, 500, 500, 389, 389,
	278, 556, 444, 667, 500, 444, 389, 348, 220, 348, 570,
}

// base14Metrics describes a standard face: its width table and the style
// flags implied by its name.
type base14Metrics struct {
	widths     *asciiWidths
	defWidth   float64
	bold       bool
	italic     bool
	fixedPitch bool
	serif      bool
}

// base14 resolves a BaseFont name to standard metrics. The name is matched
// case-insensitively after the subset prefix is stripped, so "ABCDEF+Times-Bold"
// resolves the same as "Times-Bold".
//
// ok is false for Symbol, ZapfDingbats, and unrecognized names; those carry no
// meaningful Latin metrics.
func base14(name string) (base14Metrics, bool) {
	n := strings.ToLower(stripSubsetPrefix(name))
	n = strings.ReplaceAll(n, " ", "")

	bold := strings.Contains(n, "bold") || strings.Contains(n, "black") ||
		strings.Contains(n, "heavy")
	italic := strings.Contains(n, "italic") || strings.Contains(n, "oblique")

	switch {
	case strings.Contains(n, "courier") || strings.Contains(n, "mono"):
		// Every Courier face is 600 units wide at every code.
		return base14Metrics{
			defWidth: 600, bold: bold, italic: italic, fixedPitch: true,
		}, true

	case strings.Contains(n, "times") || strings.Contains(n, "serif") ||
		strings.Contains(n, "roman") || strings.Contains(n, "georgia") ||
		strings.Contains(n, "garamond") || strings.Contains(n, "minion"):
		m := base14Metrics{defWidth: 500, bold: bold, italic: italic, serif: true}
		switch {
		case bold && italic:
			m.widths = &timesBoldItalicWidths
		case bold:
			m.widths = &timesBoldWidths
		case italic:
			m.widths = &timesItalicWidths
		default:
			m.widths = &timesRomanWidths
		}
		return m, true

	case strings.Contains(n, "helvetica") || strings.Contains(n, "arial") ||
		strings.Contains(n, "sans"):
		m := base14Metrics{defWidth: 556, bold: bold, italic: italic}
		if bold {
			m.widths = &helveticaBoldWidths
		} else {
			m.widths = &helveticaWidths
		}
		return m, true
	}

	return base14Metrics{}, false
}

// stripSubsetPrefix removes the "ABCDEF+" tag that subsetted fonts carry.
func stripSubsetPrefix(name string) string {
	if len(name) > 7 && name[6] == '+' {
		allUpper := true
		for i := 0; i < 6; i++ {
			if name[i] < 'A' || name[i] > 'Z' {
				allUpper = false
				break
			}
		}
		if allUpper {
			return name[7:]
		}
	}
	return name
}
