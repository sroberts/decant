package decant_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sroberts/decant"
	"github.com/sroberts/decant/internal/testpdf"
)

// --- inline bold and italic, spec 4.6 ---

// emphasisDoc sets one phrase of a paragraph in a bold font and another in an
// italic one, which is how a producer marks emphasis: a font switch mid-line.
func emphasisDoc() []byte {
	// F1 is Helvetica and F3 Helvetica-Bold, both supplied by the builder;
	// F5 adds the oblique face. A producer marks emphasis exactly this way,
	// by switching font mid-line.
	body := "BT\n" +
		"/F1 12 Tf 1 0 0 1 72 720 Tm (An ordinary sentence with ) Tj\n" +
		"/F3 12 Tf (emphasised words) Tj\n" +
		"/F1 12 Tf ( and then more plain text following on.) Tj\n" +
		"ET\n" +
		"BT\n" +
		"/F1 12 Tf 1 0 0 1 72 700 Tm (A second line of ordinary prose so the block ) Tj\n" +
		"/F5 12 Tf (stands out here) Tj\n" +
		"/F1 12 Tf ( within a longer paragraph of text.) Tj\n" +
		"ET\n"
	return testpdf.New().
		AddFont("F5", "/Type /Font /Subtype /Type1 /BaseFont /Helvetica-Oblique").
		AddPage(612, 792, body).
		Build()
}

func TestBoldRunBecomesStrong(t *testing.T) {
	xhtml := chapterXHTML(t, emphasisDoc(), defaultOpts())
	if !strings.Contains(xhtml, "<strong>emphasised words</strong>") {
		t.Errorf("bold run not emitted as strong:\n%s", xhtml)
	}
}

func TestItalicRunBecomesEm(t *testing.T) {
	xhtml := chapterXHTML(t, emphasisDoc(), defaultOpts())
	if !strings.Contains(xhtml, "<em>stands out here</em>") {
		t.Errorf("italic run not emitted as em:\n%s", xhtml)
	}
}

func TestSurroundingTextIsUnharmed(t *testing.T) {
	xhtml := chapterXHTML(t, emphasisDoc(), defaultOpts())
	for _, want := range []string{"An ordinary sentence with", "and then more plain text"} {
		if !strings.Contains(xhtml, want) {
			t.Errorf("text around the emphasis was damaged, missing %q:\n%s", want, xhtml)
		}
	}
}

func TestStyleRunsAreExposedOnTheBlock(t *testing.T) {
	// The block tree is the editable model, so a caller can narrow or drop a
	// run before Write.
	doc := analyze(t, emphasisDoc(), defaultOpts())
	found := false
	for _, b := range doc.Blocks {
		for _, st := range b.Styles {
			if st.Start < 0 || st.End > len(b.Text) || st.Start >= st.End {
				t.Errorf("style run %v is out of range for %q", st, b.Text)
			}
			if st.Bold || st.Italic {
				found = true
			}
		}
	}
	if !found {
		t.Error("no style runs exposed on any block")
	}
}

func TestCallerCanDropAStyleRun(t *testing.T) {
	conv, err := decant.New(defaultOpts())
	if err != nil {
		t.Fatal(err)
	}
	doc := analyze(t, emphasisDoc(), defaultOpts())
	for i := range doc.Blocks {
		doc.Blocks[i].Styles = nil
	}
	var out bytes.Buffer
	if _, err := conv.Write(contextTODO(), doc, &out); err != nil {
		t.Fatal(err)
	}
	// The chapters have to be unzipped: an EPUB is a ZIP, so searching the
	// container bytes for markup finds nothing whether or not it is there.
	xhtml := allChapterText(t, out.Bytes())
	if strings.Contains(xhtml, "<strong>") || strings.Contains(xhtml, "<em>") {
		t.Errorf("clearing Styles left emphasis in the output:\n%s", xhtml)
	}
	if !strings.Contains(xhtml, "emphasised words") {
		t.Errorf("clearing Styles dropped the words themselves:\n%s", xhtml)
	}
}

func TestWholeParagraphEmphasisIsDropped(t *testing.T) {
	// Emphasis is a contrast with its surroundings. A block set entirely in
	// one italic font is not emphasising anything, and a heading is already
	// marked up structurally.
	body := "BT\n/F3 12 Tf 1 0 0 1 72 720 Tm (Every word of this line is bold) Tj\nET\n" +
		"BT\n/F3 12 Tf 1 0 0 1 72 700 Tm (and so is every word of this one.) Tj\nET\n"
	src := testpdf.New().AddPage(612, 792, body).Build()

	xhtml := chapterXHTML(t, src, defaultOpts())
	if strings.Contains(xhtml, "<strong>") {
		t.Errorf("a wholly bold block was wrapped in strong:\n%s", xhtml)
	}
}

func TestStyleMinLettersIsTunable(t *testing.T) {
	// Spec principle 5. The default of 2 exists because a single italic
	// letter is a variable or a symbol, not emphasis.
	opts := defaultOpts()
	if opts.Heuristics.StyleMinLetters != 2 {
		t.Fatalf("StyleMinLetters default = %d, want 2", opts.Heuristics.StyleMinLetters)
	}
	opts.Heuristics.StyleMinLetters = 100

	xhtml := chapterXHTML(t, emphasisDoc(), opts)
	if strings.Contains(xhtml, "<strong>") || strings.Contains(xhtml, "<em>") {
		t.Errorf("raising StyleMinLetters did not suppress short runs:\n%s", xhtml)
	}
}

func TestEmphasisSurvivesSerialization(t *testing.T) {
	// The markup must not cost text: spec 10's conservation property applies
	// to emphasised words like any other.
	conv, err := decant.New(defaultOpts())
	if err != nil {
		t.Fatal(err)
	}
	src := emphasisDoc()
	doc := analyze(t, src, defaultOpts())

	var out bytes.Buffer
	if _, err := conv.Write(contextTODO(), doc, &out); err != nil {
		t.Fatal(err)
	}
	xhtml := allChapterText(t, out.Bytes())
	for _, want := range []string{"emphasised", "words", "stands", "out"} {
		if !strings.Contains(xhtml, want) {
			t.Errorf("%q did not survive:\n%s", want, xhtml)
		}
	}
}
