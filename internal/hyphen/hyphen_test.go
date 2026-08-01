package hyphen

import (
	"strings"
	"testing"
)

func TestParsePatterns(t *testing.T) {
	// A miniature pattern file in the same shape as hyph-utf8's.
	src := []byte(`% comment line
% licence:
%     name: MIT
% hyphenmins:
%     typesetting:
%         left: 2
%         right: 3
\patterns{
a1b
.ac4
2b1c
}
\hyphenation{
as-so-ciate
}
`)
	h := Parse("xx", src)

	if h.PatternCount() != 3 {
		t.Errorf("PatternCount = %d, want 3", h.PatternCount())
	}
	if h.leftMin != 2 || h.rightMin != 3 {
		t.Errorf("hyphenmins = %d/%d, want 2/3", h.leftMin, h.rightMin)
	}
	if len(h.exceptions) != 1 {
		t.Errorf("exceptions = %d, want 1", len(h.exceptions))
	}
	if got := h.exceptions["associate"]; len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Errorf("associate breaks = %v, want [2 4]", got)
	}
}

func TestParseSkipsComments(t *testing.T) {
	// A comment inside the group must not become a pattern.
	src := []byte("\\patterns{\na1b\n% not a pattern\nc1d\n}\n")
	h := Parse("xx", src)
	if h.PatternCount() != 2 {
		t.Errorf("PatternCount = %d, want 2 (a comment leaked in)", h.PatternCount())
	}
}

func TestExceptionsOverridePatterns(t *testing.T) {
	// TeX treats \hyphenation entries as authoritative.
	src := []byte("\\patterns{\nas1so1ci1ate\n}\n\\hyphenation{\nasso-ciate\n}\n")
	h := Parse("xx", src)
	h.leftMin, h.rightMin = 1, 1

	got := h.BreakPoints("associate")
	if len(got) != 1 || got[0] != 4 {
		t.Errorf("breaks = %v, want [4] from the exception list", got)
	}
}

func TestLanguageTagMatching(t *testing.T) {
	// Region and script subtags must not defeat the lookup.
	for _, tag := range []string{"en", "EN", "en-US", "en_GB", "en-Latn-US", " en "} {
		if _, err := For(tag); err != nil {
			t.Errorf("For(%q): %v", tag, err)
		}
	}
	for _, tag := range []string{"", "zz", "klingon", "123"} {
		if _, err := For(tag); err == nil {
			t.Errorf("For(%q) resolved, want an error", tag)
		}
	}
}

func TestNoPatternsErrorIsTyped(t *testing.T) {
	_, err := For("zz")
	if err == nil {
		t.Fatal("expected an error")
	}
	var missing *ErrNoPatterns
	if !asErrNoPatterns(err, &missing) {
		t.Fatalf("error is %T, want *ErrNoPatterns", err)
	}
	if !strings.Contains(err.Error(), "zz") {
		t.Errorf("error does not name the language: %v", err)
	}
}

// asErrNoPatterns is a local errors.As to keep the import list minimal.
func asErrNoPatterns(err error, target **ErrNoPatterns) bool {
	e, ok := err.(*ErrNoPatterns)
	if ok {
		*target = e
	}
	return ok
}

func TestBreakPointsRespectHyphenmins(t *testing.T) {
	h, err := For("en")
	if err != nil {
		t.Fatal(err)
	}
	// Too short to break at all under 2/3 hyphenmins.
	if got := h.BreakPoints("cat"); len(got) != 0 {
		t.Errorf("cat: breaks %v, want none", got)
	}
	if got := h.BreakPoints("a"); len(got) != 0 {
		t.Errorf("a: breaks %v, want none", got)
	}
}

func TestBreakPointsIsCaseInsensitive(t *testing.T) {
	h, _ := For("en")
	lower := h.BreakPoints("hyphenation")
	upper := h.BreakPoints("HYPHENATION")
	if len(lower) != len(upper) {
		t.Fatalf("case changed the result: %v vs %v", lower, upper)
	}
	for i := range lower {
		if lower[i] != upper[i] {
			t.Errorf("case changed the result: %v vs %v", lower, upper)
			break
		}
	}
}

func TestJoinOverrides(t *testing.T) {
	h, _ := For("en")

	// Every override in spec 4.6 keeps the hyphen.
	for _, c := range []struct{ left, right string }{
		{"Sachs", "Wolfe"}, // both capitalized
		{"COVID", "19"},    // digit right
		{"x1", "y"},        // digit left
		{"foo", "Bar"},     // continuation capitalized
		{"", "word"},       // empty fragment
		{"word", ""},       // empty fragment
	} {
		if d := h.Join(c.left, c.right); d.Drop {
			t.Errorf("Join(%q,%q) dropped the hyphen: %s", c.left, c.right, d.Reason)
		}
	}
}

func TestJoinReportsJoinedForm(t *testing.T) {
	h, _ := For("en")
	d := h.Join("adip", "iscing")
	if d.Joined != "adipiscing" {
		t.Errorf("Joined = %q, want adipiscing", d.Joined)
	}
	if !d.Drop {
		t.Errorf("expected the hyphen to drop: %s", d.Reason)
	}
}

func TestGermanPatternsWork(t *testing.T) {
	// The largest shipped set, and the one most likely to expose a parsing
	// problem: 274 KB and roughly 36,000 patterns.
	h, err := For("de")
	if err != nil {
		t.Fatal(err)
	}
	if h.PatternCount() < 30000 {
		t.Errorf("German loaded only %d patterns", h.PatternCount())
	}
	if got := h.BreakPoints("Silbentrennung"); len(got) == 0 {
		t.Error("no break points for a compound German word")
	}
}

func TestCachingReturnsSameInstance(t *testing.T) {
	a, err := For("en")
	if err != nil {
		t.Fatal(err)
	}
	b, err := For("en-GB")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("two lookups of one language parsed the file twice")
	}
}

func TestBreakPointsIsDeterministic(t *testing.T) {
	h, _ := For("en")
	for i := 0; i < 20; i++ {
		got := h.BreakPoints("hyphenation")
		if len(got) != 2 || got[0] != 2 || got[1] != 6 {
			t.Fatalf("run %d gave %v", i, got)
		}
	}
}
