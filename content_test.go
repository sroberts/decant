package decant_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/sroberts/decant"
)

// Content fidelity, spec section 10.
//
// The question these answer is whether the EPUB still says what the PDF said.
// It splits in two, and the split matters because only one half has a
// trustworthy oracle.
//
// Serialization is exact: every word the analyzed Document holds must appear
// in the EPUB, because stages 7 and 8 format text, they do not select it.
// That is checked here with no external tool and no threshold.
//
// Extraction is not exact. Comparing against another PDF tool means comparing
// against a different set of decisions about what counts as content, so it is
// measured as a tracked number in the corpus manifest rather than gated. See
// textRecall.

// Inline elements are zero-width when text is extracted; block elements
// separate words. Treating a <sup> as a space would split the word it sits
// inside, which is how a superscripted footnote marker turns "ap0" into two
// tokens that then look lost.
var (
	inlineTagRe = regexp.MustCompile(`(?i)</?(sup|sub|em|strong|i|b|span|a|code|small)\b[^>]*>`)
	xmlTagRe    = regexp.MustCompile(`<[^>]*>`)
)

// stripMarkup renders markup to text the way a reader would see it.
func stripMarkup(s string) string {
	s = inlineTagRe.ReplaceAllString(s, "")
	return xmlTagRe.ReplaceAllString(s, " ")
}

// epubText returns the visible text of every chapter, in spine order.
func epubText(t *testing.T, epubBytes []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(epubBytes), int64(len(epubBytes)))
	if err != nil {
		t.Fatalf("opening EPUB: %v", err)
	}

	var names []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "OEBPS/text/") {
			names = append(names, f.Name)
		}
	}
	sort.Strings(names)

	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		byName[f.Name] = f
	}

	var sb strings.Builder
	for _, n := range names {
		rc, err := byName[n].Open()
		if err != nil {
			t.Fatalf("opening %s: %v", n, err)
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("reading %s: %v", n, err)
		}
		sb.WriteString(unescapeXML(stripMarkup(string(raw))))
		sb.WriteByte(' ')
	}
	return sb.String()
}

func unescapeXML(s string) string {
	return strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&apos;", "'", "&#39;", "'",
	).Replace(s)
}

// words splits on whitespace and folds the differences that carry no meaning
// for a content comparison.
//
// Private-use code points are dropped first. Block.Text carries the sentinels
// that mark a superscript run, and the renderer strips them on the way out,
// so leaving them in would report every word containing one as lost.
func words(s string) []string {
	s = strings.Map(func(r rune) rune {
		if r >= 0xE000 && r <= 0xF8FF {
			return -1
		}
		return r
	}, s)

	var out []string
	for _, f := range strings.Fields(strings.ToLower(s)) {
		f = strings.Trim(f, ".,;:!?()[]{}\"'`“”‘’—–-")
		if f == "" {
			continue
		}
		out = append(out, f)
	}
	return out
}

func TestSerializationLosesNoText(t *testing.T) {
	// Stages 7 and 8 format text; they do not choose it. Anything the block
	// tree holds after Analyze must therefore survive into the EPUB, so this
	// is an equality check rather than a threshold. It covers the half of the
	// pipeline the reading-order test cannot see: that test reads
	// doc.Blocks, so text lost in rendering or chunk splitting is invisible
	// to it.
	for _, tc := range []struct {
		name string
		src  []byte
		opts decant.Options
	}{
		{"simple", simpleDoc(), defaultOpts()},
		{"headings", headingDoc(), defaultOpts()},
		{"two-column", twoColumnDoc(), defaultOpts()},
		{"lists-and-footnotes", listDoc(), defaultOpts()},
		{"tables", epubcheckTableDoc(), defaultOpts()},
		{"figures", figureDoc(), defaultOpts()},
		{"chunk-split", longDoc(), func() decant.Options {
			o := defaultOpts()
			o.SplitAt = decant.SplitAtNone
			o.MaxChunkBytes = 4096
			return o
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, err := decant.New(tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := conv.Analyze(context.Background(),
				bytes.NewReader(tc.src), int64(len(tc.src)))
			if err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			if _, err := conv.Write(context.Background(), doc, &out); err != nil {
				t.Fatal(err)
			}

			// A table block carries the same words twice, flattened into
			// Text and again in TableRows. Counting both would demand the
			// EPUB repeat every cell.
			var blockText strings.Builder
			for _, b := range doc.Blocks {
				blockText.WriteString(b.Text)
				blockText.WriteByte(' ')
			}

			want := words(blockText.String())
			if len(want) == 0 {
				t.Fatal("the fixture analyzed to no text at all")
			}

			have := map[string]int{}
			for _, w := range words(epubText(t, out.Bytes())) {
				have[w]++
			}

			var missing []string
			for _, w := range want {
				if have[w] > 0 {
					have[w]--
					continue
				}
				missing = append(missing, w)
			}
			if len(missing) > 0 {
				n := len(missing)
				if n > 12 {
					n = 12
				}
				t.Errorf("%d of %d words did not survive serialization: %s",
					len(missing), len(want), strings.Join(missing[:n], " "))
			}
		})
	}
}
