package decant

import "testing"

// The scope detection in spec section 1 is tested here rather than through a
// synthetic PDF because testpdf writes literal string bytes against a base-14
// font, so a Hebrew fixture decodes back as Latin. The end-to-end path is
// covered by TestCorpusScopeWarnings against the real Arabic documents.

func TestIsRTLCoversTheScripts(t *testing.T) {
	rtl := map[string]rune{
		"Hebrew":              'ש',
		"Arabic":              'ح',
		"Arabic presentation": 'ﻲ',
		"Syriac":              'ܐ',
		"Thaana":              'ހ',
		"N'Ko":                'ߊ',
	}
	for name, r := range rtl {
		if !isRTL(r) {
			t.Errorf("%s %q not detected as right-to-left", name, r)
		}
	}

	ltr := map[string]rune{
		"Latin":      'a',
		"Greek":      'α',
		"Cyrillic":   'д',
		"CJK":        '漢',
		"digit":      '7',
		"whitespace": ' ',
	}
	for name, r := range ltr {
		if isRTL(r) {
			t.Errorf("%s %q wrongly detected as right-to-left", name, r)
		}
	}
}

func TestCountScriptsIgnoresSharedCharacters(t *testing.T) {
	// Digits, punctuation and spaces take direction from context, so counting
	// them would dilute the ratio on exactly the pages this exists to catch:
	// an Arabic page carrying folios and Latin punctuation.
	letters, rtl := countScripts("שלום, 1234 — עולם!")
	if letters != 8 {
		t.Errorf("letters = %d, want 8 (only the Hebrew)", letters)
	}
	if rtl != 8 {
		t.Errorf("rtl = %d, want 8", rtl)
	}

	if l, r := countScripts("1234 !!! ..."); l != 0 || r != 0 {
		t.Errorf("a string with no letters counted %d/%d", r, l)
	}
}

func TestCountScriptsMixesScripts(t *testing.T) {
	letters, rtl := countScripts("hello שלום")
	if letters != 9 {
		t.Errorf("letters = %d, want 9", letters)
	}
	if rtl != 4 {
		t.Errorf("rtl = %d, want 4", rtl)
	}
}

// scopeReport builds a report with the given per-page counts.
func scopeReport(pages ...PageMetrics) *Report {
	r := newReport("test")
	r.Pages = append(r.Pages, pages...)
	return r
}

func warnCount(r *Report, substr string) int {
	n := 0
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityWarning && contains(d.Message, substr) {
			n++
		}
	}
	return n
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func newTestConverter(t *testing.T) *Converter {
	t.Helper()
	c, err := New(DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestWarnOnScopeFiresAboveTheRatio(t *testing.T) {
	c := newTestConverter(t)
	rep := scopeReport(PageMetrics{Letters: 100, RTLLetters: 70})
	c.warnOnScope(rep)

	if warnCount(rep, "right-to-left") != 1 {
		t.Errorf("no warning at 70%% right-to-left")
	}
	if rep.RTLLetterRatio != 0.7 {
		t.Errorf("RTLLetterRatio = %v, want 0.7", rep.RTLLetterRatio)
	}
}

func TestWarnOnScopeStaysQuietBelowTheRatio(t *testing.T) {
	// A Latin book quoting a line of Hebrew is not a bidirectional document.
	c := newTestConverter(t)
	rep := scopeReport(PageMetrics{Letters: 100, RTLLetters: 5})
	c.warnOnScope(rep)

	if n := warnCount(rep, "right-to-left"); n != 0 {
		t.Errorf("warned at 5%% right-to-left")
	}
	if rep.RTLLetterRatio != 0.05 {
		t.Errorf("RTLLetterRatio = %v, want 0.05", rep.RTLLetterRatio)
	}
}

func TestWarnOnScopeIsTunable(t *testing.T) {
	opts := DefaultOptions()
	opts.Heuristics.RTLLetterRatio = 0.01
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	rep := scopeReport(PageMetrics{Letters: 100, RTLLetters: 5})
	c.warnOnScope(rep)

	if warnCount(rep, "right-to-left") != 1 {
		t.Error("lowering RTLLetterRatio did not lower the threshold")
	}
}

func TestWarnOnScopeReportsVerticalText(t *testing.T) {
	c := newTestConverter(t)
	rep := scopeReport(
		PageMetrics{Letters: 50, VerticalText: true},
		PageMetrics{Letters: 50},
		PageMetrics{Letters: 50, VerticalText: true},
	)
	c.warnOnScope(rep)

	if warnCount(rep, "vertical writing mode") != 1 {
		t.Error("no warning for vertical text")
	}
	if rep.VerticalTextPages != 2 {
		t.Errorf("VerticalTextPages = %d, want 2", rep.VerticalTextPages)
	}
}

func TestWarnOnScopeHandlesAnEmptyDocument(t *testing.T) {
	// No letters must not divide by zero or warn about a script it never saw.
	c := newTestConverter(t)
	rep := scopeReport(PageMetrics{})
	c.warnOnScope(rep)

	if len(rep.Diagnostics) != 0 {
		t.Errorf("an empty document produced %v", rep.Diagnostics)
	}
	if rep.RTLLetterRatio != 0 {
		t.Errorf("RTLLetterRatio = %v, want 0", rep.RTLLetterRatio)
	}
}
