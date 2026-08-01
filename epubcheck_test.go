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
			name: "headings",
			opts: defaultOpts,
			pdf:  simpleDoc,
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
