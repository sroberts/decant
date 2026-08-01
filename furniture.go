package decant

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"
)

// folioPattern matches the page-number forms that appear in running heads and
// feet: bare digits, roman numerals, and the "Page N of M" family.
//
// Spec section 4.5 removes these independently of the repetition test,
// because a folio's text differs on every page and so never repeats.
var folioPattern = regexp.MustCompile(
	`^(?i)\s*(?:` +
		`[0-9]+` + // 12
		`|[ivxlcdm]+` + // xii
		`|page\s+[0-9]+(?:\s*(?:of|/)\s*[0-9]+)?` + // Page 3 of 40
		`|[0-9]+\s*(?:of|/)\s*[0-9]+` + // 3 of 40
		`|[-–—\s]*[0-9]+[-–—\s]*` + // - 12 -
		`)\s*$`)

// digitRun collapses digit sequences so that "Chapter 3" and "Chapter 4"
// hash alike, which is what makes a running head with a chapter number
// repeat detectably.
var digitRun = regexp.MustCompile(`[0-9]+`)

// removeFurniture drops running heads and folios, per spec section 4.5.
//
// The repetition test samples pages rather than scanning all of them: on a
// long document the sample is enough to identify a running head, and the cost
// is bounded. Blocks are then removed across every page, not only the sampled
// ones.
//
// It returns the surviving blocks and features, which stay index-aligned.
func (c *Converter) removeFurniture(
	blocks []Block,
	feats []blockFeatures,
	pageHeights map[int]float64,
	pageCount int,
	rep *Report,
) ([]Block, []blockFeatures) {
	h := c.opts.Heuristics

	if c.opts.KeepHeaders {
		rep.info("furniture", -1, "running heads retained by --keep-headers")
		return blocks, feats
	}
	// Spec 4.5 skips removal entirely on short documents, where a repeated
	// line is more likely to be real content than a running head.
	if pageCount < h.FurnitureMinPages {
		return blocks, feats
	}

	// Collect the pages that actually produced blocks, in order.
	var pages []int
	seenPage := map[int]bool{}
	for _, b := range blocks {
		if !seenPage[b.Page] {
			seenPage[b.Page] = true
			pages = append(pages, b.Page)
		}
	}
	if len(pages) < h.FurnitureMinPages {
		return blocks, feats
	}

	sample := evenSample(pages, h.FurnitureSamplePages)
	inSample := make(map[int]bool, len(sample))
	for _, p := range sample {
		inSample[p] = true
	}

	// Count, per hash, how many distinct sampled pages carry it. Counting
	// pages rather than blocks keeps a page that repeats a phrase twice from
	// looking like two pages' worth of evidence.
	//
	// Two keys are tracked. The text hash is the rule in spec section 4.5.
	// The position key is an addition: section 4.5 assumes a constant running
	// head, but a book with per-chapter heads writes different text on every
	// page and its hash never repeats. What does repeat is the position, to
	// the point, in the margin band. Requiring the box to fall *entirely*
	// inside the band is what keeps this off body text, whose first line
	// starts near the band but extends past it.
	pagesByHash := map[string]map[int]bool{}
	pagesByPos := map[string]map[int]bool{}

	for i, b := range blocks {
		if !inSample[b.Page] || feats[i].isFigure {
			continue
		}
		if !inFurnitureBand(b, pageHeights[b.Page], h.FurnitureBandRatio) {
			continue
		}
		if key := furnitureHash(b.Text); key != "" {
			if pagesByHash[key] == nil {
				pagesByHash[key] = map[int]bool{}
			}
			pagesByHash[key][b.Page] = true
		}
		pkey := positionKey(b)
		if pagesByPos[pkey] == nil {
			pagesByPos[pkey] = map[int]bool{}
		}
		pagesByPos[pkey][b.Page] = true
	}

	threshold := int(math.Ceil(h.FurnitureRepeatRatio * float64(len(sample))))
	if threshold < 2 {
		threshold = 2
	}
	repeated := map[string]bool{}
	for key, seen := range pagesByHash {
		if len(seen) >= threshold {
			repeated[key] = true
		}
	}
	repeatedPos := map[string]bool{}
	for key, seen := range pagesByPos {
		if len(seen) >= threshold {
			repeatedPos[key] = true
		}
	}

	outBlocks := blocks[:0:0]
	outFeats := feats[:0:0]
	removedRepeat, removedPos, removedFolio := 0, 0, 0
	var samples []string

	for i, b := range blocks {
		drop := false
		if !feats[i].isFigure && inFurnitureBand(b, pageHeights[b.Page], h.FurnitureBandRatio) {
			switch {
			case repeated[furnitureHash(b.Text)]:
				drop, removedRepeat = true, removedRepeat+1
			case folioPattern.MatchString(b.Text):
				drop, removedFolio = true, removedFolio+1
			case repeatedPos[positionKey(b)]:
				drop, removedPos = true, removedPos+1
			}
		}
		if drop {
			if len(samples) < 3 {
				samples = append(samples, strings.TrimSpace(b.Text))
			}
			continue
		}
		outBlocks = append(outBlocks, b)
		outFeats = append(outFeats, feats[i])
	}

	total := removedRepeat + removedPos + removedFolio
	rep.FurnitureRemoved = total
	if total > 0 {
		rep.info("furniture", -1, fmt.Sprintf(
			"removed %d block(s) from the page margins across %d sampled pages: "+
				"%d repeating by text, %d repeating by position, %d page numbers "+
				"(e.g. %s)",
			total, len(sample), removedRepeat, removedPos, removedFolio,
			quoteList(samples)))
	}
	return outBlocks, outFeats
}

// quoteList renders a few sample strings for a diagnostic.
func quoteList(items []string) string {
	parts := make([]string, 0, len(items))
	for _, s := range items {
		parts = append(parts, fmt.Sprintf("%q", s))
	}
	return strings.Join(parts, ", ")
}

// positionKey identifies a block by where it sits in the margin band.
//
// Coordinates round to the point: a running head is typeset at the same
// position on every page, but sub-point differences would otherwise split one
// head across several keys.
func positionKey(b Block) string {
	return fmt.Sprintf("%.0f:%.0f:%.0f",
		math.Round(b.Bounds.MinY), math.Round(b.Bounds.MaxY),
		math.Round(b.Bounds.MinX))
}

// inFurnitureBand reports whether a block sits entirely within the top or
// bottom band of its page.
//
// Spec section 4.5 requires the whole bounding box to fall inside the band, so
// a first line of body text that merely starts high does not qualify.
func inFurnitureBand(b Block, pageHeight, bandRatio float64) bool {
	if pageHeight <= 0 {
		return false
	}
	band := pageHeight * bandRatio
	if b.Bounds.MaxY <= band {
		return true
	}
	return b.Bounds.MinY >= pageHeight-band
}

// furnitureHash digest-normalizes a block's text for the repetition test.
//
// Digits collapse to a single marker so a running head carrying a page or
// chapter number still matches itself across pages, which is the whole point
// of hashing rather than comparing text.
func furnitureHash(text string) string {
	s := strings.TrimSpace(text)
	if s == "" {
		return ""
	}
	s = digitRun.ReplaceAllString(s, "#")
	s = strings.ToLower(s)
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	}), " ")
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
