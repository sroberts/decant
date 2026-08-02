// Package decant converts fixed-layout, text-layer PDF into semantic,
// reflowable EPUB 3.
//
// PDF stores positioned glyphs; EPUB needs paragraphs, headings, and reading
// order. Recovering the second from the first is the whole problem, and it is
// heuristic: decant infers structure that the source file does not record.
// Every inference it makes is tunable through [Heuristics], inspectable
// through [Converter.Probe], and reported through [Report].
//
// # The pipeline
//
// Conversion runs in eight stages:
//
//	parse → glyphs → lines → blocks → furniture → classify → assemble → serialize
//
// Stages 1 through 6 produce a [Document], an editable block tree. Stage 7 and
// 8 turn that tree into an EPUB container. The split is deliberate and is the
// main reason this is a library rather than only a command: a caller can
// inspect and correct the inferred structure before committing to output.
//
// # Basic use
//
// [Converter.Convert] runs the whole pipeline:
//
//	conv, err := decant.New(decant.DefaultOptions())
//	if err != nil {
//		return err
//	}
//	rep, err := conv.Convert(ctx, in, size, out)
//
// A Converter holds no mutable state, so one instance is safe to reuse across
// documents and across goroutines.
//
// # Correcting structure before writing
//
// To review or edit what was inferred, call [Converter.Analyze], modify the
// returned [Document], then call [Converter.Write]:
//
//	doc, err := conv.Analyze(ctx, in, size)
//	if err != nil {
//		return err
//	}
//	for i := range doc.Blocks {
//		if doc.Blocks[i].Text == "Appendix A" {
//			doc.Blocks[i].Kind = decant.KindHeading
//			doc.Blocks[i].Level = 1
//		}
//	}
//	rep, err := conv.Write(ctx, doc, out)
//
// Edits reach the output: heading levels drive chapter splitting and the
// navigation document, so promoting a paragraph to a level-1 heading starts a
// new chapter. Levels outside 1 through 6 are clamped rather than rejected,
// because XHTML has no other heading elements. Write does not mutate the
// Document, so it may be called more than once on the same tree.
//
// # Determinism
//
// Identical input and options produce byte-identical output, regardless of
// [Options.Jobs]. Anchor IDs are content hashes rather than counters, the
// package identifier is a UUIDv5 over the input's SHA-256, and ZIP entries
// carry a fixed timestamp taken from [Options.Deterministic], the PDF
// ModDate, or the Unix epoch, in that order. Reconverting a file therefore
// yields the same bytes, which is what makes output diffable and cacheable.
//
// # Failure
//
// decant fails loudly rather than emitting silently corrupt EPUB. Four
// conditions are reported as typed errors, each of which the command maps to
// a distinct exit code:
//
//   - [EncryptedError], for a PDF carrying an /Encrypt dictionary
//   - [NoTextLayerError], for a scan. decant does not OCR, ever
//   - [MalformedError], for damage beyond what xref reconstruction recovers
//   - [UsageError], for invalid options
//
// Everything short of those degrades gracefully and records a [Diagnostic] in
// the [Report] instead. A conversion that emitted warnings still produced a
// valid EPUB; [Report.QualityScore] summarizes how much to trust it.
//
// # Stability
//
// Every package below this one is internal, so the surface here is the whole
// supported API. See the repository README for what the v1 compatibility
// promise does and does not cover.
package decant
