# decant: PDF to EPUB Converter

**Specification v0.1**
Status: Draft. Repository `github.com/sroberts/decant`, MIT licensed. Single repo holds both the library (root package) and the CLI (`cmd/decant`).

## Bottom Line

Build a single static Go binary that reconstructs semantic, reflowable EPUB 3 from fixed-layout PDF. The hard problem is not file format plumbing, it is layout reconstruction: PDF stores positioned glyphs, EPUB needs paragraphs, headings, and reading order. Roughly 70% of the engineering effort lands in three stages (content stream interpretation, block segmentation, structure classification). Ship the conversion engine as an importable package first and the CLI as a thin wrapper, so the CrossPoint library TUI consumes the same code path without shelling out.

Primary technical risk: no permissively licensed pure-Go library extracts text with reliable positional and font metadata. Expect to write the content stream interpreter, roughly 2,000 to 3,000 lines.

## 1. Scope

### In scope
- Text-layer PDFs (digitally generated) converted to EPUB 3.3 with EPUB 2 NCX fallback
- Reading order recovery for single and multi-column layouts
- Heading, paragraph, list, blockquote, code block, and footnote reconstruction
- Table of contents from the PDF outline, with inference fallback
- Raster image extraction, placement, and re-encoding
- Table detection with graceful degradation
- Device profiles that constrain output for low-memory e-readers
- Deterministic, reproducible output

### Out of scope (v1)
- OCR, in any form. No embedded engine, no subprocess delegation. Scanned PDFs get detected and rejected with a diagnostic.
- PDF forms, annotations, JavaScript, embedded video
- Vector graphics conversion to SVG. Vector regions rasterize or drop.
- Editing or authoring EPUB
- Right-to-left and vertical CJK layout beyond basic text extraction. Detect and warn.
- Encrypted PDFs. Detect `/Encrypt` and exit 3. Decryption is cheap to add later behind a `--password` flag, so keep the detection path clean rather than stubbing the handler.

### Repository layout

One repo, `github.com/sroberts/decant`, MIT licensed:

```
decant/
  *.go              package decant, the conversion engine
  internal/pdf/     content stream interpreter, font machinery
  internal/layout/  segmentation and classification
  internal/epub/    serialization
  internal/hyphen/  Liang pattern matching + embedded pattern data
  cmd/decant/       CLI, thin wrapper over the root package
  testdata/corpus/  golden corpus and structure fingerprints
```

Everything below the root package sits under `internal/` so the public surface stays small and the CrossPoint TUI cannot couple to implementation details. Tag `v0.x` through M5 and treat the API as unstable; cut `v1.0.0` at M6 once the TUI has exercised `Analyze` and `Write` against real files.

## 2. Design Principles

1. **Library first.** `package decant` holds all logic. `cmd/decant` parses flags and calls it. No global state, no `os.Exit` below `main`, all I/O through `io.Reader`/`io.Writer`.
2. **Pure Go, no cgo.** A cross-compiled static binary is a hard requirement. This rules out MuPDF bindings.
3. **Fail loud, degrade gracefully.** Never emit silently corrupt EPUB. Every heuristic that fires records a diagnostic in the conversion report.
4. **Deterministic output.** Identical input plus identical flags produces byte-identical EPUB, independent of worker count or wall clock.
5. **Heuristics are tunable and inspectable.** Every threshold lives in a config struct with a documented default. `decant probe` dumps the intermediate model.

## 3. CLI Surface

```
decant convert <input.pdf> [-o output.epub] [flags]
decant probe   <input.pdf> [--stage=glyphs|lines|blocks|structure] [--page=N] [--json]
decant meta    <input.pdf>
decant version
```

`convert` is the default verb: `decant book.pdf` behaves as `decant convert book.pdf -o book.epub`.

### Core flags

| Flag | Default | Behavior |
|---|---|---|
| `-o, --output` | input basename + `.epub` | Output path; `-` writes to stdout |
| `--profile` | `standard` | `standard`, `crosspoint`, `minimal` |
| `--title`, `--author`, `--language` | from PDF metadata | Override Dublin Core metadata |
| `--pages` | all | Page range, e.g. `5-200,210` |
| `--split-at` | `h1` | Chapter boundary: `h1`, `h2`, `page`, `none` |
| `--max-chunk-bytes` | `262144` | Force split of oversized XHTML at paragraph boundary |
| `--columns` | `auto` | `auto`, `1`, `2`, `3` |
| `--keep-headers` | false | Retain running heads and folios |
| `--no-dehyphenate` | false | Preserve line-break hyphens verbatim |
| `--table-mode` | `auto` | `auto`, `html`, `text`, `drop` |
| `--image-max-width` | `1600` | Longest edge in pixels, 0 disables scaling |
| `--images` | `keep` | `keep`, `grayscale`, `drop` |
| `--report` | none | Write JSON conversion report to path |
| `--strict` | false | Non-zero exit when any quality threshold is breached |
| `--jobs` | `NumCPU` | Reserved. Accepted and ignored; page processing is sequential. See §4 |
| `--date` | PDF ModDate | Fixed timestamp for reproducible builds; honors `SOURCE_DATE_EPOCH` |

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Runtime failure |
| 2 | Usage error |
| 3 | Encrypted PDF (unsupported in v1) |
| 4 | No usable text layer (scanned document) |
| 5 | Converted with warnings, `--strict` set |
| 6 | Malformed PDF beyond repair |

Diagnostics go to stderr. `--json` output goes to stdout, unmixed.

## 4. Pipeline

```
parse → glyphs → lines → blocks → furniture → classify → assemble → serialize
```

Stages 2 through 6 run per page. Stage 7 requires the full document (heading rank, footnote resolution, TOC).

**Parallelism, revised (M6).** This section originally had stages 2 through 6 parallelize across `--jobs`. Measurement retired that.

Stage 2 is glyph extraction, and it runs inside pdfcpu. `indRefToObject` writes `xRefTable.CurObj` on every dereference and replaces `entry.Object` when decoding a lazy object stream, with no mutex anywhere, so concurrent page access is a data race rather than a speedup. Stage 1 has the same constraint. On the corpus's largest document those two are 66.6% of per-page time, which caps any page-parallel speedup at 1.5× by Amdahl.

It is worse than that in practice. Page analysis is about 5% of a conversion's wall clock: on that document the whole 117-page text pipeline runs in 160 ms while three images cost 650 ms. Parallelizing stages 3 through 6 would return roughly 4%.

`Options.Jobs` is therefore removed from §7 and `--jobs` is reserved at the CLI, accepted and ignored with a notice. Shipping a field that Go compatibility would make permanent, for a concept the architecture cannot deliver, is the one decision that cannot be reversed after `v1.0.0`; adding it back when parallelism exists is not a breaking change.

If parallelism is revisited, image processing is the target rather than pages. Extraction through pdfcpu stays serial, but re-encoding is pure Go and pipelining the two would overlap roughly 360 ms of extraction against 290 ms of processing.

### 4.1 Parse

Read xref, resolve object streams, handle both classic and cross-reference stream layouts. On damaged xref, rebuild by scanning the file for `N M obj` markers. Detect encryption from the trailer `/Encrypt` dictionary and exit 3 immediately, naming the security handler in the diagnostic. Extract the outline tree, page tree, `/Info` dictionary, and XMP metadata.

### 4.2 Glyph extraction

Interpret each page content stream, tracking full graphics and text state: `Tf Tm Td TD T* TL Tc Tw Tz Ts Tr Tj TJ ' "` plus `cm q Q gs Do`. Emit:

```go
type Glyph struct {
    Rune       rune
    X, Y       float64 // baseline origin in page space, after CTM
    Advance    float64
    Size       float64 // effective size after Tz and Tm scaling
    FontID     FontRef
    Rise       float64 // Ts, used for super/subscript detection
    RenderMode int
    Rotation   float64
}
```

**Code to rune mapping**, in order of precedence: `/ToUnicode` CMap, `/Encoding` with `/Differences`, standard encoding tables (Standard, WinAnsi, MacRoman, PDFDoc), Adobe Glyph List name lookup, reverse lookup in the embedded font's `cmap` table via `golang.org/x/image/font/sfnt`. Unmapped codes become U+FFFD and increment the page's decode-failure counter.

**Invisible text (Tr 3)** normally drops. Exception: when a page contains no visible glyphs but does contain a mode-3 layer, keep it. That is the OCR layer of a searchable scan and it is the only text available.

Normalize ligatures (ﬁ ﬂ ﬀ ﬃ ﬄ) to component characters, strip soft hyphens, and apply NFC normalization.

### 4.3 Line assembly

Cluster glyphs whose baselines fall within `0.3 × median glyph height`, then sort by x. Insert a space where the gap between advance-end and next origin exceeds `0.25 × the font's space width`, falling back to `0.25 × median advance` when the font lacks a space glyph. Rotated runs (|rotation| > 5°) extract into a separate collection and, by default, drop with a warning; margin text is rarely body content.

### 4.4 Block segmentation

Bottom-up merge is the primary algorithm and outperforms recursive XY-cut on documents with floats and sidebars:

1. Sort lines top to bottom.
2. Merge line into current block when horizontal overlap exceeds 50% of the narrower line **and** vertical gap is at most `1.5 × running median leading`.
3. Break the block on a font-family change, a size change exceeding 15%, or a leading gap over threshold.

Column detection uses a vertical projection profile of glyph density across the page width. Gutters appear as sustained near-zero bands wider than `2 × median space width` spanning at least 60% of the text block height. Order blocks column by column, top to bottom within each column. `--columns` overrides detection when the heuristic misfires on tables or figures.

### 4.5 Furniture removal

Sample up to 20 pages spread evenly through the document. Hash the digit-normalized text of every block whose bounding box falls entirely in the top or bottom 8% of the page. Remove any block whose hash repeats on at least 60% of sampled pages. Independently, remove blocks in those bands consisting solely of digits, roman numerals, or `Page N of M` patterns. Skip removal entirely on documents shorter than 5 pages.

### 4.6 Structure classification

Compute the body font as the glyph-count-weighted mode of (family, size, weight) across the document, not per page. Classify each block against it:

- **Heading**: size exceeds body by 15%, or bold with fewer than 15 words and terminal punctuation absent. Rank distinct heading sizes descending and map to `h1`..`h6`.
- **Outline reconciliation**: when a PDF outline exists, treat it as authoritative. Match each outline destination to the nearest block below it and force that block's level to the outline depth. Outline titles win over inferred text.
- **List item**: begins with a bullet glyph, `N.`, `N)`, or a letter marker, and subsequent lines carry a hanging indent. Group consecutive items into `<ul>` or `<ol>` and infer `start` from the first marker.
- **Blockquote**: both margins inset beyond body by more than 1.5 em.
- **Code**: fixed-pitch font family. Emit `<pre><code>`, preserve leading whitespace, suppress dehyphenation and paragraph merging.
- **Caption**: within 1.5 line heights of an image bounding box, size below body, or a `Figure|Table|Fig\.|Plate \d` prefix. Emit inside `<figure><figcaption>`.
- **Footnote**: sits in the bottom 20% band, size below body by more than 10%, begins with a digit, dagger, or asterisk marker.

**Paragraph reconstruction** inside a block: join lines with a space. Start a new paragraph when the line indent exceeds the block median by more than 0.5 em, when the vertical gap exceeds the running leading by more than 25%, or when the previous line ends with terminal punctuation while filling under 80% of the block width.

**Dehyphenation**: use TeX hyphenation patterns, inverted. When a line ends in U+002D and the next line begins lowercase, form the joined token and run Liang's pattern-matching algorithm against the pattern set for the document language. A break permitted at the fragment boundary means the hyphen is a typesetting artifact, so drop it. A break the patterns forbid means the hyphen is lexical (compound word, prefix), so keep it.

Implementation notes:

- Liang's algorithm is roughly 200 lines: build a trie from the pattern set, score every inter-letter position, and treat odd scores as legal break points. Standard `\patterns{}` files parse directly.
- Vendor pattern sets from the `hyph-utf8` distribution via `go:embed`. Each language costs 20 to 100 KB, so embedding the top 10 to 15 languages stays under 1 MB. Ship English, German, French, Spanish, Italian, Portuguese, Dutch, Polish, Russian, and Swedish in v1.
- Select the pattern set from `--language`, falling back to PDF `/Lang`, then XMP `dc:language`, then `en-us`. When no pattern set matches the detected language, disable dehyphenation and record it in the report rather than guessing with English patterns.
- Patterns handle common vocabulary, not proper nouns or technical jargon. Keep the override rules: retain the hyphen when both fragments capitalize, when a digit sits on either side, and inside code blocks.
- Licensing varies per language file (MIT, LPPL, and a few custom permissive terms). decant ships MIT, so vendor only files under MIT, BSD, or unrestricted terms. Skip any pattern set whose license imposes renaming or share-alike conditions, and drop that language from the shipped set rather than complicating the license. Record every file's terms in `THIRD_PARTY.md`.

Record every decision, with the pattern score, in the report.

**Inline runs**: derive bold and italic from `/FontDescriptor` flags plus family-name suffix matching, emit `<strong>` and `<em>`. Detect superscript from positive `Ts` rise or a baseline offset above 20% em combined with reduced size; emit `<sup>`. Link superscript markers to matching footnote blocks using `epub:type="noteref"` and `epub:type="footnote"`, which renders as a popup note on conforming readers.

### 4.7 Images

Extract image XObjects with their placement rectangle from the CTM at draw time.

- Drop images covering more than 95% of the page that draw beneath text (backgrounds and watermarks)
- Drop images under 16×16 px or under 2% of page area unless `--keep-small-images`
- Deduplicate by SHA-256 of decoded pixel data; repeated logos become one manifest entry
- Convert CMYK, indexed, and separation color spaces to RGB; composite `/SMask` alpha onto white
- Pass DCTDecode streams through unmodified when no scaling applies, which preserves quality and skips a decode-encode cycle
- Otherwise re-encode: JPEG q85 for photographic content, PNG for line art, decided by unique-color count against a 256 threshold. Device profiles override this: `crosspoint` forces JPEG pending verification of CrossPoint's image decoder (section 5.1)
- Scale with Catmull-Rom resampling when the longest edge exceeds `--image-max-width`

**Performance notes (M6).** Measured on the corpus's LaTeX textbook, where three images account for 650 ms of an 810 ms conversion while the entire 117-page text pipeline accounts for 160 ms. Images, not layout, are what a conversion spends its time on.

- Resampling targets an `image.RGBA` destination. `x/image/draw` generates a fast path per concrete destination type and falls back to a generic `RGBA64Image` path otherwise; `NRGBA` has none, so the vertical pass took the fallback while the horizontal pass did not. Premultiplied resampling is also the more correct of the two, since averaging non-premultiplied colour across a partly transparent edge weights fully transparent pixels as though they were opaque.
- PNG uses `DefaultCompression`. Across the corpus the strongest level buys 0.05% of output size for 4% of conversion time, which is the wrong trade for a format already carrying most of its win in the palette.
- The dedup digest converts a row at a time rather than the whole image. It runs on the source, before any scaling, so a 4000×3000 photograph needed 48 MB of scratch against §9's budget; it now needs 16 KB. The bytes fed to the hash are unchanged, so digests and therefore dedup are unaffected.

Together these are about 8% of an image-heavy conversion, and every output image is pixel-identical. What remains is roughly 360 ms inside pdfcpu's image extraction, which is not reachable without forking it.

Place each image as a block-level `<figure>` at its reading-order position. Images narrower than 40% of the text column and vertically inside a paragraph inline as `<img>` instead.

### 4.8 Tables

Two detection signals, both required for `html` output at high confidence:

1. **Ruling lines**: `re` and `l` path operators with stroke width under 2 pt forming a grid of at least 2 rows by 2 columns
2. **Alignment**: three or more consecutive lines sharing at least two column boundaries within 2 pt tolerance

Cell text assembles from glyphs bounded by adjacent rulings, or by inferred column boundaries when rulings are absent. Handle `colspan` where a cell's bounding box spans multiple detected boundaries. `--table-mode=auto` emits `<table>` at high confidence and space-preserved `<pre>` otherwise. Constrained profiles default to `text`.

The `image` mode this section originally specified, rasterizing the region to PNG at medium confidence, is **removed (M6)**. It depended on the vector renderer §13.1 leaves for after v1, so it never did anything but degrade to text with a warning. Go compatibility would have made the exported `TableImage` constant permanent at `v1.0.0`, and a documented mode that silently does something else is worse than one that does not exist. Asking for it is now a usage error. Reintroducing it alongside a rasterizer is not a breaking change; removing it later would have been. This is the same call made for `Options.Jobs` in §7, for the same reason.

**Implementation notes (M5).** The two signals above accept far too much on real documents, and three guards were added to narrow them. Each is tunable in `Heuristics`.

- `TableMinFilledRatio` (0.5) applies twice: to the fraction of cells carrying any text, and to the fraction of filled cells carrying more than a single character. The first rejects layout frames; the second rejects a plotted graph's axes and tick labels, and diagrams drawn from repeated marks, both of which form a fully aligned grid whose cells hold one glyph each.
- A candidate from the alignment signal is rejected when its rows straddle more than one of the page's detected columns. Section 4.3 owns that geometry: on a two-column page every left-column line shares a baseline with a right-column line, so the rows are perfectly aligned and every boundary is shared. Nothing in the text distinguishes that from a two-column table, but the page layout does, and treating it as a table flattens the reading order into rows read across instead of down.

Medium confidence cannot rasterize while section 13.1 stays open, so it degrades to `text` and records a warning rather than silently substituting.

Residual over-firing is real and documented rather than tuned away: on the corpus's LaTeX textbook, detection reports eight medium-confidence tables that are mathematical displays. Section 11 warns against tuning against a handful of files, and this corpus contains only one document with a genuine ruled table.

### 4.9 Assembly and serialization

Chapter files split at `--split-at` boundaries and again at `--max-chunk-bytes`, always at a paragraph boundary, with `-2`, `-3` suffixes. Internal cross-references (PDF `/Link` annotations targeting page destinations) rewrite to `href` fragments against generated anchor IDs. Anchor IDs derive from a content hash, not a counter, so they stay stable across runs.

**Implementation notes (M6).**

- Named destinations are resolved by walking the catalog's `/Names /Dests` name tree directly. pdfcpu's `DereferenceDestArray` reads a map only its validation pass populates, and decant reads with `ValidationRelaxed`, so every named destination failed. The same resolver serves outline reconciliation in §4.6, which was silently dead on documents using named destinations — most TeX output.
- A link is matched to text glyph by glyph, on whether a glyph's center falls inside the annotation rectangle. Matching on line bounds instead would link a whole line for a reference that covers three words of it. Annotation rectangles are drawn generously and clip a neighbouring glyph's edge often enough that an overlap test pulls it in, which is why the test point is the center.
- Only blocks something points at carry an `id`. Anchoring every paragraph would inflate every file against the `crosspoint` chunk budget in §5 for no benefit.
- A cross-reference whose destination resolves to no block renders as plain text and is counted in the report. It is an info rather than a warning: a link into a page outside `--pages`, or into one that was dropped, is a normal consequence of options the caller chose.
- Overlapping spans are resolved rather than emitted, and a superscript noteref inside a linked range emits as a plain `<sup>`, because nested anchors are invalid XHTML.
- Only `/Rect` is read, not `/QuadPoints`, so a single annotation genuinely spanning two lines over-links to its bounding box. Producers usually emit one annotation per line instead.

**EPUB structure:**

```
mimetype                        (stored, uncompressed, first entry)
META-INF/container.xml
OEBPS/package.opf
OEBPS/nav.xhtml                 (EPUB 3 nav with toc + landmarks)
OEBPS/toc.ncx                   (EPUB 2 fallback)
OEBPS/text/ch001.xhtml ...
OEBPS/images/img001.jpg ...
OEBPS/styles/base.css
```

Metadata: `dc:identifier` is a UUIDv5 over the input file SHA-256, which makes the identifier stable and collision-free across reconversions. `dc:source` records the original filename. `dcterms:modified` takes `--date`, `SOURCE_DATE_EPOCH`, or the PDF `ModDate`, in that order.

The ZIP writer sorts entries deterministically, zeroes extra fields, and writes the fixed timestamp to every header.

CSS stays under 50 lines: relative `em` sizing, no fixed widths, no embedded fonts, `img { max-width: 100%; height: auto; }`, and heading margins. Reader defaults should win wherever possible.

## 5. Device Profiles

| Setting | standard | crosspoint | minimal |
|---|---|---|---|
| Image handling | keep, RGB | 16-level grayscale; JPEG for photos, paletted PNG for line art | drop |
| `--image-max-width` | 1600 | 480 | n/a |
| `--max-chunk-bytes` | 262144 | 262144 | 65536 |
| Table mode | auto | text | text |
| TOC depth | unlimited | 2 | 2 |
| CSS | base | base minus decorative rules | none |
| NCX fallback | yes | yes | yes |

### 5.1 Target hardware: Xteink X4

The `crosspoint` profile targets the Xteink X4 running CrossPoint firmware. Stock-firmware specifications drive the defaults:

| Property | Value | Consequence for decant |
|---|---|---|
| Display | 4.3 in E Ink, 220 PPI, 114 × 69 × 5.9 mm body | Panel is **480 × 800**, confirmed by the CrossPoint user guide. Portrait text column is 480 px wide, which sets `--image-max-width`. The X3 is 528 × 792 if that target ever matters |
| CPU | ESP32-C3, roughly 380 KB usable RAM | Not a constraint on chunk size: the firmware streams XHTML rather than holding it. See the ceiling note below |
| Image formats | stock firmware: JPG and BMP only | CrossPoint's EPUB path reads JPG and PNG, and has no BMP decoder. Confirmed by reading the firmware; see the codec note below |
| Document formats | EPUB and TXT | EPUB 3 with NCX fallback stays correct |
| Front light | none | No dark-mode or contrast CSS. Assume ambient light |
| Touchscreen | none, page-turn buttons only | Navigation is linear. Flatten the TOC to two levels; deep nesting is unusable on two buttons |
| Storage | microSD, 16 GB stock | File size is not a constraint. Per-file size is |

**Image codec.** Section 4.7 selects PNG for line art on unique-color count, and that selection stands under `crosspoint`. Photographs quantize to 16 gray levels, take Floyd-Steinberg dithering, and encode at q90; dithering before JPEG limits ringing artifacts that a low-bit-depth panel otherwise renders as visible banding. Never emit BMP.

The PNG exclusion this section previously carried is withdrawn. Reading CrossPoint's EPUB image path settled it: `ImageDecoderFactory` dispatches on file extension to a `PNGdec`-backed decoder living in `lib/Epub/Epub/converters/`, and that decoder handles indexed PNG including palette transparency, which is the form line art takes. There is no BMP decoder in that path at all, so the stock firmware's "JPG and BMP" does not describe what CrossPoint reads from an EPUB: it reads JPG and PNG.

The cost is heap, and it is the reason to keep the choice narrow rather than universal:

| Decoder | Working set | Free heap required |
|---|---|---|
| JPEG | 20 KB | 36 KB |
| PNG | 44 KB | 60 KB |

Both fail closed, logging and skipping the image rather than crashing. Line art is where PNG earns that extra 24 KB: it is smaller as a paletted PNG than as a JPEG, and it stays sharp where JPEG would ring.

Three consequences follow for the pipeline:

- Line art is classified on the **source** image, before any reduction. Every reduction destroys the evidence in a different direction: grayscale conversion leaves at most 256 values so everything afterwards looks like line art, smooth resampling interpolates new colors so a chart afterwards looks like a photograph, and dithering scatters flat regions into noise with the same effect.
- Line art scales with **nearest-neighbor**, not Catmull-Rom. Interpolating across every edge blurs the artwork and destroys the palette that makes PNG worth choosing.
- Line art is **not** dithered. Dithering a flat region is noise on a diagram, and the 16-level quantization it exists to serve is a photographic concern.

**What CrossPoint documents, and what it does not.**

- The firmware runs on the ESP32-C3 with roughly 380 KB of usable RAM, and its design caches aggressively to the SD card specifically to work inside that limit. Chapters cache to `.crosspoint/epub_<hash>/sections/N.bin` on first load and serve from cache afterward.
- Recent releases index sections on demand in the background, which cut large-book open times to about five seconds. The same release notes describe fixing CSS parser bugs that caused out-of-memory crashes on complex EPUBs. That is a direct argument for the minimal stylesheet this profile already emits.
- CrossPoint renders EPUB 2 and 3, handles images and footnotes, and does its own hyphenation and kerning. Its hyphenation is display-time and does not conflict with decant's source-level dehyphenation.
- The CrossInk fork publishes whole-file guidance: EPUBs under 20 MB work best, files over 50 MB grow slow and memory-sensitive. No per-XHTML byte ceiling appears in any public documentation.

**The XHTML size ceiling is not a memory constraint.** Settled by reading `crosspoint-reader/crosspoint-reader`, which shows chapter size never lands in RAM:

- Chapter XHTML is parsed by **expat**, streaming, through a **1 KB** buffer (`ChapterHtmlSlimParser.cpp: PARSE_BUFFER_SIZE`). There is no DOM.
- Each laid-out page is serialized to `sections/N.bin` and freed as it completes (`Section::onPageComplete`). Builds are incremental (`buildSomeMore`) and resumable across sleep or exit via a partial-file sentinel.
- The only structure that scales with chapter length is the in-RAM page lookup table: a 12-byte `PageLutEntry` per page, held only while a build runs.
- No chapter or XHTML size limit appears anywhere in the firmware. The single content-size guard is `MAX_CSS_FILE_SIZE = 128 KB` — **CSS**, which is what the out-of-memory crashes in the release notes were about. decant emits under 1 KB of CSS.

At 480 × 800 the reader fits roughly 40 characters across and 29 lines down, about 1,160 characters per page; decant's XHTML runs about 88% text, so roughly **1,300 XHTML bytes per page**. The lookup table therefore costs about **0.9% of a chapter's byte size**:

| Chunk | Pages | Lookup table |
|---|---|---|
| 64 KB | ~50 | 0.6 KB |
| 256 KB | ~200 | 2.4 KB |
| 1 MB | ~800 | 9.6 KB |
| 4 MB | ~3,200 | 38 KB |

For scale, the firmware refuses to decode a PNG below 60 KB of free heap and to retain a font below 40 KB. The lookup table is noise against that until several megabytes. The other hard bound is `uint16_t pageCount`, capping a section at 65,535 pages, near 85 MB.

The real argument for keeping chapters modest is **cache rebuild cost**, not memory: the section file format has moved v28 through v35, and each bump invalidates caches, so a reader re-lays-out whatever section it is in. 262144 puts that at roughly 200 pages, which rebuilds quickly.

The 20 MB whole-file target from CrossInk is a separate, still-useful `crosspoint` warning threshold.

## 6. Scanned PDF Handling

The tool does not OCR. Scanned PDFs fail fast.

Classify as scanned when median glyph count per page falls below 20 and full-page images cover over 80% of pages. Run the check immediately after stage 2 on a sample of up to 20 pages, before spending work on segmentation.

On detection, exit 4 and write a diagnostic naming the metrics that triggered it and pointing at an external OCR step:

```
error: no usable text layer (median 3 glyphs/page across 20 sampled pages,
       94% of pages covered by full-page images)
       decant does not perform OCR. Run the file through an OCR pass that
       writes a text layer (ocrmypdf, tesseract --pdf), then convert the result.
```

`--images-only` remains available and emits an image-per-page EPUB. That output is not reflowable and exists only as an escape hatch for documents worth shelving on the device as-is.

Two edge cases the classifier must not misfire on: a text PDF whose body sits entirely in mode-3 invisible text over page images (a searchable scan, which converts fine and must pass), and an image-heavy art book with sparse but real captions (converts, with a warning).

## 7. Library API

```go
package decant

type Options struct {
    Profile      Profile
    Metadata     Metadata
    Pages        PageRange
    Heuristics   Heuristics // every threshold from section 4
    Deterministic time.Time
}

type Converter struct{ /* unexported */ }

func New(opts Options) (*Converter, error)

// Analyze runs stages 1 through 6 and returns the intermediate model.
func (c *Converter) Analyze(ctx context.Context, r io.ReaderAt, size int64) (*Document, error)

// Write serializes an analyzed Document to EPUB.
func (c *Converter) Write(ctx context.Context, doc *Document, w io.Writer) (*Report, error)

// Convert is Analyze followed by Write.
func (c *Converter) Convert(ctx context.Context, r io.ReaderAt, size int64, w io.Writer) (*Report, error)
```

`Document` exposes the block tree so callers modify structure before serialization. The CrossPoint TUI uses this to preview detected chapters and let the user correct heading levels before committing the EPUB, which is the main reason for splitting `Analyze` and `Write`.

`Report` carries per-page metrics: decode failure rate, blocks dropped as furniture, headings detected, dehyphenation decisions, table confidence scores, and an overall 0 to 100 quality score. The TUI surfaces the score to flag conversions worth reviewing.

## 8. Dependencies

| Package | License | Role |
|---|---|---|
| `github.com/pdfcpu/pdfcpu` | Apache-2.0 | xref parsing, object model, image XObject extraction |
| `golang.org/x/image` | BSD-3 | `font/sfnt` for embedded font metrics and cmap, resampling, TIFF/BMP decode |
| `golang.org/x/text` | BSD-3 | Unicode normalization, encoding tables |
| stdlib `flag`, `archive/zip`, `image/*` | BSD-3 | CLI, EPUB container, image codecs |
| `hyph-utf8` pattern files (vendored data, not code) | per-language, audit required | Dehyphenation via Liang pattern matching |

**Rejected:** `unidoc/unipdf` (AGPL or paid commercial license, incompatible with permissive distribution), `go-fitz` and other MuPDF bindings (cgo plus AGPL, breaks the static binary requirement), `rsc.io/pdf` (no font or positioning support). `ledongthuc/pdf` (MIT) serves as a reference implementation for content stream handling but not as a dependency; its text model discards the positional fidelity this tool needs.

Use stdlib `flag` with manual subcommand dispatch rather than cobra. The CLI surface is small enough that the dependency does not pay for itself.

## 9. Performance Targets

| Metric | Target |
|---|---|
| 300-page text PDF, 8 cores | under 5 s wall clock |
| Peak RSS, 300-page PDF | under 300 MB |
| Peak RSS, 2,000-page PDF | under 800 MB |
| Binary size | under 25 MB including embedded hyphenation patterns |
| Determinism | byte-identical output from repeated conversion of one input |

Stream page processing rather than holding every page's glyph set. Retain only the block model after stage 6 and release glyph slices; glyphs dominate memory at roughly 60 bytes each and a dense page carries 5,000 of them.

## 10. Testing

**Corpus-driven golden tests.** Scott supplies the corpus at build time, drawn from his actual library: academic papers and trade books both. Backfill the gaps from that set with a two-column LaTeX paper, a Word export, an InDesign-set book with drop caps, a government report heavy with tables, a scanned book with an OCR layer, a CJK document, and a deliberately malformed PDF with a broken xref. Academic PDFs in the corpus mean column detection stays on the M2 critical path and cannot be deferred. Assert on extracted plain text and a structure fingerprint (ordered list of element types and heading levels), not on byte-identical XHTML, so formatting refactors do not churn the corpus.

**Property tests.** Reading order preserves the sentence-level word sequence, verified against `pdftotext -layout` output for single-column documents. Determinism verified by converting each file twice through separately constructed Converters and diffing.

**Content fidelity (M6).** "Does the EPUB still say what the PDF said" splits in two, and the halves need opposite treatment because only one has a trustworthy oracle.

*Serialization is checked exactly.* Stages 7 and 8 format text; they do not select it. Every word the analyzed `Document` holds must therefore appear in the EPUB, so `TestSerializationLosesNoText` and `TestCorpusSerializationLosesNoText` assert equality with no external tool and no threshold. This covers the half the reading-order property cannot see: that test reads `doc.Blocks`, so text lost in rendering or chunk splitting is invisible to it. Both currently pass with zero loss on every fixture and all 25 convertible corpus documents.

*Extraction is measured, not gated.* Recall against `pdftotext` is recorded in the corpus manifest as a bucketed percentage rather than asserted, because another PDF tool is not ground truth — it makes different decisions about what counts as content. On `027-cropped-rotated-scaled`, pdftotext extracts 225 words to decant's 21; rendering the pages shows the difference is text that displays sideways or upside down, which §4.3 drops deliberately and a reader could not read anyway. On `012-libreoffice-form` the difference is form widget values, out of scope per §1. A gate would demand decant reproduce both. Recording the number instead means a change that silently drops text moves the manifest, which is what the manifest is for.

The buckets that are not 100% each have a known cause: math symbol fonts on `009-pdflatex-geotopo` (75%, the largest open decode gap), deliberate rotation drops (10% and 0%), form widgets (90%), and emoji plus phone-number formatting (95%). A bucket falling for any other reason is a regression.

Comparison normalizes what carries no meaning: case, edge punctuation, private-use superscript sentinels, and XML entities. Inline elements (`sup`, `em`, `span`, …) are treated as zero-width and block elements as separators, matching how a reader sees the text — treating a `<sup>` as a space splits the word it sits inside and reports it as lost.

**Fuzzing.** Native Go fuzzing against the content stream interpreter and xref parser. Malformed PDFs are a hostile input class; the parser must not panic or allocate unbounded.

**Validation.** Run `epubcheck` in CI against every corpus output. Zero errors is a merge gate; warnings get triaged.

## 11. Milestones

1. **M1**: Parse, glyph extraction, line assembly, single-file EPUB of plain paragraphs. Proves the interpreter.
2. **M2**: Block segmentation, column detection, heading classification, outline-driven TOC, chapter splitting.
3. **M3**: Image extraction, placement, re-encoding, figure and caption handling.
4. **M4**: Furniture removal, dehyphenation, footnote linking, list and blockquote detection.
5. **M5**: Table detection, device profiles, conversion report, `probe` subcommand.
6. **M6**: Public library API stabilization and CrossPoint TUI integration.

Ship M1 through M3 before optimizing anything. Layout heuristics need real-corpus feedback, and tuning thresholds against three test files produces overfitted garbage.

## 12. Development Workflow

GitHub Flow throughout: branch off `main` per milestone or fix, commit, push, open a PR. CI runs `go vet`, `staticcheck`, race-enabled tests, the golden corpus, and `epubcheck`. Tag releases with goreleaser, cross-compiling for linux/amd64, linux/arm64, darwin/arm64, and windows/amd64.

## 13. Open Decisions

1. **Vector graphics.** Charts drawn as paths are not rendered. They no longer vanish silently: the interpreter counts painted paths per page and the conversion report warns when a page carries more than `VectorMinPaints` of them, which principle 3 requires while the question stays open. On the sample corpus that fires on 39 of the 117 pages of the one mathematics textbook and on nothing else.

    Two constraints have since narrowed the decision. Conversion to SVG is dead rather than merely out of scope: CrossPoint's EPUB path decodes JPG and PNG only (section 13, closed), so rasterizing to PNG is the only output that reaches the target device. And the rendering engine the original note worried about is largely present already: `golang.org/x/image/vector` is a pure-Go rasterizer under BSD-3 and `golang.org/x/image` is a direct dependency. What remains is Bézier flattening, fill rules, stroking, and the colour operators.

    Table detection in section 4.8 requires interpreting `re` and `l` with stroke widths regardless, so M5 has to build path tracking whether or not vector artwork is ever rendered. Deciding this after that work, rather than before it, costs nothing and removes most of the estimate's uncertainty.

**Closed:**

- **CrossPoint in-EPUB image formats** (2026-08-01): the EPUB path reads JPG and PNG, so the `crosspoint` profile keeps paletted PNG for line art and uses dithered JPEG only for photographs. Settled by reading the firmware: `ImageDecoderFactory` dispatches by extension to a `PNGdec`-backed decoder that handles indexed PNG with palette transparency, and no BMP decoder exists in that path. PNG costs 24 KB more free heap than JPEG (60 KB against 36 KB) and fails closed, which is why the choice stays narrow. See section 5.1.
- **CrossPoint XHTML size ceiling** (2026-08-01): not a memory constraint, and `--max-chunk-bytes` for the `crosspoint` profile is therefore 262144, the same as `standard`. Settled by reading the firmware rather than measuring: XHTML streams through expat in 1 KB chunks and pages are serialized to the SD card as they complete, so only a 12-byte-per-page lookup table scales with chapter length, about 0.9% of chapter bytes. The out-of-memory crashes in the release notes were the CSS parser, now guarded at 128 KB; decant emits under 1 KB of CSS. See section 5.1. The binding consideration is cache rebuild cost after a section-format bump, not RAM.
- **Name, module path, license** (2026-08-01): `decant`, at `github.com/sroberts/decant`, MIT. Library and CLI ship from one repo.
- **Encryption** (2026-08-01): out of scope for v1. Detect and exit 3.
- **Dehyphenation approach** (2026-08-01): TeX hyphenation patterns via Liang's algorithm. No vendored wordlist. See section 4.6.
- **OCR** (2026-08-01): out of scope. Scanned PDFs exit 4. See section 6.