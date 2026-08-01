package hyphen

import (
	"embed"
	"fmt"
	"strings"
	"sync"
	"unicode"
)

// patternFS holds the vendored hyph-utf8 pattern files.
//
// Only license-compatible sets are here. Spec section 4.6 requires MIT, BSD,
// or unrestricted terms and says to drop a language rather than complicate
// decant's license; Russian and Swedish are LPPL-only and are therefore
// absent even though section 4.6's language list names them. THIRD_PARTY.md
// records the audit.
//
//go:embed patterns/*.tex
var patternFS embed.FS

// patternFiles maps a language subtag to its vendored file.
var patternFiles = map[string]string{
	"en": "patterns/hyph-en-us.tex",
	"de": "patterns/hyph-de-1996.tex",
	"es": "patterns/hyph-es.tex",
	"fr": "patterns/hyph-fr.tex",
	"it": "patterns/hyph-it.tex",
	"nl": "patterns/hyph-nl.tex",
	"pl": "patterns/hyph-pl.tex",
	"pt": "patterns/hyph-pt.tex",
}

// Languages returns the shipped language subtags, sorted.
func Languages() []string {
	out := make([]string, 0, len(patternFiles))
	for k := range patternFiles {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*Hyphenator{}
)

// ErrNoPatterns reports that no pattern set ships for a language.
//
// Spec section 4.6 is explicit that this disables dehyphenation and gets
// recorded, rather than falling back to English patterns and guessing.
type ErrNoPatterns struct{ Language string }

func (e *ErrNoPatterns) Error() string {
	return fmt.Sprintf("no hyphenation patterns ship for language %q", e.Language)
}

// For returns the hyphenator for a BCP 47 language tag.
//
// Matching is on the primary subtag, so "en-GB", "en_US", and "en" all
// resolve to the same set. Parsing is done once per language and cached.
func For(lang string) (*Hyphenator, error) {
	key := primarySubtag(lang)
	if key == "" {
		return nil, &ErrNoPatterns{Language: lang}
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()

	if h, ok := cache[key]; ok {
		if h == nil {
			return nil, &ErrNoPatterns{Language: lang}
		}
		return h, nil
	}

	path, ok := patternFiles[key]
	if !ok {
		cache[key] = nil
		return nil, &ErrNoPatterns{Language: lang}
	}

	src, err := patternFS.ReadFile(path)
	if err != nil {
		cache[key] = nil
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	h := Parse(key, src)
	if h.PatternCount() == 0 {
		cache[key] = nil
		return nil, fmt.Errorf("%s contained no patterns", path)
	}
	cache[key] = h
	return h, nil
}

// primarySubtag lowercases a language tag and takes the part before any
// region or script separator.
func primarySubtag(lang string) string {
	s := strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(s, "-_"); i > 0 {
		s = s[:i]
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return ""
		}
	}
	return s
}

// Decision records why a line-break hyphen was kept or dropped, so the
// conversion report can carry the reasoning spec section 4.6 asks for.
type Decision struct {
	// Left and Right are the fragments either side of the hyphen.
	Left, Right string
	// Joined is the word formed by removing the hyphen.
	Joined string
	// Drop reports the outcome: true removes the hyphen and joins the word.
	Drop bool
	// Reason is a short explanation.
	Reason string
}

// Join decides what to do with a hyphen at the end of a line.
//
// Spec section 4.6 inverts Liang's algorithm: form the joined token and ask
// the patterns whether a break is legal at the fragment boundary. A permitted
// break means the hyphen is where the typesetter put it, so it goes. A
// forbidden break means the hyphen belongs to the word, so it stays.
//
// The override rules come from the same section. They exist because patterns
// describe common vocabulary, not proper nouns, compounds, or anything with a
// digit in it.
func (h *Hyphenator) Join(left, right string) Decision {
	d := Decision{Left: left, Right: right, Joined: left + right}

	if left == "" || right == "" {
		d.Reason = "empty fragment"
		return d
	}

	// Both fragments capitalized: a hyphenated proper noun such as
	// "Sachs-Wolfe", not a broken word.
	if startsUpper(left) && startsUpper(right) {
		d.Reason = "both fragments capitalized; likely a hyphenated proper noun"
		return d
	}

	// A digit on either side: "COVID-19", "section-3". Never a line break
	// artifact.
	if hasDigitEdge(left, right) {
		d.Reason = "a digit sits beside the hyphen"
		return d
	}

	// Spec 4.6 conditions the test on the next line starting lowercase.
	if !startsLower(right) {
		d.Reason = "the continuation does not start lowercase"
		return d
	}

	if !isAllLetters(d.Joined) {
		d.Reason = "the joined token is not all letters"
		return d
	}

	n := len([]rune(left))
	if h.AllowsBreakAt(d.Joined, n) {
		d.Drop = true
		d.Reason = "the patterns permit a break here, so the hyphen is a typesetting artifact"
		return d
	}
	d.Reason = "the patterns forbid a break here, so the hyphen is lexical"
	return d
}

func startsUpper(s string) bool {
	for _, r := range s {
		return unicode.IsUpper(r)
	}
	return false
}

func startsLower(s string) bool {
	for _, r := range s {
		return unicode.IsLower(r)
	}
	return false
}

// hasDigitEdge reports a digit immediately adjacent to the hyphen.
func hasDigitEdge(left, right string) bool {
	lr := []rune(left)
	if len(lr) > 0 && unicode.IsDigit(lr[len(lr)-1]) {
		return true
	}
	for _, r := range right {
		return unicode.IsDigit(r)
	}
	return false
}
