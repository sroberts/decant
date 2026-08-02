package decant_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/sroberts/decant"
)

// openPDF is the boilerplate every example needs: decant reads through
// io.ReaderAt and needs the size, so a caller passes both.
func openPDF(path string) (*os.File, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

// Convert a PDF to EPUB with the default settings.
func Example() {
	in, size, err := openPDF("book.pdf")
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	out, err := os.Create("book.epub")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	conv, err := decant.New(decant.DefaultOptions())
	if err != nil {
		log.Fatal(err)
	}

	rep, err := conv.Convert(context.Background(), in, size, out)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d chapters, quality %d/100\n", rep.Chapters, rep.QualityScore)
}

// Correct the inferred structure before writing.
//
// This is why Analyze and Write are separate. Headings drive chapter
// splitting and the navigation document, so promoting a block to a level-1
// heading starts a new chapter in the output.
func ExampleConverter_Analyze() {
	in, size, err := openPDF("book.pdf")
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	conv, err := decant.New(decant.DefaultOptions())
	if err != nil {
		log.Fatal(err)
	}

	doc, err := conv.Analyze(context.Background(), in, size)
	if err != nil {
		log.Fatal(err)
	}

	// An epigraph set in a large face is a classic false heading. Demote it,
	// and promote an appendix the classifier read as body text.
	for i := range doc.Blocks {
		switch doc.Blocks[i].Text {
		case "It was a bright cold day in April":
			doc.Blocks[i].Kind = decant.KindParagraph
			doc.Blocks[i].Level = 0
		case "Appendix A":
			doc.Blocks[i].Kind = decant.KindHeading
			doc.Blocks[i].Level = 1
		}
	}

	out, err := os.Create("book.epub")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	if _, err := conv.Write(context.Background(), doc, out); err != nil {
		log.Fatal(err)
	}
}

// Distinguish the four failure modes.
//
// Each maps to a distinct exit code at the command layer. Everything short of
// these degrades gracefully and lands in the report as a diagnostic instead.
func ExampleConverter_Convert_errors() {
	in, size, err := openPDF("book.pdf")
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	conv, err := decant.New(decant.DefaultOptions())
	if err != nil {
		log.Fatal(err)
	}

	_, err = conv.Convert(context.Background(), in, size, os.Stdout)

	var encrypted *decant.EncryptedError
	var noText *decant.NoTextLayerError
	var malformed *decant.MalformedError
	switch {
	case errors.As(err, &encrypted):
		fmt.Println("password-protected; decant does not decrypt")
	case errors.As(err, &noText):
		fmt.Println("scanned; decant does not OCR")
	case errors.As(err, &malformed):
		fmt.Println("damaged beyond repair")
	case err != nil:
		log.Fatal(err)
	}
}

// Read metadata without converting.
//
// Meta parses only the trailer and page tree, so it is cheap enough to run
// across a whole library to build an index.
func ExampleMeta() {
	in, size, err := openPDF("book.pdf")
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	info, err := decant.Meta(context.Background(), in, size)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s by %s, %d pages\n", info.Title, info.Author, info.PageCount)
}

// Tune a heuristic.
//
// Every threshold decant infers structure from is exposed and documented.
// This one raises how far a block must exceed the body font to read as a
// heading, from the default 15% to 30%, which suppresses false headings in a
// document set slightly large throughout.
func ExampleHeuristics() {
	opts := decant.DefaultOptions()
	opts.Heuristics = decant.DefaultHeuristics()
	opts.Heuristics.HeadingSizeRatio = 0.30

	conv, err := decant.New(opts)
	if err != nil {
		log.Fatal(err)
	}
	_ = conv
}

// Target a constrained reading device.
//
// A profile sets image handling, chunk size, and table mode together.
// ApplyProfileDefaults overwrites those fields unconditionally, so set it
// before any value you want to keep.
func ExampleOptions_ApplyProfileDefaults() {
	opts := decant.DefaultOptions()
	opts.Profile = decant.ProfileCrossPoint
	opts.ApplyProfileDefaults()

	// Overrides go after, or the profile wins.
	opts.ImageMaxWidth = 800

	conv, err := decant.New(opts)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(conv.Options().Tables)
}

// Inspect the intermediate model at one pipeline stage.
//
// Probe is what makes the heuristics auditable: it dumps what decant saw at
// the point a decision was made, for one page or the whole document.
func ExampleConverter_Probe() {
	in, size, err := openPDF("book.pdf")
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	conv, err := decant.New(decant.DefaultOptions())
	if err != nil {
		log.Fatal(err)
	}

	res, err := conv.Probe(context.Background(), in, size, decant.StageLines, 12)
	if err != nil {
		log.Fatal(err)
	}
	for _, p := range res.Pages {
		for _, l := range p.Lines {
			fmt.Printf("%.1f  %s\n", l.Baseline, l.Text)
		}
	}
}

// Act on what the conversion reported.
//
// A warning does not mean failure: the EPUB is still valid. It means a
// heuristic fired somewhere worth reviewing.
func ExampleReport() {
	in, size, err := openPDF("book.pdf")
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	conv, err := decant.New(decant.DefaultOptions())
	if err != nil {
		log.Fatal(err)
	}

	out, err := os.Create("book.epub")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	rep, err := conv.Convert(context.Background(), in, size, out)
	if err != nil {
		log.Fatal(err)
	}

	if rep.QualityScore < 80 {
		fmt.Printf("quality %d/100, worth reviewing\n", rep.QualityScore)
	}
	for _, d := range rep.Diagnostics {
		if d.Severity == decant.SeverityWarning {
			fmt.Printf("%s: %s\n", d.Stage, d.Message)
		}
	}
}
