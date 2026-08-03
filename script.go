package decant

import "unicode"

// Script detection for the scope warnings in spec section 1.
//
// Right-to-left and vertical CJK layout are out of scope beyond basic text
// extraction, and the section asks for them to be detected and warned about
// rather than silently mishandled. decant extracts the runes correctly in both
// cases; what it does not do is reorder a bidirectional paragraph or set a
// vertical column, so the reader sees logical order where the source showed
// something else.

// rtlRanges are the Unicode blocks whose scripts are written right to left.
//
// Explicit ranges rather than x/text/unicode/bidi: the dependency exists to
// implement the bidi algorithm, which decant deliberately does not, and a
// table this small is easier to audit than a call into one that answers a
// subtly different question.
var rtlRanges = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x0590, Hi: 0x05FF, Stride: 1}, // Hebrew
		{Lo: 0x0600, Hi: 0x06FF, Stride: 1}, // Arabic
		{Lo: 0x0700, Hi: 0x074F, Stride: 1}, // Syriac
		{Lo: 0x0750, Hi: 0x077F, Stride: 1}, // Arabic Supplement
		{Lo: 0x0780, Hi: 0x07BF, Stride: 1}, // Thaana
		{Lo: 0x07C0, Hi: 0x07FF, Stride: 1}, // N'Ko
		{Lo: 0x0800, Hi: 0x083F, Stride: 1}, // Samaritan
		{Lo: 0x0840, Hi: 0x085F, Stride: 1}, // Mandaic
		{Lo: 0x0860, Hi: 0x086F, Stride: 1}, // Syriac Supplement
		{Lo: 0x08A0, Hi: 0x08FF, Stride: 1}, // Arabic Extended-A
		{Lo: 0xFB1D, Hi: 0xFB4F, Stride: 1}, // Hebrew presentation forms
		{Lo: 0xFB50, Hi: 0xFDFF, Stride: 1}, // Arabic presentation forms A
		{Lo: 0xFE70, Hi: 0xFEFF, Stride: 1}, // Arabic presentation forms B
	},
}

// isRTL reports whether a rune belongs to a right-to-left script.
func isRTL(r rune) bool { return unicode.Is(rtlRanges, r) }

// countScripts returns the number of letters in s and how many of them are
// right to left.
//
// Only letters count. Digits, punctuation and spaces are shared between
// scripts and take their direction from context, so including them would
// dilute the ratio on exactly the documents this exists to catch: an Arabic
// page carrying page numbers and Latin punctuation.
func countScripts(s string) (letters, rtl int) {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if isRTL(r) {
			rtl++
		}
	}
	return letters, rtl
}
