package decant

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// BlockKind classifies a block after structure classification.
type BlockKind string

const (
	// KindParagraph is flowed body text.
	KindParagraph BlockKind = "paragraph"
	// KindHeading is a heading; see Block.Level for its rank.
	KindHeading BlockKind = "heading"
	// KindList is a bullet or numbered list.
	KindList BlockKind = "list"
	// KindQuote is a blockquote.
	KindQuote BlockKind = "blockquote"
	// KindCode is a fixed-pitch code block.
	KindCode BlockKind = "code"
	// KindCaption is a figure or table caption.
	KindCaption BlockKind = "caption"
	// KindFootnote is a footnote body.
	KindFootnote BlockKind = "footnote"
	// KindFigure is an image with optional caption.
	KindFigure BlockKind = "figure"
	// KindTable is a detected table.
	KindTable BlockKind = "table"
)

// Block is one unit of reconstructed content in reading order.
//
// Document exposes these so a caller can correct structure before
// serialization; the CrossPoint TUI uses that to let a reader fix heading
// levels. Mutating Kind, Level, or Text is supported. Page and Bounds are
// provenance and should be left alone.
type Block struct {
	Kind BlockKind
	// Level is the heading rank 1 through 6, and 0 for non-headings.
	Level int
	// Text is the block's plain text content.
	Text string

	// Page is the zero-based source page index.
	Page int
	// Bounds is the block's bounding box in page space.
	Bounds Rect

	// ID is the anchor identifier. It derives from a content hash rather than
	// a counter so it stays stable across runs, which internal cross-
	// reference rewriting in spec section 4.9 depends on.
	ID string

	// Size is the block's median font size in points.
	Size float64
	// Font is the block's dominant font family name.
	Font string

	// ImageID names the image a figure block carries, matching Document.Images.
	// Empty for every other kind.
	ImageID string
	// Caption is a figure's caption text, empty when it has none.
	Caption string
	// InlineImage marks a figure narrow enough, and inside a paragraph, to
	// flow in the text rather than stand as a block. Spec 4.7.
	InlineImage bool
}

// Rect is an axis-aligned rectangle in page space, with y increasing
// downward from the top-left of the page.
type Rect struct {
	MinX, MinY, MaxX, MaxY float64
}

// Width returns the horizontal extent.
func (r Rect) Width() float64 { return r.MaxX - r.MinX }

// Height returns the vertical extent.
func (r Rect) Height() float64 { return r.MaxY - r.MinY }

// OutlineItem is one node of the source PDF's bookmark tree.
type OutlineItem struct {
	Title string
	// Page is the zero-based destination page, or -1 when unresolved.
	Page int
	// Y is the destination's vertical position in PDF user space, or NaN when
	// the destination specifies none. It is not page space: user space runs
	// y-up from the crop box's lower-left corner.
	Y        float64
	Children []OutlineItem
}

// Document is the intermediate model produced by Analyze: the block tree
// plus the metadata needed to serialize it.
type Document struct {
	// Title, Author, and Language are resolved from PDF metadata and any
	// caller overrides.
	Title    string
	Author   string
	Language string

	// Source is the input filename, recorded as dc:source.
	Source string

	// Blocks is the reconstructed content in reading order.
	Blocks []Block

	// Outline is the PDF bookmark tree, empty when the document has none.
	Outline []OutlineItem

	// Images are the pictures carried into the EPUB, referenced by
	// Block.ImageID.
	Images []Image

	// PageCount is the number of pages in the source document, before any
	// page range applies.
	PageCount int

	// Digest is the hex SHA-256 of the input file. The EPUB identifier is a
	// UUIDv5 over it, which makes reconversion produce the same identifier.
	Digest string

	// Modified is the timestamp written to dcterms:modified and every ZIP
	// header.
	Modified time.Time

	// report accumulates diagnostics through Analyze and is finished by
	// Write.
	report *Report
}

// Report returns the diagnostics gathered so far. Analyze fills the per-page
// metrics; Write adds serialization results and the quality score.
func (d *Document) Report() *Report { return d.report }

// blockID computes the stable anchor identifier for a block.
//
// Hashing the text with the page index keeps identical repeated text (a
// running head, a recurring section title) from colliding, while keeping the
// value independent of block ordering.
func blockID(page int, kind BlockKind, text string) string {
	h := sha256.New()
	h.Write([]byte(kind))
	h.Write([]byte{0})
	// A four-byte page index, big-endian, so the hash does not depend on
	// integer formatting.
	h.Write([]byte{
		byte(page >> 24), byte(page >> 16), byte(page >> 8), byte(page),
	})
	h.Write([]byte(text))
	// Eight hex characters is 32 bits. Anchor collisions within one document
	// would break a cross-reference, so this is checked and extended by the
	// caller when it collides.
	return "b" + hex.EncodeToString(h.Sum(nil))[:8]
}

// assignBlockIDs fills in stable, unique anchor IDs.
func assignBlockIDs(blocks []Block) {
	seen := make(map[string]int, len(blocks))
	for i := range blocks {
		id := blockID(blocks[i].Page, blocks[i].Kind, blocks[i].Text)
		if n, clash := seen[id]; clash {
			// Deterministic disambiguation: append the occurrence count.
			seen[id] = n + 1
			id = id + "-" + hex.EncodeToString([]byte{byte(n)})
		} else {
			seen[id] = 1
		}
		blocks[i].ID = id
	}
}
