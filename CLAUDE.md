# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

decant converts fixed-layout, text-layer PDFs into semantic, reflowable EPUB 3. Module `github.com/sroberts/decant`, MIT.

`spec.md` is the authoritative design document — read the relevant section before changing a stage, and update §13 (open/closed decisions, with dates) when a design decision changes. Code comments reference spec sections by number; keep those references accurate when you move logic.

Currently at **M1** complete: text extraction and paragraph reconstruction work end to end and all output passes epubcheck. M2 (block segmentation with column detection, heading classification, outline-driven TOC) is next.

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
internal/pdf/        content stream lexer + interpreter, font machinery, doc/xref access
internal/layout/     line assembly, block segmentation, paragraph reconstruction
internal/epub/       deterministic EPUB 3.3 serialization
internal/testpdf/    synthetic PDF builder for tests
cmd/decant/          CLI, thin wrapper over the root package
```

Pipeline: `parse → glyphs → lines → blocks → furniture → classify → assemble → serialize`. Stages 2–6 are per-page; stage 7 needs the whole document. Roughly 70% of the work is in content stream interpretation (`internal/pdf/content.go`), block segmentation, and structure classification.

`Analyze` (stages 1–6, returns a mutable `*Document` block tree) is deliberately separate from `Write` (serialization) so the CrossPoint TUI can let a user correct heading levels before committing. Keep that split intact.

### Boundaries worth knowing

- **Public API must not leak internal types.** `Heuristics` is defined in the root package and projected onto `layout.Config` by `layoutConfig()` in `decant.go`. When you add a threshold, add it in both places plus `layout.DefaultConfig()`. The duplication is intentional.
- **`FontID` is a `uint16` index, not a `FontRef`.** `spec.md` §4.2 sketches it as a struct; it is an index because `Glyph` is the dominant memory consumer (~5,000/page, ~56 bytes each against §9's 60-byte budget). Resolve through `PageContent.Fonts`.
- **Page space runs y-down** from the top-left of the crop box, with `/Rotate` already applied by `baseCTM`. PDF user space is y-up. `OutlineItem.Y` is the one exception — it is still in user space and must be converted by the consumer.
- **pdfcpu panics on hostile input.** `internal/pdf/doc.go` wraps every entry point in `recoverMalformed`, converting panics to `ErrMalformed`. `FuzzOpen` found a nil deref in `EnsurePageCount` within seconds. Do not remove those recovers, and add one to any new pdfcpu entry point.

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

Golden tests assert on extracted text plus a **structure fingerprint** (ordered element types and heading levels), never byte-identical XHTML, so formatting refactors don't churn the corpus. `internal/testpdf` builds synthetic fixtures in memory; the real corpus comes from Scott's library at build time.

Two fixture gotchas that already caused false failures:

- Text drawn horizontally on a `/Rotate 90` page genuinely displays sideways and is correctly dropped as a rotated run. Use `testpdf.RotatedTextPage` to pre-rotate, which is what real landscape pages do.
- Chunk splitting only happens at paragraph boundaries, so a fixture must contain blank lines. One enormous paragraph legitimately stays in a single file and logs a warning.

Fuzzing is not optional — malformed PDFs are a hostile input class and the parser must not panic or allocate without bound. Add a seed to the relevant target whenever you fix a parser bug.

## Milestones

M1 interpreter + paragraphs (**done**) → M2 segmentation, columns, headings, TOC → M3 images → M4 furniture removal, dehyphenation, footnotes, lists → M5 tables, profiles, report, `probe` → M6 API stabilization + TUI integration.

Ship M1–M3 before optimizing: tuning layout thresholds against three test files produces overfitted garbage. Tag `v0.x` through M5; the API is unstable until `v1.0.0`.

## Licensing discipline

decant ships MIT, so vendored `hyph-utf8` hyphenation patterns (M4) must be MIT, BSD, or unrestricted. Drop a language rather than accept renaming or share-alike terms. Record every vendored file's terms in `THIRD_PARTY.md`.

## Workflow

GitHub Flow: branch off `main` per milestone or fix, commit, push, open a PR.
