// Package hyphen implements Liang's hyphenation algorithm over TeX pattern
// sets, which spec section 4.6 uses inverted to decide whether a line-break
// hyphen is a typesetting artifact or part of the word.
package hyphen

import (
	"strings"
	"unicode"
)

// Hyphenator answers where a word may be broken.
//
// The patterns come from TeX. Each is a letter sequence carrying digits at
// some inter-letter positions, e.g. "hy3ph" or ".ach4". Scoring a word takes
// the maximum digit seen at every position across all matching patterns; an
// odd score means a break is permitted there.
type Hyphenator struct {
	// Language is the BCP 47 tag this set covers.
	Language string

	// patterns maps a pattern's letters to its digit values. Values has one
	// more entry than the letter count: values[i] is the score for the
	// position before letter i.
	patterns map[string][]uint8

	// exceptions holds words from \hyphenation{}, mapping the lowercased word
	// to the positions where a break is allowed. TeX treats these as
	// authoritative, overriding the patterns entirely.
	exceptions map[string][]int

	// maxPattern is the longest pattern in letters, which bounds the lookup
	// window.
	maxPattern int

	// leftMin and rightMin are the minimum letters that must remain on each
	// side of a break, from the file's typesetting hyphenmins.
	leftMin, rightMin int
}

// Parse reads a hyph-utf8 pattern file.
//
// The format is a TeX source file: a \patterns{...} group of whitespace
// separated patterns, optionally a \hyphenation{...} group of explicit
// exceptions, and comment lines starting with %.
func Parse(lang string, src []byte) *Hyphenator {
	h := &Hyphenator{
		Language:   lang,
		patterns:   map[string][]uint8{},
		exceptions: map[string][]int{},
		leftMin:    2,
		rightMin:   3,
	}

	text := string(src)
	h.readHyphenmins(text)

	for _, p := range fields(group(text, `\patterns{`)) {
		h.addPattern(p)
	}
	for _, w := range fields(group(text, `\hyphenation{`)) {
		h.addException(w)
	}
	return h
}

// group extracts the body of a TeX group introduced by marker, honoring
// nesting and skipping comment lines.
func group(text, marker string) string {
	i := strings.Index(text, marker)
	if i < 0 {
		return ""
	}
	i += len(marker)

	depth := 1
	var sb strings.Builder
	inComment := false

	for ; i < len(text); i++ {
		c := text[i]
		if inComment {
			if c == '\n' {
				inComment = false
				sb.WriteByte('\n')
			}
			continue
		}
		switch c {
		case '%':
			inComment = true
		case '{':
			depth++
			sb.WriteByte(c)
		case '}':
			depth--
			if depth == 0 {
				return sb.String()
			}
			sb.WriteByte(c)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

func fields(s string) []string { return strings.Fields(s) }

// readHyphenmins pulls the typesetting hyphenmins out of the file header.
//
// They are the minimum letters TeX leaves on each side of a break. Ignoring
// them would let a two-letter fragment count as a legal break and make the
// dehyphenator too eager.
func (h *Hyphenator) readHyphenmins(text string) {
	i := strings.Index(text, "typesetting:")
	if i < 0 {
		return
	}
	// The block is a few lines of "%     left: 2" / "%     right: 3".
	window := text[i:]
	if j := strings.Index(window, "\n%\n"); j > 0 {
		window = window[:j]
	}
	if len(window) > 300 {
		window = window[:300]
	}
	if v, ok := readIntAfter(window, "left:"); ok {
		h.leftMin = v
	}
	if v, ok := readIntAfter(window, "right:"); ok {
		h.rightMin = v
	}
}

func readIntAfter(s, key string) (int, bool) {
	i := strings.Index(s, key)
	if i < 0 {
		return 0, false
	}
	rest := strings.TrimSpace(s[i+len(key):])
	n := 0
	digits := 0
	for _, r := range rest {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
		digits++
	}
	if digits == 0 {
		return 0, false
	}
	return n, true
}

// addPattern splits a pattern into its letters and its digit values.
func (h *Hyphenator) addPattern(p string) {
	runes := []rune(p)
	letters := make([]rune, 0, len(runes))
	// values has one slot per inter-letter position, plus the ends.
	values := make([]uint8, 0, len(runes)+1)
	values = append(values, 0)

	for _, r := range runes {
		if r >= '0' && r <= '9' {
			values[len(values)-1] = uint8(r - '0')
			continue
		}
		letters = append(letters, r)
		values = append(values, 0)
	}
	if len(letters) == 0 {
		return
	}

	key := string(letters)
	// A file can repeat a pattern; keep the stronger values so behavior does
	// not depend on file order.
	if existing, ok := h.patterns[key]; ok {
		for i := range values {
			if i < len(existing) && existing[i] > values[i] {
				values[i] = existing[i]
			}
		}
	}
	h.patterns[key] = values
	if n := len(letters); n > h.maxPattern {
		h.maxPattern = n
	}
}

// addException records a \hyphenation{} entry such as "as-so-ciate".
func (h *Hyphenator) addException(w string) {
	var word strings.Builder
	var breaks []int
	n := 0
	for _, r := range w {
		if r == '-' {
			breaks = append(breaks, n)
			continue
		}
		word.WriteRune(r)
		n++
	}
	if word.Len() == 0 {
		return
	}
	h.exceptions[strings.ToLower(word.String())] = breaks
}

// BreakPoints returns the positions at which word may be hyphenated, as
// counts of letters before the break.
//
// The word is lowercased and must contain only letters; callers strip
// punctuation first.
func (h *Hyphenator) BreakPoints(word string) []int {
	lower := strings.ToLower(word)
	runes := []rune(lower)
	n := len(runes)
	if n < h.leftMin+h.rightMin {
		return nil
	}

	if breaks, ok := h.exceptions[lower]; ok {
		return h.applyMins(breaks, n)
	}

	// TeX wraps the word in periods so patterns can anchor to the edges.
	padded := append(append([]rune{'.'}, runes...), '.')

	// scores[i] is the value for the position before padded[i]. Position i in
	// scores corresponds to a break after i-1 letters of the original word.
	scores := make([]uint8, len(padded)+1)

	for i := 0; i < len(padded); i++ {
		limit := h.maxPattern
		if i+limit > len(padded) {
			limit = len(padded) - i
		}
		for l := 1; l <= limit; l++ {
			values, ok := h.patterns[string(padded[i:i+l])]
			if !ok {
				continue
			}
			for k, v := range values {
				if v > scores[i+k] {
					scores[i+k] = v
				}
			}
		}
	}

	var breaks []int
	// scores index i maps to "after i-1 letters", since padded[0] is the
	// leading period.
	for i := 1; i < len(scores)-1; i++ {
		if scores[i]%2 == 1 {
			breaks = append(breaks, i-1)
		}
	}
	return h.applyMins(breaks, n)
}

// applyMins drops breaks that would leave too few letters on either side.
func (h *Hyphenator) applyMins(breaks []int, n int) []int {
	out := breaks[:0:0]
	for _, b := range breaks {
		if b < h.leftMin || n-b < h.rightMin {
			continue
		}
		out = append(out, b)
	}
	return out
}

// AllowsBreakAt reports whether the word may be hyphenated after exactly n
// letters.
//
// This is the inverted use spec section 4.6 describes: a line-break hyphen
// sitting where the patterns permit a break is a typesetting artifact and
// should be removed, while one the patterns forbid is lexical and stays.
func (h *Hyphenator) AllowsBreakAt(word string, n int) bool {
	for _, b := range h.BreakPoints(word) {
		if b == n {
			return true
		}
	}
	return false
}

// PatternCount reports how many patterns were loaded, which the report uses
// to confirm a language actually resolved to a usable set.
func (h *Hyphenator) PatternCount() int { return len(h.patterns) }

// isAllLetters reports whether every rune is a letter, which is the only
// shape the patterns are defined over.
func isAllLetters(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}
