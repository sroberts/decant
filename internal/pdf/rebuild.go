package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
)

// Bounds on the repair pass. A rebuild reads the whole file into memory, which
// the streaming path deliberately avoids, so it refuses to do so for a file
// large enough to threaten spec section 9's budget. maxRebuildObjects caps the
// object table a hostile file can make decant allocate.
const (
	maxRebuildBytes   = 256 << 20
	maxRebuildObjects = 1 << 20
)

// objMarker matches "N G obj" at a token boundary.
//
// The leading class rather than a line anchor is deliberate: PDF permits CR,
// LF, or CRLF, and the corpus's damaged file uses bare CR throughout, which a
// multi-line anchor in Go's regexp does not treat as a line start. Anchoring
// on it finds two markers in that file instead of a hundred and seventy.
var objMarker = regexp.MustCompile(`(?:\A|[\r\n \t>])(\d{1,10})[ \t\r\n]+(\d{1,5})[ \t\r\n]+obj\b`)

// catalogMarker finds the document catalog, which the rebuilt trailer needs
// for /Root.
var catalogMarker = regexp.MustCompile(`/Type[ \t\r\n]*/Catalog\b`)

// rootRef reads "/Root N G R" out of an existing trailer.
var rootRef = regexp.MustCompile(`/Root[ \t\r\n]+(\d{1,10})[ \t\r\n]+(\d{1,5})[ \t\r\n]+R\b`)

// ErrUnrepairable reports that a rebuild found nothing usable.
var errUnrepairable = errors.New("no object markers found")

// rebuildXref repairs a document whose cross-reference table cannot be
// followed, per spec section 4.1.
//
// It scans the raw bytes for object markers, builds a fresh classic
// cross-reference table from the offsets, and appends it with a new trailer.
// The original bytes are left untouched ahead of it, so every object keeps the
// offset the new table records.
//
// This is the standard recovery: a damaged table is a damaged *index*, and the
// objects it indexes are usually all still present. The corpus's
// unreadablemetadata.pdf is the shape that motivates it — a linearized file
// whose trailer declares /Size 175 while its only subsection covers objects
// 175 through 266, leaving everything below 175 indexed nowhere.
func rebuildXref(r io.ReaderAt, size int64) ([]byte, error) {
	if size <= 0 || size > maxRebuildBytes {
		return nil, fmt.Errorf("file is %d bytes; refusing to rebuild above %d",
			size, maxRebuildBytes)
	}

	data := make([]byte, size)
	if _, err := io.ReadFull(io.NewSectionReader(r, 0, size), data); err != nil {
		return nil, fmt.Errorf("reading document for repair: %w", err)
	}

	// Later definitions win, which is what an incremental update means.
	offsets := map[int]int64{}
	gens := map[int]int{}
	for _, m := range objMarker.FindAllSubmatchIndex(data, -1) {
		num, err := strconv.Atoi(string(data[m[2]:m[3]]))
		if err != nil || num < 0 || num >= maxRebuildObjects {
			continue
		}
		gen, err := strconv.Atoi(string(data[m[4]:m[5]]))
		if err != nil || gen < 0 || gen > 65535 {
			continue
		}
		// The match may have consumed a leading delimiter; the object starts
		// at the number.
		offsets[num] = int64(m[2])
		gens[num] = gen
	}
	if len(offsets) == 0 {
		return nil, errUnrepairable
	}

	root, ok := findRoot(data, offsets)
	if !ok {
		return nil, errors.New("no document catalog found")
	}

	maxObj := 0
	for num := range offsets {
		if num > maxObj {
			maxObj = num
		}
	}
	size64 := maxObj + 1

	var out bytes.Buffer
	out.Grow(len(data) + 20*size64 + 256)
	out.Write(data)
	// A table appended straight after arbitrary trailing bytes has to start on
	// its own line.
	if n := out.Len(); n > 0 && data[n-1] != '\n' && data[n-1] != '\r' {
		out.WriteByte('\n')
	}

	xrefOffset := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", size64)
	// Object 0 heads the free list, by convention with generation 65535.
	out.WriteString("0000000000 65535 f \n")
	for num := 1; num < size64; num++ {
		off, present := offsets[num]
		if !present {
			// An object the scan never saw is free rather than absent, so the
			// table stays contiguous and every entry is exactly 20 bytes.
			out.WriteString("0000000000 65535 f \n")
			continue
		}
		fmt.Fprintf(&out, "%010d %05d n \n", off, gens[num])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %d %d R >>\nstartxref\n%d\n%%%%EOF\n",
		size64, root.num, root.gen, xrefOffset)

	return out.Bytes(), nil
}

type objRef struct{ num, gen int }

// findRoot locates the document catalog.
//
// An existing trailer's /Root is tried first, since it is authoritative when
// present and cheap to read; a file can have a broken table and an intact
// trailer. Failing that, the object bodies are scanned for /Type /Catalog.
func findRoot(data []byte, offsets map[int]int64) (objRef, bool) {
	if m := rootRef.FindSubmatch(data); m != nil {
		num, err1 := strconv.Atoi(string(m[1]))
		gen, err2 := strconv.Atoi(string(m[2]))
		if err1 == nil && err2 == nil {
			if _, ok := offsets[num]; ok {
				return objRef{num, gen}, true
			}
		}
	}

	// Scan object bodies. Objects are visited in number order so the choice
	// does not depend on map iteration.
	nums := make([]int, 0, len(offsets))
	for n := range offsets {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	for i, num := range nums {
		start := offsets[num]
		// Stop at the next object rather than at a fixed window, or a short
		// object's window spills into its neighbour and reports whichever
		// object happens to precede the catalog.
		end := int64(len(data))
		if i+1 < len(nums) {
			if next := offsets[nums[i+1]]; next > start && next < end {
				end = next
			}
		}
		if catalogMarker.Match(data[start:end]) {
			return objRef{num, 0}, true
		}
	}
	return objRef{}, false
}
