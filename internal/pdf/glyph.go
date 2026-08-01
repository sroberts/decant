package pdf

// FontID indexes a document's font table.
//
// Spec section 4.2 sketches this field as a FontRef. It is an index here
// because Glyph is the dominant memory consumer in the pipeline: a dense page
// carries roughly 5,000 glyphs and section 9 budgets about 60 bytes each. A
// FontRef embeds a string header and would push Glyph past 80 bytes with no
// benefit, since every consumer needs the resolved *Font anyway. Resolve
// through Page.Fonts or Document.Fonts.
type FontID uint16

// NoFont marks a glyph emitted with no font set, which malformed streams
// produce by showing text before Tf.
const NoFont = FontID(0xFFFF)

// Glyph is one positioned character in page space, after the CTM applies.
//
// Field order is chosen so the struct packs to 56 bytes on 64-bit platforms.
type Glyph struct {
	// X, Y is the baseline origin in page space.
	X, Y float64
	// Advance is the horizontal displacement to the next glyph origin, in
	// page space, including character and word spacing.
	Advance float64
	// Size is the effective font size after Tz horizontal scaling and any
	// scale carried by the text and current transformation matrices.
	Size float64
	// Rise is the Ts text rise, used for super- and subscript detection.
	Rise float64
	// Rotation is the glyph's baseline angle in degrees.
	Rotation float64

	Rune   rune
	FontID FontID
	// RenderMode is the Tr text rendering mode. Mode 3 and mode 7 are
	// invisible; stage 2 keeps them only when a page has no visible text,
	// which is the searchable-scan case in spec section 4.2.
	RenderMode uint8
	// Missing marks a glyph whose code could not be mapped, emitted as
	// U+FFFD. Counted per page as the decode failure rate.
	Missing bool
}

// Visible reports whether the glyph renders under normal text rendering
// modes. Modes 3 (invisible) and 7 (clip-only) do not paint.
func (g Glyph) Visible() bool { return g.RenderMode != 3 && g.RenderMode != 7 }
