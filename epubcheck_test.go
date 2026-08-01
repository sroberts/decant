package decant_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sroberts/decant"
	"github.com/sroberts/decant/internal/testpdf"
)

// TestEPUBCheckValidation runs the real validator against generated output.
//
// Spec section 12 makes zero epubcheck errors a merge gate. The binary needs
// a JVM, so the test skips when it is absent rather than failing on a
// developer machine that has not installed it; CI installs it and therefore
// enforces the gate.
func TestEPUBCheckValidation(t *testing.T) {
	bin, err := exec.LookPath("epubcheck")
	if err != nil {
		t.Skip("epubcheck not installed; CI enforces this gate")
	}

	cases := []struct {
		name string
		opts func() decant.Options
		pdf  func() []byte
	}{
		{
			name: "standard",
			opts: defaultOpts,
			pdf:  simpleDoc,
		},
		{
			name: "crosspoint",
			opts: func() decant.Options {
				o := defaultOpts()
				o.Profile = decant.ProfileCrossPoint
				o.ApplyProfileDefaults()
				return o
			},
			pdf: simpleDoc,
		},
		{
			name: "minimal",
			opts: func() decant.Options {
				o := defaultOpts()
				o.Profile = decant.ProfileMinimal
				o.ApplyProfileDefaults()
				return o
			},
			pdf: simpleDoc,
		},
		{
			name: "split-at-page",
			opts: func() decant.Options {
				o := defaultOpts()
				o.SplitAt = decant.SplitAtPage
				return o
			},
			pdf: simpleDoc,
		},
		{
			name: "chunk-split",
			opts: func() decant.Options {
				o := defaultOpts()
				o.SplitAt = decant.SplitAtNone
				o.MaxChunkBytes = 4096
				return o
			},
			pdf: longDoc,
		},
		{
			name: "hostile-metadata",
			opts: defaultOpts,
			pdf:  hostileMetadataDoc,
		},
		{
			name: "headings-and-chapters",
			opts: defaultOpts,
			pdf:  headingDoc,
		},
		{
			name: "nested-nav",
			opts: func() decant.Options {
				o := defaultOpts()
				o.SplitAt = decant.SplitAtNone
				return o
			},
			pdf: nestedHeadingDoc,
		},
		{
			name: "two-column",
			opts: defaultOpts,
			pdf:  twoColumnDoc,
		},
		{
			name: "outline-driven",
			opts: defaultOpts,
			pdf:  outlineDoc,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, _ := buildDoc(t, c.pdf(), c.opts())

			path := filepath.Join(t.TempDir(), c.name+".epub")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatalf("writing epub: %v", err)
			}

			out, err := exec.Command(bin, path).CombinedOutput()
			if err != nil || strings.Contains(string(out), "ERROR") {
				t.Errorf("epubcheck reported problems:\n%s", out)
			}
		})
	}
}

// longDoc is large enough that a small chunk limit splits it.
func longDoc() []byte {
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines,
			"This is a line of body text used to grow the document past the chunk limit.",
			"It continues onto a second line so the paragraph has some substance to it.",
			"",
		)
	}
	return testpdf.New().
		SetInfo("Title", "Long Document").
		AddPage(612, 2000, testpdf.TextPage("F1", 10, 72, 1950, 14, lines)).
		Build()
}

// hostileMetadataDoc carries the characters most likely to break XML output.
func hostileMetadataDoc() []byte {
	return testpdf.New().
		SetInfo("Title", `A & B <C> "D" 'E'`).
		SetInfo("Author", "Ampersand & Company").
		AddPage(612, 792, testpdf.TextPage("F1", 12, 72, 720, 14, []string{
			`Text with & ampersands, <angle brackets>, and "quotes" in it.`,
			`More text with a stray ]]> sequence and a -- double hyphen.`,
		})).
		Build()
}

// nestedHeadingDoc has two heading levels, so the TOC nests.
func nestedHeadingDoc() []byte {
	content := testpdf.HeadingPageAt("F1", 10, 22, 13, 1500, [][]string{{
		"Part One",
		"Body text introducing the part, running to a couple of lines so",
		"the classifier sees a genuine paragraph beneath the heading.",
	}}) + testpdf.HeadingPageAt("F1", 10, 15, 13, 1330, [][]string{
		{
			"Chapter A",
			"Body text of chapter A running to more than one line so it",
			"reads clearly as body rather than display type.",
		},
		{
			"Chapter B",
			"Body text of chapter B, likewise more than a single line in",
			"length so the block is unambiguous.",
		},
	})
	return testpdf.New().
		SetInfo("Title", "Nested Structure").
		AddPage(612, 1600, content).
		Build()
}

// twoColumnDoc is the academic-paper shape: a spanning title over two columns.
func twoColumnDoc() []byte {
	// A real two-column page carries many rows; the ColumnMinRows guard
	// deliberately refuses to trust a projection profile with fewer.
	left := []string{
		"Alpha one text here", "Alpha two text here", "Alpha three here",
		"Alpha four text now", "Alpha five text now", "Alpha six text now",
		"Alpha seven is here", "Alpha eight is here", "Alpha nine is now",
		"Alpha ten text here", "Alpha eleven here", "Alpha twelve now",
	}
	right := []string{
		"Beta one text here", "Beta two text here", "Beta three here",
		"Beta four text now", "Beta five text now", "Beta six text now",
		"Beta seven is here", "Beta eight is here", "Beta nine is now",
		"Beta ten text here", "Beta eleven here", "Beta twelve now",
	}
	return testpdf.New().
		SetInfo("Title", "Two Column Paper").
		AddPage(612, 792, testpdf.TwoColumnPage("F1", 10, 13,
			"A Paper Title Spanning The Full Measure Of Both Columns", left, right)).
		Build()
}

// outlineDoc carries PDF bookmarks, which drive structure authoritatively.
func outlineDoc() []byte {
	return testpdf.New().
		SetInfo("Title", "Outlined Document").
		AddPage(612, 792, testpdf.HeadingPage("F1", 10, 16, 13, [][]string{{
			"First Inferred",
			"Body text on the first page running to a couple of lines so",
			"the block classifies cleanly as a paragraph.",
		}})).
		AddPage(612, 792, testpdf.HeadingPage("F1", 10, 16, 13, [][]string{{
			"Second Inferred",
			"Body text on the second page, likewise more than one line",
			"long so it reads as body text.",
		}})).
		AddNestedBookmark("Chapter One", 0, 720, "Section 1.1", 1, 720).
		Build()
}
