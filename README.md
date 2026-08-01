# decant

Reconstruct semantic, reflowable EPUB 3 from fixed-layout PDF.

PDF stores positioned glyphs. EPUB needs paragraphs, headings, and reading
order. decant is a single static Go binary that recovers the second from the
first, plus an importable library so a TUI can drive the same code path
without shelling out.

Status: **M1**. The conversion engine extracts text and reconstructs
paragraphs end to end, and every output passes `epubcheck` with zero errors.
Structure classification, images, and tables are not implemented yet; see
[Milestones](#milestones).

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
| Images | keep, RGB | 16-level grayscale, JPEG | drop |
| Image max width | 1600 | 480 | n/a |
| Max chunk bytes | 262144 | 65536 | 65536 |
| Table mode | auto | text | text |
| TOC depth | unlimited | 2 | 2 |
| CSS | base | reduced | none |

`crosspoint` targets an Xteink X4 running CrossPoint firmware: a 480x800 E Ink
panel driven by an ESP32-C3 with roughly 380 KB of usable RAM. On that
hardware the XHTML chunk size is the dominant failure mode, not image
fidelity. The 65536 byte default is a working guess anchored to the RAM
figure; no public documentation states a real ceiling.

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
stays small. Treat the API as unstable until `v1.0.0`.

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
- Paragraph reconstruction from indent, leading, and terminal punctuation
- Deterministic EPUB 3.3 output with an EPUB 2 NCX fallback
- Encrypted, scanned, and malformed input detection with distinct exit codes
- `convert`, `probe`, `meta`, and `version` subcommands

## Not implemented yet

`--columns`, `--keep-headers`, `--no-dehyphenate`, and `--table-mode` are
accepted and print a notice naming the milestone that implements them.
`--jobs` is accepted but page processing is currently sequential.

## Milestones

| | Scope | Status |
|---|---|---|
| M1 | Parse, glyph extraction, line assembly, plain paragraphs | done |
| M2 | Block segmentation, column detection, headings, outline TOC, chapter splitting | next |
| M3 | Image extraction, placement, re-encoding, figures and captions | |
| M4 | Furniture removal, dehyphenation, footnotes, lists, blockquotes | |
| M5 | Table detection, device profiles, conversion report, `probe` | partial |
| M6 | Public API stabilization and CrossPoint TUI integration | |

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
| `golang.org/x/image` | BSD-3 | `font/sfnt` metrics and cmap |
| `golang.org/x/text` | BSD-3 | Unicode normalization |

`unidoc/unipdf` (AGPL or paid), `go-fitz` and other MuPDF bindings (cgo plus
AGPL), and `rsc.io/pdf` (no font or positioning support) are all ruled out by
the pure-Go static binary requirement or the license.
