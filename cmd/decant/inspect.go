package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sroberts/decant"
)

// cmdProbe dumps the intermediate model, per spec principle 5.
func cmdProbe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: decant probe <input.pdf> [--stage=glyphs|lines|blocks|structure] [--page=N] [--json]\n\n")
		fs.PrintDefaults()
	}

	var (
		stage    = fs.String("stage", "blocks", "pipeline stage: glyphs, lines, blocks, structure")
		page     = fs.Int("page", 0, "one-based page to probe; 0 probes every page")
		asJSON   = fs.Bool("json", false, "emit JSON on stdout")
		pageList = fs.String("pages", "", "page range, e.g. 5-20 (ignored when --page is set)")
	)
	positional, err := parseArgs(fs, args)
	if err != nil {
		return usageError(err)
	}
	if len(positional) != 1 {
		fs.Usage()
		return &decant.UsageError{
			Err: fmt.Errorf("expected exactly one input file, got %d", len(positional)),
		}
	}

	opts := decant.DefaultOptions()
	if *pageList != "" {
		pr, err := decant.ParsePageRange(*pageList)
		if err != nil {
			return &decant.UsageError{Err: err}
		}
		opts.Pages = pr
	}
	conv, err := decant.New(opts)
	if err != nil {
		return err
	}

	in, size, err := openInput(positional[0])
	if err != nil {
		return err
	}
	defer in.Close()

	// The flag is one-based for humans; the library is zero-based, with -1
	// meaning every selected page.
	target := -1
	if *page > 0 {
		target = *page - 1
	}

	res, err := conv.Probe(ctx, in, size, decant.ProbeStage(*stage), target)
	if err != nil {
		return err
	}

	if *asJSON {
		return writeJSON(stdout, res)
	}
	printProbe(stdout, res)
	return nil
}

func printProbe(w io.Writer, res *decant.ProbeResult) {
	for _, note := range res.Notes {
		fmt.Fprintf(w, "note: %s\n", note)
	}
	for _, p := range res.Pages {
		fmt.Fprintf(w, "\n== page %d (%.0f x %.0f pt", p.Page+1, p.Width, p.Height)
		if p.Rotate != 0 {
			fmt.Fprintf(w, ", rotated %d", p.Rotate)
		}
		fmt.Fprintln(w, ")")

		switch res.Stage {
		case decant.StageGlyphs:
			fmt.Fprintf(w, "%d glyphs\n", len(p.Glyphs))
			for i, g := range p.Glyphs {
				flag := ""
				if g.Missing {
					flag = " MISSING"
				}
				fmt.Fprintf(w, "  %4d  %-4q x=%8.2f y=%8.2f adv=%6.2f size=%5.2f %s%s\n",
					i, g.Rune, g.X, g.Y, g.Advance, g.Size, g.Font, flag)
			}

		case decant.StageLines:
			fmt.Fprintf(w, "%d lines\n", len(p.Lines))
			for i, l := range p.Lines {
				fmt.Fprintf(w, "  %4d  y=%8.2f x=[%7.2f %7.2f] size=%5.2f %-20s %s\n",
					i, l.Baseline, l.Bounds.MinX, l.Bounds.MaxX, l.Size,
					truncate(l.Font, 20), truncate(l.Text, 90))
			}

		default:
			fmt.Fprintf(w, "%d blocks\n", len(p.Blocks))
			for i, b := range p.Blocks {
				kind := b.Kind
				if kind == "" {
					kind = "-"
				}
				fmt.Fprintf(w, "  block %d  %s  y=[%.2f %.2f] x=[%.2f %.2f]  %d lines\n",
					i, kind, b.Bounds.MinY, b.Bounds.MaxY, b.Bounds.MinX, b.Bounds.MaxX, b.Lines)
				for j, para := range b.Paragraphs {
					fmt.Fprintf(w, "      [%d] %s\n", j, truncate(para, 100))
				}
			}
		}
	}
}

// cmdMeta reports document metadata without running the pipeline.
func cmdMeta(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("meta", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: decant meta <input.pdf> [--json]\n\n")
		fs.PrintDefaults()
	}
	asJSON := fs.Bool("json", false, "emit JSON on stdout")

	positional, err := parseArgs(fs, args)
	if err != nil {
		return usageError(err)
	}
	if len(positional) != 1 {
		fs.Usage()
		return &decant.UsageError{
			Err: fmt.Errorf("expected exactly one input file, got %d", len(positional)),
		}
	}

	in, size, err := openInput(positional[0])
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := decant.Meta(ctx, in, size)
	if err != nil {
		return err
	}
	info.Source = filepath.Base(positional[0])

	if *asJSON {
		return writeJSON(stdout, info)
	}

	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(stdout, "%-12s %s\n", k+":", v)
		}
	}
	row("File", info.Source)
	row("Pages", fmt.Sprintf("%d", info.PageCount))
	row("Title", info.Title)
	row("Author", info.Author)
	row("Subject", info.Subject)
	row("Keywords", info.Keywords)
	row("Creator", info.Creator)
	row("Producer", info.Producer)
	row("Language", info.Language)
	if !info.Created.IsZero() {
		row("Created", info.Created.Format("2006-01-02 15:04:05 MST"))
	}
	if !info.Modified.IsZero() {
		row("Modified", info.Modified.Format("2006-01-02 15:04:05 MST"))
	}
	row("SHA-256", info.Digest)
	row("Identifier", info.Identifier)
	row("Outline", fmt.Sprintf("%d top-level entries", info.OutlineEntries))

	for _, ps := range info.PageSizes {
		rot := ""
		if ps.Rotate != 0 {
			rot = fmt.Sprintf(", rotated %d", ps.Rotate)
		}
		row("Geometry", fmt.Sprintf("%.0f x %.0f pt%s (%d pages)",
			ps.Width, ps.Height, rot, ps.Pages))
	}
	return nil
}

// openInput opens a file and reports its size.
func openInput(path string) (*os.File, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	if st.IsDir() {
		f.Close()
		return nil, 0, &decant.UsageError{Err: fmt.Errorf("%s is a directory", path)}
	}
	return f, st.Size(), nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
