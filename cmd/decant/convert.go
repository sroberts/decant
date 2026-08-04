package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sroberts/decant"
)

func cmdConvert(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: decant convert <input.pdf> [-o output.epub] [flags]\n\n")
		fs.PrintDefaults()
	}

	def := decant.DefaultOptions()

	var (
		output        = fs.String("o", "", "output path; \"-\" writes to stdout (default: input basename + .epub)")
		outputLong    = fs.String("output", "", "alias for -o")
		profile       = fs.String("profile", string(def.Profile), "device profile: standard, crosspoint, minimal")
		title         = fs.String("title", "", "override the Dublin Core title")
		author        = fs.String("author", "", "override the Dublin Core creator")
		language      = fs.String("language", "", "override the Dublin Core language")
		pages         = fs.String("pages", "", "page range, e.g. 5-200,210 (default: all)")
		splitAt       = fs.String("split-at", string(def.SplitAt), "chapter boundary: h1, h2, page, none")
		maxChunk      = fs.Int("max-chunk-bytes", def.MaxChunkBytes, "force split of oversized XHTML at a paragraph boundary")
		columns       = fs.String("columns", "auto", "column count: auto, 1, 2, 3")
		keepHeaders   = fs.Bool("keep-headers", false, "retain running heads and folios")
		noDehyphenate = fs.Bool("no-dehyphenate", false, "preserve line-break hyphens verbatim")
		profileFile   = fs.String("profile-file", "", "path to a JSON device profile; see \"decant profile\"")
		tableMode     = fs.String("table-mode", "auto", "table handling: auto, html, text, drop")
		imageMaxWidth = fs.Int("image-max-width", def.ImageMaxWidth, "longest image edge in pixels; 0 disables scaling")
		images        = fs.String("images", string(def.Images), "image handling: keep, grayscale, drop")
		keepSmall     = fs.Bool("keep-small-images", false, "retain images the size rules would drop")
		reportPath    = fs.String("report", "", "write a JSON conversion report to this path")
		strict        = fs.Bool("strict", false, "exit non-zero when any quality threshold is breached")
		jobs          = fs.Int("jobs", runtime.NumCPU(), "reserved; page processing is currently sequential")
		date          = fs.String("date", "", "fixed RFC 3339 timestamp for reproducible builds")
		quiet         = fs.Bool("quiet", false, "suppress the summary on stderr")
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
	inPath := positional[0]

	// Which flags the user actually passed. Device profiles override defaults
	// but must not override an explicit flag.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	// Features accepted for CLI compatibility with the spec surface but not
	// yet implemented. Refusing silently would be worse than saying so.

	// --jobs is accepted for compatibility with the documented flag set but
	// does nothing. Spec 4 assumed stages 2 through 6 would parallelize per
	// page; stage 2 is glyph extraction, which runs inside pdfcpu, and
	// pdfcpu's xref table is mutated on every dereference with no lock. Only
	// stage 3 through 6 could move, and measurement puts that at about 4% of
	// a conversion. Principle 3 says to say so rather than silently ignore
	// the flag.
	if set["jobs"] && *jobs != 1 {
		fmt.Fprintln(stderr,
			"decant: note: --jobs is reserved; page processing is currently sequential")
	}

	nColumns, err := parseColumns(*columns)
	if err != nil {
		return &decant.UsageError{Err: err}
	}

	// A profile document is parsed here and applied inside
	// applyProfileRespectingFlags, which owns the precedence order.
	var profileDoc *decant.ProfileDoc
	if *profileFile != "" {
		f, err := os.Open(*profileFile)
		if err != nil {
			return &decant.UsageError{Err: err}
		}
		profileDoc, err = decant.LoadProfileDoc(f)
		f.Close()
		if err != nil {
			return &decant.UsageError{Err: err}
		}
	}

	opts := def
	opts.Profile = decant.Profile(*profile)
	if profileDoc != nil && !set["profile"] && profileDoc.Base != "" {
		opts.Profile = profileDoc.Base
	}
	opts.SplitAt = decant.SplitMode(*splitAt)
	opts.Images = decant.ImageMode(*images)
	opts.MaxChunkBytes = *maxChunk
	opts.ImageMaxWidth = *imageMaxWidth
	opts.NoDehyphenate = *noDehyphenate
	opts.KeepHeaders = *keepHeaders
	opts.Strict = *strict
	opts.Columns = nColumns
	opts.KeepSmallImages = *keepSmall
	opts.Tables = decant.TableMode(*tableMode)
	opts.Metadata = decant.Metadata{
		Title: *title, Author: *author, Language: *language,
	}

	if *pages != "" {
		pr, err := decant.ParsePageRange(*pages)
		if err != nil {
			return &decant.UsageError{Err: err}
		}
		opts.Pages = pr
	}

	when, err := resolveDate(*date)
	if err != nil {
		return &decant.UsageError{Err: err}
	}
	opts.Deterministic = when

	// Apply profile defaults, then restore anything the user set explicitly.
	if err := applyProfileRespectingFlags(&opts, profileDoc, set,
		*maxChunk, *imageMaxWidth, decant.ImageMode(*images)); err != nil {
		return &decant.UsageError{Err: err}
	}

	conv, err := decant.New(opts)
	if err != nil {
		return err
	}

	in, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer in.Close()

	st, err := in.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return &decant.UsageError{Err: fmt.Errorf("%s is a directory", inPath)}
	}

	outPath := firstNonEmpty(*output, *outputLong)
	if outPath == "" {
		outPath = strings.TrimSuffix(inPath, filepath.Ext(inPath)) + ".epub"
	}

	doc, err := conv.Analyze(ctx, in, st.Size())
	if err != nil {
		return err
	}
	doc.Source = filepath.Base(inPath)

	rep, err := writeOutput(ctx, conv, doc, outPath)
	if err != nil {
		return err
	}

	if *reportPath != "" {
		if err := writeReport(*reportPath, rep); err != nil {
			return fmt.Errorf("writing report: %w", err)
		}
	}

	if !*quiet {
		summarize(stderr, rep, outPath)
	}
	if *strict && rep.Warnings() > 0 {
		return &strictError{warnings: rep.Warnings(), score: rep.QualityScore}
	}
	return nil
}

// writeOutput writes the EPUB, using a temporary file and a rename so a
// failed conversion never leaves a partial .epub behind.
func writeOutput(ctx context.Context, conv *decant.Converter, doc *decant.Document, outPath string) (*decant.Report, error) {
	if outPath == "-" {
		return conv.Write(ctx, doc, os.Stdout)
	}

	dir := filepath.Dir(outPath)
	tmp, err := os.CreateTemp(dir, ".decant-*.epub")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	rep, err := conv.Write(ctx, doc, tmp)
	if err != nil {
		return rep, err
	}
	if err := tmp.Close(); err != nil {
		return rep, err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return rep, err
	}
	if err := os.Rename(tmpName, outPath); err != nil {
		return rep, err
	}
	return rep, nil
}

func writeReport(path string, rep *decant.Report) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

// summarize prints the one-screen result to stderr, leaving stdout clean for
// --json output and "-o -".
func summarize(w io.Writer, rep *decant.Report, outPath string) {
	dest := outPath
	if dest == "-" {
		dest = "stdout"
	}
	fmt.Fprintf(w, "decant: wrote %s (%d pages, %d chapters, %s)\n",
		dest, rep.PagesConverted, rep.Chapters, humanBytes(rep.OutputBytes))
	if rep.ImagesPlaced > 0 {
		fmt.Fprintf(w, "        %d image(s), %s\n",
			rep.ImagesPlaced, humanBytes(int64(rep.ImageBytes)))
	}
	if h := rep.Hyphenation; h.Dropped > 0 || h.Kept > 0 {
		fmt.Fprintf(w, "        dehyphenation [%s]: %d joined, %d kept\n",
			h.Language, h.Dropped, h.Kept)
	}
	if rep.FurnitureRemoved > 0 {
		fmt.Fprintf(w, "        removed %d running head(s) and page number(s)\n",
			rep.FurnitureRemoved)
	}
	if n := len(rep.Tables); n > 0 {
		total := 0
		for _, v := range rep.Tables {
			total += v
		}
		fmt.Fprintf(w, "        %d table(s) detected\n", total)
	}
	if rep.VectorPagesDropped > 0 {
		fmt.Fprintf(w, "        %d page(s) carry vector artwork that was not rendered\n",
			rep.VectorPagesDropped)
	}
	fmt.Fprintf(w, "        quality score %d/100", rep.QualityScore)
	if n := rep.Warnings(); n > 0 {
		fmt.Fprintf(w, ", %d warning(s)", n)
	}
	if r := rep.DecodeFailureRate(); r > 0.001 {
		fmt.Fprintf(w, ", %.2f%% glyph decode failures", r*100)
	}
	fmt.Fprintln(w)

	for _, d := range rep.Diagnostics {
		if d.Severity != decant.SeverityWarning {
			continue
		}
		where := "document"
		if d.Page >= 0 {
			where = fmt.Sprintf("page %d", d.Page+1)
		}
		fmt.Fprintf(w, "        warning [%s, %s]: %s\n", d.Stage, where, d.Message)
	}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// resolveDate resolves the output timestamp: --date first, then
// SOURCE_DATE_EPOCH, then zero, which lets the library fall back to the PDF
// ModDate.
func resolveDate(flagValue string) (time.Time, error) {
	if flagValue != "" {
		t, err := time.Parse(time.RFC3339, flagValue)
		if err != nil {
			return time.Time{}, fmt.Errorf("--date must be RFC 3339, e.g. 2026-08-01T00:00:00Z: %w", err)
		}
		return t.UTC(), nil
	}
	if v := os.Getenv("SOURCE_DATE_EPOCH"); v != "" {
		secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("SOURCE_DATE_EPOCH must be a Unix timestamp: %w", err)
		}
		return time.Unix(secs, 0).UTC(), nil
	}
	return time.Time{}, nil
}

// applyProfileRespectingFlags applies the device profile defaults from spec
// section 5, then restores any value the user set explicitly. An explicit
// flag always beats a profile default.
func applyProfileRespectingFlags(
	opts *decant.Options,
	doc *decant.ProfileDoc,
	set map[string]bool,
	chunk, imgWidth int,
	images decant.ImageMode,
) error {
	opts.ApplyProfileDefaults()

	// The document layers over the built-in defaults and under the flags, so
	// a shared profile can be adopted wholesale and still overridden one
	// setting at a time from the command line.
	if doc != nil {
		if err := opts.ApplyProfileDoc(doc); err != nil {
			return err
		}
	}

	if set["max-chunk-bytes"] {
		opts.MaxChunkBytes = chunk
	}
	if set["image-max-width"] {
		opts.ImageMaxWidth = imgWidth
	}
	if set["images"] {
		opts.Images = images
	}
	return nil
}

// parseColumns converts the --columns flag to the option value, where zero
// means detect from the page's projection profile.
func parseColumns(v string) (int, error) {
	switch strings.TrimSpace(v) {
	case "", "auto":
		return 0, nil
	case "1":
		return 1, nil
	case "2":
		return 2, nil
	case "3":
		return 3, nil
	}
	return 0, fmt.Errorf("--columns must be auto, 1, 2, or 3, got %q", v)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
