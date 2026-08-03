package decant

import (
	"math"
	"sort"
)

// Severity ranks a diagnostic.
type Severity string

const (
	// SeverityInfo records a decision worth auditing but not worrying about.
	SeverityInfo Severity = "info"
	// SeverityWarning records degraded output. Under --strict these make the
	// run exit non-zero.
	SeverityWarning Severity = "warning"
)

// Diagnostic records one heuristic firing or one quality problem. Spec
// principle 3 requires every heuristic that fires to leave one of these.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	// Stage names the pipeline stage, e.g. "glyphs" or "assemble".
	Stage string `json:"stage"`
	// Page is the zero-based page index, or -1 for document-level entries.
	Page    int    `json:"page"`
	Message string `json:"message"`
}

// HyphenationReport summarizes what dehyphenation did.
//
// Spec section 4.6 asks for every decision to be recorded. A full-length book
// makes thousands, so the counts cover all of them and Decisions carries a
// bounded sample; the full trail would make a report file unreadable.
type HyphenationReport struct {
	// Language is the pattern set that was used, empty when dehyphenation
	// was disabled.
	Language string `json:"language,omitempty"`
	// Patterns is the number of patterns in that set.
	Patterns int `json:"patterns,omitempty"`
	// Dropped counts hyphens removed as typesetting artifacts, Kept counts
	// those judged lexical.
	Dropped int `json:"dropped"`
	Kept    int `json:"kept"`
	// Decisions is a bounded sample of the individual calls.
	Decisions []HyphenDecision `json:"decisions,omitempty"`
}

// HyphenDecision records one line-break hyphen and the reasoning behind it.
type HyphenDecision struct {
	Left    string `json:"left"`
	Right   string `json:"right"`
	Dropped bool   `json:"dropped"`
	Reason  string `json:"reason"`
}

// PageMetrics holds the per-page numbers the report surfaces.
type PageMetrics struct {
	Page int `json:"page"`
	// Glyphs is the number of glyphs extracted after invisible-text
	// filtering.
	Glyphs int `json:"glyphs"`
	// DecodeFailures counts glyphs that mapped to U+FFFD.
	DecodeFailures int `json:"decode_failures"`
	// Lines and Blocks count the stage 3 and stage 4 output.
	Lines  int `json:"lines"`
	Blocks int `json:"blocks"`
	// Columns is the number of text columns detected on the page.
	Columns int `json:"columns"`
	// Images is the number of images placed from the page, after the drop
	// rules in spec section 4.7.
	Images int `json:"images"`
	// Tables counts tables detected on the page.
	Tables int `json:"tables,omitempty"`
	// VectorPaints counts painted path operations on the page. decant does
	// not render vector artwork, so a high count means content was lost.
	VectorPaints int `json:"vector_paints,omitempty"`
	// RotatedDropped counts rotated runs discarded.
	RotatedDropped int `json:"rotated_dropped"`
	// UsedInvisibleText marks a page whose only text was a mode-3 layer,
	// which is the searchable-scan case.
	UsedInvisibleText bool `json:"used_invisible_text"`
}

// DecodeFailureRate returns failures as a fraction of glyphs.
func (m PageMetrics) DecodeFailureRate() float64 {
	if m.Glyphs == 0 {
		return 0
	}
	return float64(m.DecodeFailures) / float64(m.Glyphs)
}

// Report describes one conversion. It is written as JSON by --report and
// surfaced by the CrossPoint TUI to flag conversions worth reviewing.
type Report struct {
	// Source is the input filename.
	Source string `json:"source"`
	// PageCount is the source document's page count.
	PageCount int `json:"page_count"`
	// PagesConverted is how many pages the page range selected.
	PagesConverted int `json:"pages_converted"`

	Pages []PageMetrics `json:"pages"`

	// Blocks counts blocks by kind.
	Blocks map[BlockKind]int `json:"blocks"`
	// Headings counts headings by level, indexed 1 through 6.
	Headings map[int]int `json:"headings,omitempty"`
	// BodyFont describes the document's computed body font, which every
	// structure decision in spec section 4.6 is measured against.
	BodyFont string `json:"body_font,omitempty"`
	// MultiColumnPages counts pages where more than one column was detected.
	MultiColumnPages int `json:"multi_column_pages"`
	// ImagesPlaced counts distinct images carried into the EPUB, after
	// deduplication.
	ImagesPlaced int `json:"images_placed"`
	// ImageBytes is the total encoded size of those images.
	ImageBytes int `json:"image_bytes"`
	// Hyphenation summarizes the dehyphenation decisions in spec 4.6.
	Hyphenation HyphenationReport `json:"hyphenation"`
	// Tables counts detected tables by confidence, which is what
	// --table-mode=auto keys on.
	Tables map[string]int `json:"tables,omitempty"`
	// FurnitureRemoved counts blocks dropped as running heads or folios.
	FurnitureRemoved int `json:"furniture_removed"`
	// VectorPagesDropped counts pages carrying vector artwork that decant did
	// not render, and VectorPaintsDropped the painted paths on them.
	//
	// Spec section 1 puts vector conversion out of scope for v1 and section
	// 13 keeps rasterization open. Reporting the loss is what principle 3
	// requires in the meantime: a chart drawn as paths otherwise disappears
	// with no trace in the output or the report.
	VectorPagesDropped  int `json:"vector_pages_dropped,omitempty"`
	VectorPaintsDropped int `json:"vector_paints_dropped,omitempty"`
	// Chapters is the number of XHTML files written.
	Chapters int `json:"chapters"`
	// OutputBytes is the size of the EPUB.
	OutputBytes int64 `json:"output_bytes"`
	// LargestChapterBytes is the biggest single XHTML document, which is the
	// dominant failure mode on the crosspoint profile.
	LargestChapterBytes int `json:"largest_chapter_bytes"`

	Diagnostics []Diagnostic `json:"diagnostics"`

	// QualityScore is a 0 to 100 summary. See Finish for its derivation.
	QualityScore int `json:"quality_score"`
}

func newReport(source string) *Report {
	return &Report{
		Source:   source,
		Blocks:   map[BlockKind]int{},
		Headings: map[int]int{},
		Tables:   map[string]int{},
	}
}

// Warnings returns the number of warning-level diagnostics, which --strict
// turns into a non-zero exit.
func (r *Report) Warnings() int {
	n := 0
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityWarning {
			n++
		}
	}
	return n
}

func (r *Report) info(stage string, page int, msg string) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{
		Severity: SeverityInfo, Stage: stage, Page: page, Message: msg,
	})
}

func (r *Report) warn(stage string, page int, msg string) {
	r.Diagnostics = append(r.Diagnostics, Diagnostic{
		Severity: SeverityWarning, Stage: stage, Page: page, Message: msg,
	})
}

// DecodeFailureRate returns the document-wide rate.
func (r *Report) DecodeFailureRate() float64 {
	var glyphs, failures int
	for _, p := range r.Pages {
		glyphs += p.Glyphs
		failures += p.DecodeFailures
	}
	if glyphs == 0 {
		return 0
	}
	return float64(failures) / float64(glyphs)
}

// MedianGlyphsPerPage returns the median across converted pages, which the
// scanned-document classifier in spec section 6 keys on.
func (r *Report) MedianGlyphsPerPage() float64 {
	if len(r.Pages) == 0 {
		return 0
	}
	v := make([]int, len(r.Pages))
	for i, p := range r.Pages {
		v[i] = p.Glyphs
	}
	sort.Ints(v)
	n := len(v)
	if n%2 == 1 {
		return float64(v[n/2])
	}
	return float64(v[n/2-1]+v[n/2]) / 2
}

// Finish computes the quality score. It runs at the end of Write.
//
// The score starts at 100 and subtracts for the failure modes that most
// often indicate output worth reviewing by hand. It is a triage signal, not a
// measurement: the TUI uses it to decide which conversions to surface.
func (r *Report) Finish() {
	score := 100.0

	// Decode failures are the clearest signal that text came out wrong. A 5%
	// failure rate costs the full 40 points allotted here.
	score -= math.Min(40, r.DecodeFailureRate()*800)

	// Warnings each cost a little, capped so a chatty document does not floor
	// the score on its own.
	score -= math.Min(20, float64(r.Warnings())*2)

	// A document that produced no content at all is not usable output,
	// whatever else went right. Figures count: an image-only PDF converts to
	// an image-only EPUB, and penalizing it for having no prose would flag a
	// faithful conversion as a bad one.
	if r.Blocks[KindParagraph] == 0 && r.Blocks[KindFigure] == 0 {
		score -= 30
	}

	// Pages that yielded neither text nor images.
	empty := 0
	for _, p := range r.Pages {
		if p.Lines == 0 && p.Images == 0 {
			empty++
		}
	}
	if len(r.Pages) > 0 {
		score -= math.Min(20, float64(empty)/float64(len(r.Pages))*40)
	}

	if score < 0 {
		score = 0
	}
	r.QualityScore = int(math.Round(score))
}
