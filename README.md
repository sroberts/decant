# decant

Reconstruct semantic, reflowable EPUB 3 from fixed-layout PDF.

PDF stores positioned glyphs. EPUB needs paragraphs, headings, and reading
order. decant is a single static Go binary that recovers the second from the
first, plus an importable library so a TUI can drive the same code path
without shelling out.

Status: **M5**. Table detection, text extraction, column
detection, paragraph reconstruction, heading classification, outline-driven
chapter splitting, images with figures and captions, furniture removal,
dehyphenation, lists, blockquotes, code blocks, and linked footnotes. Every
output passes `epubcheck` with zero errors. See [Milestones](#milestones).

MIT licensed. Full design in [`spec.md`](spec.md).

## Install

```
go install github.com/sroberts/decant/cmd/decant@latest
```

Or build from a checkout:

```
go build -o decant ./cmd/decant
```

Pure Go, no cgo. Cross-compiles to a static binary for linux/amd64,
linux/arm64, darwin/arm64, and windows/amd64.

## Use

```
decant book.pdf                          # writes book.epub
decant convert book.pdf -o out.epub
decant convert book.pdf --profile=crosspoint
decant convert book.pdf --pages 5-200,210 --report report.json
decant meta book.pdf                     # metadata, no conversion
decant probe book.pdf --stage=lines --page=12
```

`convert` is the default verb. Flags may appear before or after the input
path.

### Device profiles

| Setting | standard | crosspoint | minimal |
|---|---|---|---|
| Images | keep, RGB | 16-level grayscale; JPEG photos, PNG line art | drop |
| Image max width | 1600 | 480 | n/a |
| Max chunk bytes | 262144 | 262144 | 65536 |
| Table mode | auto | text | text |
| TOC depth | unlimited | 2 | 2 |
| CSS | base | reduced | none |

`crosspoint` targets an Xteink X4 running CrossPoint firmware: a 480x800 E Ink
panel driven by an ESP32-C3 with roughly 380 KB of usable RAM.

Chapter size is **not** a memory constraint there, contrary to the obvious
guess. The firmware streams XHTML through expat in 1 KB chunks and serializes
each laid-out page to the SD card as it completes, so a chapter never lands in
RAM; only a 12-byte-per-page lookup table scales with its length, about 0.9%
of the chapter's bytes. The out-of-memory crashes in its release notes were
the CSS parser, now guarded at 128 KB, and decant emits under 1 KB of CSS. The
profile therefore keeps the standard 256 KB chunk.

The same reading settled the image formats. CrossPoint's EPUB path decodes
JPG and **PNG** — including the indexed form — and has no BMP decoder at all,
so line art ships as a paletted PNG where it is smaller and stays sharp, and
only photographs dither to JPEG. See spec section 5.1.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Runtime failure |
| 2 | Usage error |
| 3 | Encrypted PDF (unsupported in v1) |
| 4 | No usable text layer (scanned document) |
| 5 | Converted with warnings and `--strict` was set |
| 6 | Malformed PDF beyond repair |

Diagnostics go to stderr. `--json` output goes to stdout, unmixed, so
`-o -` can stream an EPUB to a pipe.

## Library

```go
conv, err := decant.New(decant.Options{
    Profile:    decant.ProfileCrossPoint,
    Heuristics: decant.DefaultHeuristics(),
})

// Analyze runs stages 1 through 6 and returns the block tree.
doc, err := conv.Analyze(ctx, reader, size)

// Callers may correct structure before committing the EPUB.
doc.Blocks[0].Kind = decant.KindHeading
doc.Blocks[0].Level = 1

report, err := conv.Write(ctx, doc, out)
fmt.Println(report.QualityScore) // 0 to 100, a triage signal
```

`Convert` is `Analyze` followed by `Write`. The split exists so a caller can
preview detected structure and fix it first.

Everything below the root package is under `internal/`, so the public surface
stays small. Full documentation and runnable examples are on
[pkg.go.dev](https://pkg.go.dev/github.com/sroberts/decant).

### API stability

The API is unstable until `v1.0.0`, which is cut at M6 once the CrossPoint
TUI has exercised `Analyze` and `Write` against real files. From `v1.0.0`,
normal Go compatibility applies to the root package, and these behaviours are
part of the contract rather than incidental:

- `Write` does not mutate the `Document` and does not accumulate into the
  `Report`, so it may be called repeatedly on one tree. A TUI writes a
  preview, keeps editing, and writes again.
- A `Converter` holds no mutable state and is reusable across documents.
- Edits to `Document.Blocks` reach the output. Heading levels drive chapter
  splitting and the navigation document.
- Heading levels outside 1 through 6 are clamped, not rejected, because XHTML
  has no other heading elements. A stray edit cannot fail a conversion or
  emit an element that does not exist.
- A document with no blocks is refused rather than written as an empty but
  structurally valid EPUB.
- `New` copies its `Options`; mutating the caller's copy afterwards does not
  affect the converter.

`api_test.go` pins each of these.

What is explicitly **not** covered: the exact structure any given PDF
converts to. Layout reconstruction is heuristic, and heuristics improve.
Block counts, heading levels, and chapter boundaries may change in any
release. Pin thresholds through `Heuristics` if you need stability there, and
watch `testdata/corpus_manifest.json` for how a change moves 34 real
documents.

## Guarantees

**Deterministic output.** Identical input plus identical flags produces
byte-identical EPUB, independent of worker count or wall clock. Anchor IDs
derive from content hashes rather than counters. `dc:identifier` is a UUIDv5
over the input's SHA-256, so reconverting a file yields the same identifier.
ZIP entries sort by name, carry no extra fields, and share one fixed
timestamp taken from `--date`, then `SOURCE_DATE_EPOCH`, then the PDF
`ModDate`.

**No OCR, ever.** Not embedded, not by subprocess. Scanned PDFs are detected
after glyph extraction and exit 4 with the metrics that triggered the
decision, pointing at an external OCR pass.

**Fail loud, degrade gracefully.** Every heuristic that fires records a
diagnostic in the conversion report. `decant probe` dumps the intermediate
model at any stage.

## What works today

- Content stream interpretation with full graphics and text state
- Simple and Type0/CID fonts; `/ToUnicode`, `/Differences`, the standard
  encoding tables, Adobe glyph names, and reverse lookup through an embedded
  font's `cmap`
- Base-14 metrics for documents that reference fonts without embedding them
- Page rotation, form XObject recursion, inline image skipping
- Line assembly with space reconstruction; ligature, soft hyphen, and NFC
  normalization
- Column detection from a per-row projection profile, with correct reading
  order and full-width headings preserved across the gutter
- Bottom-up block segmentation using horizontal overlap and running leading
- Paragraph reconstruction from indent, leading, and terminal punctuation
- Heading classification against a document-wide body font, ranked to h1-h6
- PDF outline reconciliation, hierarchical TOC, chapter splitting at headings
- Image extraction with placement from the CTM, deduplication by pixel
  digest, Catmull-Rom scaling, JPEG/paletted-PNG selection by colour count,
  and DCT passthrough when nothing needs the pixels
- Background and watermark rejection, size floors, figures placed in reading
  order, caption binding, and grayscale dithering for the crosspoint profile
- Running head and folio removal by repeated text and repeated position
- Dehyphenation by inverted Liang pattern matching in eight languages
- Ordered and unordered lists with inferred start, blockquotes, code blocks
- Table detection from ruling lines and column alignment, with colspan
- Inline bold and italic as `<strong>` and `<em>`, with TeX math italic
  excluded so a formula's variables do not become emphasis
- Internal cross-references rewritten from PDF `/Link` annotations to `href`
  fragments, anchoring only the blocks something points at
- Superscript detection and footnotes linked with `epub:type` noteref
- Deterministic EPUB 3.3 output with an EPUB 2 NCX fallback
- Encrypted, scanned, and malformed input detection with distinct exit codes
- `convert`, `probe`, `meta`, and `version` subcommands

## Not implemented yet

`--table-mode=image` has been **removed**. It needed the vector renderer that
spec §13.1 leaves for after v1, so it only ever degraded to text with a
warning; shipping it would have frozen a mode that silently does something
else into the v1 API. Asking for it is now a usage error.

`--jobs` is **reserved**: it is accepted, prints a notice, and does nothing.
Page processing is sequential and stays that way. Stages 1 and 2 run inside
pdfcpu, which mutates its cross-reference table on every dereference with no
lock, and they are two thirds of per-page time; the rest is about 4% of a
conversion. `Options.Jobs` was removed from the library rather than shipped
as a permanent no-op. See spec §4.

Hyphenation language comes from `--language`, then the PDF's `/Lang`, then
XMP `dc:language`, then English.

Dehyphenation ships patterns for English, German, Spanish, French, Italian,
Dutch, Polish, and Portuguese. Russian and Swedish are **deliberately
absent**: their `hyph-utf8` files are LPPL-only, and spec §4.6 says to drop
the language rather than take on a share-alike or renaming condition. Those
documents convert normally with dehyphenation disabled and a diagnostic.
See [`THIRD_PARTY.md`](THIRD_PARTY.md).

Right-to-left and vertical CJK documents **convert, with a warning**. The
text is extracted correctly, but decant emits it in logical order: it does not
run the bidirectional algorithm and does not set vertical columns, so lines
may read in the wrong direction. The report carries `rtl_letter_ratio` and
`vertical_text_pages`, and the warning fires once the document is
substantially right-to-left rather than on a single quoted phrase.

JPEG 2000 and JBIG2 images drop with a diagnostic: neither has a pure-Go
decoder, and spec principle 2 rules out cgo. Inline (`BI`) images have their
position recorded for scan detection but are not extracted.

Vector artwork is not rendered, so a chart drawn as paths is lost. It is
reported rather than dropped silently: the conversion report counts painted
paths per page and warns when a page carries enough of them to be a diagram.
Rasterization was **considered and declined** for v1 (spec §13, closed
2026-08-03). §1 permits either rasterizing or dropping, and dropping is what
decant does.

In practice that means a **diagram-heavy academic PDF loses its figures**.
On the sample corpus the warning fires on 39 of one mathematics textbook's
117 pages and on nothing else, so the exposure is narrow, but if your library
is mostly papers with plotted charts, expect to lose them and to be told so.
The text around them converts normally.

Table detection still over-fires on mathematical typesetting. On the corpus's
LaTeX textbook it reports eight medium-confidence tables that are really
plotted axes and matrix-like displays; `--table-mode=auto` renders those as
space-preserved text rather than as `<table>`, so no false table markup
reaches the reader, but the layout is still wrong. `--table-mode=drop` turns
detection off entirely and leaves the text as paragraphs. Three guards
already narrow this — a fill ratio, a rejection of grids whose cells hold one
character each, and a rule that a table may not straddle the page's own
columns — and further tuning needs a corpus with more real tables in it than
this one has.

## Milestones

| | Scope | Status |
|---|---|---|
| M1 | Parse, glyph extraction, line assembly, plain paragraphs | done |
| M2 | Block segmentation, column detection, headings, outline TOC, chapter splitting | done |
| M3 | Image extraction, placement, re-encoding, figures and captions | done |
| M4 | Furniture removal, dehyphenation, footnotes, lists, blockquotes | done |
| M5 | Table detection, device profiles, conversion report, `probe` | done |
| M6 | Public API stabilization, content fidelity, remaining spec gaps | done |

M1 through M3 ship before anything gets optimized. Layout heuristics need
real-corpus feedback; tuning thresholds against three test files produces
overfitted garbage.

## Development

```
go test ./...                  # unit and integration tests
go test -race ./...            # CI runs this
go vet ./... && staticcheck ./...
```

`epubcheck` on `PATH` enables validation tests against generated output; they
skip without it. CI installs it and enforces zero errors as a merge gate.

### Real-world corpus

```
make corpus        # fetch py-pdf/sample-files, pinned to a commit
make corpus-test   # run the corpus tests
make manifest      # regenerate the golden, then review the diff
```

The corpus is [py-pdf/sample-files](https://github.com/py-pdf/sample-files):
34 PDFs from pdfTeX, LibreOffice, Google Docs, ReportLab, PDFKit, and others,
including a 117-page LaTeX book, a two-column paper, Arabic text, an
encrypted file, and a damaged one. It is **not vendored** — those files are
CC-BY-SA-4.0 and decant ships MIT — so it is fetched on demand and the tests
skip without it.

`testdata/corpus_manifest.json` records what decant produces for each file:
outcome, block and heading counts, columns detected, a decode-failure bucket,
and structure and text digests. It is the regression gate, and the tool for
judging whether a heuristic change helps or hurts across real documents
rather than across three fixtures.

Current state: 27 files convert (25 with zero decode failures), 5 are
correctly rejected as image-only with no text layer, 1 is correctly rejected
as encrypted, and 1 damaged file is not yet recoverable.

Fuzz targets cover the content stream lexer, the interpreter, the CMap
parser, and the xref parser:

```
go test -run XXX -fuzz FuzzLexer -fuzztime 60s ./internal/pdf/
```

Run a single test:

```
go test -run TestDeterministicOutput ./...
```

## Dependencies

| Package | License | Role |
|---|---|---|
| `github.com/pdfcpu/pdfcpu` | Apache-2.0 | xref parsing, object model |
| `golang.org/x/image` | BSD-3 | `font/sfnt` metrics, Catmull-Rom resampling |
| `golang.org/x/text` | BSD-3 | Unicode normalization |
| `github.com/hhrutter/tiff` | BSD-3 | CMYK TIFF decode, which x/image rejects |

Hyphenation patterns are vendored from
[hyph-utf8](https://github.com/hyphenation/tex-hyphen) under MIT, BSD, or
unrestricted terms only; [`THIRD_PARTY.md`](THIRD_PARTY.md) records the
per-file audit.

`unidoc/unipdf` (AGPL or paid), `go-fitz` and other MuPDF bindings (cgo plus
AGPL), and `rsc.io/pdf` (no font or positioning support) are all ruled out by
the pure-Go static binary requirement or the license.
