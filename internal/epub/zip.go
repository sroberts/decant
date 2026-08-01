package epub

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
	"time"
)

// zipEntry is one file destined for the EPUB container.
type zipEntry struct {
	name string
	data []byte
	// store forces the entry to be written uncompressed, which the EPUB
	// specification requires for the mimetype entry.
	store bool
}

// deterministicZip writes entries with byte-identical output for identical
// input, independent of wall clock, worker count, or map iteration order.
//
// Three details make that work, per spec section 4.9:
//
//   - The mimetype entry is first and stored uncompressed.
//   - Remaining entries sort by name.
//   - Every header carries the same fixed MS-DOS timestamp and no extra
//     fields. Setting FileHeader.Modified would make archive/zip append an
//     Info-ZIP extended-timestamp extra field, so the MS-DOS date and time
//     fields are set directly instead.
//
// CreateRaw is used rather than Create so no data descriptor is emitted and
// the compressed bytes come from a flate writer at a pinned level.
func deterministicZip(w io.Writer, entries []zipEntry, modTime time.Time) error {
	// Sort everything but the mimetype, which must lead.
	rest := make([]zipEntry, 0, len(entries))
	var mimetype *zipEntry
	for i := range entries {
		if entries[i].name == "mimetype" {
			mimetype = &entries[i]
			continue
		}
		rest = append(rest, entries[i])
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].name < rest[j].name })

	ordered := make([]zipEntry, 0, len(entries))
	if mimetype != nil {
		ordered = append(ordered, *mimetype)
	}
	ordered = append(ordered, rest...)

	dosDate, dosTime := toMSDOS(modTime)

	zw := zip.NewWriter(w)
	for _, e := range ordered {
		fh := &zip.FileHeader{
			Name:         e.name,
			ModifiedDate: dosDate,
			ModifiedTime: dosTime,
			// Deprecated in favor of Modified, but they are the only way to
			// set a timestamp without triggering the extra field.
		}
		fh.SetMode(0o644)

		var raw []byte
		if e.store {
			fh.Method = zip.Store
			raw = e.data
		} else {
			fh.Method = zip.Deflate
			var buf bytes.Buffer
			fw, err := flate.NewWriter(&buf, flate.BestCompression)
			if err != nil {
				return fmt.Errorf("flate writer for %s: %w", e.name, err)
			}
			if _, err := fw.Write(e.data); err != nil {
				return fmt.Errorf("compress %s: %w", e.name, err)
			}
			if err := fw.Close(); err != nil {
				return fmt.Errorf("finish %s: %w", e.name, err)
			}
			raw = buf.Bytes()
		}

		fh.CRC32 = crc32.ChecksumIEEE(e.data)
		fh.CompressedSize64 = uint64(len(raw))
		fh.UncompressedSize64 = uint64(len(e.data))

		ew, err := zw.CreateRaw(fh)
		if err != nil {
			return fmt.Errorf("create %s: %w", e.name, err)
		}
		if _, err := ew.Write(raw); err != nil {
			return fmt.Errorf("write %s: %w", e.name, err)
		}
	}
	return zw.Close()
}

// toMSDOS converts a time to the packed MS-DOS date and time fields. The
// format covers 1980 through 2107 at two-second resolution; times outside
// that range clamp to the epoch so output stays valid and deterministic.
func toMSDOS(t time.Time) (date, tm uint16) {
	t = t.UTC()
	if t.Year() < 1980 || t.Year() > 2107 {
		t = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	date = uint16(t.Day()) |
		uint16(t.Month())<<5 |
		uint16(t.Year()-1980)<<9
	tm = uint16(t.Second()/2) |
		uint16(t.Minute())<<5 |
		uint16(t.Hour())<<11
	return date, tm
}
