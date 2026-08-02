# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

decant converts fixed-layout, text-layer PDFs into semantic, reflowable EPUB 3. Module `github.com/sroberts/decant`, MIT.

`spec.md` is the authoritative design document — read the relevant section before changing a stage, and update §13 (open/closed decisions, with dates) when a design decision changes. Code comments reference spec sections by number; keep those references accurate when you move logic.

Currently at **M5** complete: tables, profiles, report, and `probe`. M6 (API stabilization and TUI integration) is next.

## Commands

```
go build ./... && go vet ./...
go test ./...                          # full suite
go test -race ./...                    # CI gate
staticcheck ./...                      # CI gate
gofmt -l .                             # CI fails on any output

go test -run TestDeterministicOutput ./...          # single test
go test -run TestAssembleLines ./internal/layout/   # single package
go test -run TestEPUBCheck -v ./...                 # epubcheck validation

go test -run XXX -fuzz FuzzLexer     -fuzztime 60s ./internal/pdf/
go test -run XXX -fuzz FuzzInterpret -fuzztime 60s ./internal/pdf/
go test -run XXX -fuzz FuzzOpen      -fuzztime 60s ./internal/pdf/
go test -run XXX -fuzz FuzzParseCMap -fuzztime 60s ./internal/pdf/

go run ./cmd/decant convert book.pdf -o book.epub
go run ./cmd/decant probe book.pdf --stage=lines --page=12
go run ./cmd/decant meta book.pdf --json
```

`epubcheck` (brew/apt) must be on `PATH` for the validation tests to run; they skip silently without it, and CI enforces them. staticcheck must be recent enough for the local Go version — reinstall with `go install honnef.co/go/tools/cmd/staticcheck@latest` if it reports "invalid Go version".

## Architecture

```
*.go                 package decant — public engine (Options, Converter, Document, Report)
  classify.go        body font, heading classification, outline reconciliation
  render.go          blocks to XHTML, chapter splitting, TOC construction
internal/pdf/        content stream lexer + interpreter, font machinery, doc/xref access
internal/layout/     column detection, line assembly, block segmentation, figures
internal/images/     decode, scale, dither, re-encode extracted images
internal/hyphen/     Liang pattern matching + embedded hyph-utf8 patterns
internal/epub/       deterministic EPUB 3.3 serialization
internal/testpdf/    synthetic PDF builder for tests
cmd/decant/          CLI, thin wrapper over the root package
```

Pipeline: `parse → glyphs → lines → blocks → furniture → classify → assemble → serialize`. Roughly 70% of the work is in content stream interpretation (`internal/pdf/content.go`), block segmentation, and structure classification.

**Stage split matters.** `layout.AnalyzePage` runs stages 3–5 per page. Classification (stage 6) runs *after every page* in `classify.go`, because the body font is a document-wide glyph-count-weighted mode — computing it per page would make a chapter opening page classify its own headings as body text. `Analyze` therefore builds `doc.Blocks` and a parallel `[]blockFeatures` during the page loop, then calls `reconcileOutline` and `classify` once at the end.

**Column detection runs before line assembly is final.** Baseline clustering can merge two columns into one line, so `SplitLinesAtGutters` cuts lines wherever a real inter-glyph gap contains a gutter midpoint. A full-width heading is continuous text across the gutter with no gap there, so it survives intact and `OrderLines` treats it as a band barrier. That is what keeps spanning titles from being sliced in half.

`Analyze` (stages 1–6, returns a mutable `*Document` block tree) is deliberately separate from `Write` (serialization) so the CrossPoint TUI can let a user correct heading levels before committing. Keep that split intact.

### Boundaries worth knowing

- **Public API must not leak internal types.** `Heuristics` is defined in the root package and projected onto `layout.Config` by `layoutConfig()` in `decant.go`. When you add a threshold, add it in both places plus `layout.DefaultConfig()`. The duplication is intentional.
- **`FontID` is a `uint16` index, not a `FontRef`.** `spec.md` §4.2 sketches it as a struct; it is an index because `Glyph` is the dominant memory consumer (~5,000/page, ~56 bytes each against §9's 60-byte budget). Resolve through `PageContent.Fonts`.
- **Page space runs y-down** from the top-left of the crop box, with `/Rotate` already applied by `baseCTM`. PDF user space is y-up. `OutlineItem.Y` is the one exception — it is still in user space and must be converted by the consumer.
- **pdfcpu panics on hostile input.** `internal/pdf/doc.go` wraps every entry point in `recoverMalformed`, converting panics to `ErrMalformed`. `FuzzOpen` found a nil deref in `EnsurePageCount` within seconds. Do not remove those recovers, and add one to any new pdfcpu entry point.
- **Never re-sort blocks after stage 4.** Their order already encodes the column reading order. M3 briefly sorted paragraphs and figures together by vertical position, which interleaved the columns of every two-column page; `figureInsertIndex` now inserts figures into the existing sequence instead. `TestFigureDoesNotDisturbColumnOrder` guards it.
- **pdfcpu's `Dereference` matches `types.IndirectRef` by value.** Passing the `*IndirectRef` that `NewIndirectRef` returns hands it straight back unresolved, which looks like success and fails much later. Dereference `*types.NewIndirectRef(n, 0)`.
- **Use `pdfcpu.ExtractImage`, not `RenderImage`.** The latter assumes filter-pipeline preparation has already happened and yields an empty reader on Flate-encoded images.
- **Never hold a pointer into a slice you are still appending to.** `buildNav` in `render.go` builds its tree from individually allocated nodes for exactly this reason: a reallocation would silently strand every child added through a stale pointer.
- **Four heuristics are deliberately not in the spec**, all guards against the spec's rule misfiring, all tunable in `Heuristics`. `ColumnMinRows` (8) and `ColumnMinLines` (3) reject a column split the page has too little evidence for — asking whether a band is empty across 60% of rows is meaningless on a four-row title page, which is where the phantom gutters came from. `ColumnMinGlyphRatio` rejects a split leaving a near-empty column. `HeadingMaxWords` stops a long epigraph set slightly large from becoming a heading. Blockquotes require two or more lines — without it, a maths textbook's 2,540 display equations all became blockquotes. Footnotes require eight letters of prose — without it, a mis-decoded run like `2 2 2 2 2` satisfies every stated condition. Furniture removal also matches on repeated *position*, because §4.5's text-hash rule assumes a constant running head and catches nothing on a book with per-chapter heads (0 of 107 on GeoTopo). Say so if you change them.
- **`hhrutter/tiff`, not `x/image/tiff`.** pdfcpu renders CMYK images to TIFF and the upstream decoder rejects that colour model. Same BSD-3 fork pdfcpu itself uses.
- **Line art is palettized before PNG encoding.** Palettizing is the entire reason spec §4.7 picks PNG on a low colour count; Go's encoder otherwise writes full RGBA, which turned a 255-colour test chart into 1.7 MB where the paletted form is 447 KB.
- **TeX text fonts get the OT1 encoding** (`internal/pdf/encoding.go`). They are symbolic Type1 programs with no `/Encoding` and no `/ToUnicode`, and sfnt cannot parse Type1 to recover the built-in encoding, so nothing in the PDF says how to read them. Without OT1 every f-ligature and typographic quote in a LaTeX document becomes U+FFFD. `isTeXTextFont` excludes the math families (CMMI, CMSY, CMEX, MSAM…) — they use OML/OMS/OMX and OT1 would be actively wrong. **Math symbol extraction from TeX documents remains unsolved** and is the largest remaining source of decode failures on academic PDFs.

## Non-negotiable constraints

Violating one of these is a design regression, not a style nit.

- **Pure Go, no cgo.** Static cross-compiled binaries are a hard requirement — this is why MuPDF bindings and `unidoc/unipdf` (AGPL) are ruled out. Verify with `CGO_ENABLED=0 GOOS=... go build ./cmd/decant`.
- **Deterministic output.** Identical input plus flags produces byte-identical EPUB regardless of `--jobs`. Anchor IDs are content hashes, not counters. `dc:identifier` is a UUIDv5 over the input SHA-256. ZIP entries sort by name with **no extra fields** — set `FileHeader.ModifiedDate`/`ModifiedTime` directly, because setting `Modified` makes `archive/zip` append an Info-ZIP extended-timestamp extra field. `CreateRaw` avoids data descriptors.
- **Library first.** All logic in `package decant`; `cmd/decant` only parses flags. No global state, no `os.Exit` below `main`, I/O through `io.Reader`/`io.Writer`/`io.ReaderAt`.
- **Fail loud, degrade gracefully.** Never emit silently corrupt EPUB. Every heuristic that fires records a `Diagnostic` in the `Report`.
- **Heuristics are tunable and inspectable.** Every threshold lives in `Heuristics` with a documented default — no magic numbers inline. `decant probe` dumps the intermediate model per stage.
- **No OCR, ever.** Not embedded, not by subprocess. Scanned PDFs exit 4 after the stage-2 sample. The classifier requires *both* low median glyph count and high image coverage, so it does not misfire on an art book with sparse captions.
- **Memory.** Stream pages; release glyph slices after stage 6. Targets: <300 MB RSS at 300 pages, <800 MB at 2,000, <25 MB binary (currently 6.7 MB stripped).

## Exit codes

`0` success · `1` runtime · `2` usage · `3` encrypted · `4` no text layer · `5` warnings with `--strict` · `6` malformed. Diagnostics to stderr; `--json` to stdout, unmixed.

## Testing approach

Golden tests assert on extracted text plus a **structure fingerprint** (ordered element types and heading levels), never byte-identical XHTML, so formatting refactors don't churn the corpus. `internal/testpdf` builds synthetic fixtures in memory.

### The real-world corpus

`make corpus` fetches [py-pdf/sample-files](https://github.com/py-pdf/sample-files) into `testdata/corpus/py-pdf`, pinned to a commit in the Makefile. **It is deliberately not vendored**: the files are CC-BY-SA-4.0 and spec §4.6 rules out carrying share-alike material in an MIT repo. Every corpus test skips when it is absent, so a fresh clone runs green; CI fetches it and enforces them.

**`pdftotext` is not ground truth.** It extracts text that displays sideways or upside down on a rotated page, and it renders form widget values; decant drops both deliberately. That is why recall against it is a *recorded* manifest bucket (`text_recall_bucket`) rather than an assertion — gating on it would demand decant reproduce text a reader cannot read. On `027-cropped-rotated-scaled` pdftotext gets 225 words to decant's 21, and rendering the pages confirms decant is right. Do not "fix" a low recall bucket without rendering the page first; spec §10 records the known cause of every bucket below 100%.

`testdata/corpus_manifest.json` is the regression gate — one entry per file recording outcome, block and heading counts, column count, a coarse decode-failure bucket, and fingerprint/text digests. Workflow: make a change, run `make manifest`, **read the diff**. Drift on a file you did not mean to touch is exactly what this exists to surface. The manifest is the tool for judging a heuristic change across 34 real documents instead of guessing from three fixtures — spec §11 warns that tuning against a handful of files produces overfitted garbage.

Corpus tests, all in `corpus_test.go`:
- `TestCorpusMatchesIndex` — page counts and encryption against the corpus's own `files.json`, an oracle from an independent implementation
- `TestCorpusManifest` — the golden gate above
- `TestCorpusDeterminism` — every file converted twice at different `--jobs`, byte-identical
- `TestCorpusEPUBCheck` — epubcheck on all 25 convertible files
- `TestCorpusSerializationLosesNoText` — every word in `doc.Blocks` must reach the EPUB. Exact, no oracle, no threshold: stages 7–8 format text, they do not select it. This is the only content check that can be an assertion, and it covers what the reading-order test cannot see, since that one reads `doc.Blocks` and so is blind to loss in rendering or chunk splitting
- `TestCorpusReadingOrder` — extracted words must be a ≥70% in-order subsequence of `pdftotext` output, which is spec §10's property test. Skips multi-column documents (pdftotext orders columns differently) and documents with form fields (forms are out of scope per spec §1, and pdftotext interleaves widget values)

Four fixture gotchas that already caused false failures:

- Text drawn horizontally on a `/Rotate 90` page genuinely displays sideways and is correctly dropped as a rotated run. Use `testpdf.RotatedTextPage` to pre-rotate, which is what real landscape pages do.
- Chunk splitting only happens at paragraph boundaries, so a fixture must contain blank lines. One enormous paragraph legitimately stays in a single file and logs a warning.
- Two-column fixtures need column text short enough to leave an actual gutter. Overlapping columns have no gutter and correctly detect as one column.
- `testpdf.HeadingPage` starts at y=720. Concatenating two of them overlaps the text at identical baselines and line assembly interleaves them character by character. Use `HeadingPageAt` with explicit start positions to stack sections.

Fuzzing is not optional — malformed PDFs are a hostile input class and the parser must not panic or allocate without bound. Add a seed to the relevant target whenever you fix a parser bug.

## Milestones

M1 interpreter + paragraphs (**done**) → M2 segmentation, columns, headings, TOC (**done**) → M3 images (**done**) → M4 furniture, dehyphenation, footnotes, lists (**done**) → M5 tables, profiles, report, `probe` (**done**) → M6 API stabilization + TUI integration.

Ship M1–M3 before optimizing: tuning layout thresholds against three test files produces overfitted garbage. Tag `v0.x` through M5; the API is unstable until `v1.0.0`.

## Licensing discipline

decant ships MIT, so vendored `hyph-utf8` hyphenation patterns must be MIT, BSD, or unrestricted. **Russian and Swedish are deliberately absent** — their files are LPPL-only, and §4.6 says to drop the language rather than complicate the license, even though §4.6's own language list names them. `TestLPPLLanguagesAreNotShipped` guards this; `THIRD_PARTY.md` records the per-file audit. Adding a language means auditing its license first.

## Workflow

GitHub Flow: branch off `main` per milestone or fix, commit, push, open a PR.
