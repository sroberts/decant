package decant

import (
	"fmt"
	"math"
)

// resolveCrossRefs points each block's cross-references at the block its
// destination lands on, per spec section 4.9.
//
// This runs after IDs are assigned, for the same reason footnote linking
// does: a cross-reference names its target by anchor, and an anchor that does
// not exist yet cannot be named.
//
// The nearest-block rule matches outline reconciliation deliberately. A
// destination is a point on a page, and both features have to answer the same
// question about it: which block does this point name? Answering it two ways
// would let a heading's outline entry and a cross-reference to that heading
// disagree about where it is.
func resolveCrossRefs(
	blocks []Block,
	pageSpaceY map[int]func(float64) float64,
	rep *Report,
) {
	byPage := map[int][]int{}
	hasRefs := false
	for i := range blocks {
		byPage[blocks[i].Page] = append(byPage[blocks[i].Page], i)
		if len(blocks[i].Links) > 0 {
			hasRefs = true
		}
	}
	if !hasRefs {
		return
	}

	resolved, unresolved := 0, 0
	// targeted counts distinct destinations, which is what decides how many
	// anchors the renderer has to emit.
	targeted := map[string]bool{}

	for i := range blocks {
		for j := range blocks[i].Links {
			ref := &blocks[i].Links[j]

			// TargetY arrives in PDF user space. Converting it needs the
			// destination page's own base CTM, which is only available if
			// that page was processed: a link into a page outside --pages
			// has nothing to resolve against.
			toPage, ok := pageSpaceY[ref.TargetPage]
			if !ok {
				unresolved++
				continue
			}
			if !math.IsNaN(ref.TargetY) {
				ref.TargetY = toPage(ref.TargetY)
			}

			best := nearestBlockTo(blocks, byPage[ref.TargetPage], ref.TargetY)
			if best < 0 {
				unresolved++
				continue
			}
			ref.TargetID = blocks[best].ID
			targeted[ref.TargetID] = true
			resolved++
		}
	}

	if resolved > 0 {
		rep.info("assemble", -1, fmt.Sprintf(
			"rewrote %d internal cross-reference(s) against %d anchor(s)",
			resolved, len(targeted)))
	}
	if unresolved > 0 {
		// Not a warning. A link into a page decant dropped, or into front
		// matter outside the selected range, is a normal consequence of
		// options the caller chose, and the text still renders.
		rep.info("assemble", -1, fmt.Sprintf(
			"%d internal link(s) pointed at no block and render as plain text",
			unresolved))
	}
}

// nearestBlockTo returns the index of the block a destination point names, or
// -1 when the page holds none.
//
// The distance rule is the one reconcileOutline uses: a destination normally
// lands on the top edge of what it names, and a block's box starts an ascent
// above its baseline, so measuring to the box rather than to a coordinate is
// what makes a heading beat the paragraph beneath it. A block entirely above
// the destination is allowed but penalized, so anything at or below wins.
func nearestBlockTo(blocks []Block, candidates []int, y float64) int {
	best := -1
	bestDist := math.Inf(1)

	for _, i := range candidates {
		var dist float64
		b := blocks[i].Bounds
		switch {
		case math.IsNaN(y):
			dist = float64(i)
		case y < b.MinY:
			dist = b.MinY - y
		case y > b.MaxY:
			dist = (y - b.MaxY) + 1e6
		default:
			dist = 0
		}
		if dist < bestDist {
			best, bestDist = i, dist
		}
	}
	return best
}
