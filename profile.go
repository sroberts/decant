package decant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
)

// maxProfileBytes bounds a profile document. A profile is a short list of
// settings; anything larger is a mistake or a hostile input.
const maxProfileBytes = 1 << 20

// ProfileDoc is a device profile in serializable form, so one can be written
// once and shared rather than compiled in.
//
// The three built-in profiles in spec section 5 stay compiled in, because
// their values encode findings this repository is responsible for: the
// crosspoint numbers come from reading the firmware. A document extends that
// set for a device decant has never seen, without a rebuild.
//
// Keys under Options and Heuristics are the field names of [Options] and
// [Heuristics], matched without regard to case. That is deliberate: the field
// documentation is then the format documentation, and `go doc decant.Options`
// is the reference. Unknown keys are an error rather than a silent no-op,
// since a typo in a threshold would otherwise look like a threshold that did
// nothing.
//
// A field the document omits keeps the value the base profile gave it, so a
// document states only what it changes.
type ProfileDoc struct {
	// Name identifies the profile. It is not matched against anything and
	// exists so a file says what it is.
	Name string `json:"name"`
	// Description is free text for whoever reads the file next.
	Description string `json:"description,omitempty"`

	// Base names the built-in profile to start from. Empty means standard.
	Base Profile `json:"base,omitempty"`

	// Options overrides fields of [Options]; Heuristics overrides fields of
	// [Heuristics]. Both are applied over the base profile's defaults.
	Options    json.RawMessage `json:"options,omitempty"`
	Heuristics json.RawMessage `json:"heuristics,omitempty"`
}

// LoadProfileDoc reads a profile document.
//
// Only the document's own shape is checked here. Whether its values make a
// usable option set is decided by [Options.ApplyProfileDoc] and [New].
func LoadProfileDoc(r io.Reader) (*ProfileDoc, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxProfileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading profile: %w", err)
	}
	if len(data) > maxProfileBytes {
		return nil, fmt.Errorf("profile is larger than %d bytes", maxProfileBytes)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var d ProfileDoc
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("parsing profile: %w", err)
	}
	if d.Base != "" && !d.Base.Valid() {
		return nil, fmt.Errorf("profile %q has unknown base %q", d.Name, d.Base)
	}
	return &d, nil
}

// ApplyProfileDoc layers a document's overrides onto o.
//
// It applies Options and Heuristics only. The document's Base is advisory and
// the caller chooses the starting point, because the caller is the only one
// that knows whether a profile was also named explicitly:
//
//	o := DefaultOptions()
//	o.Profile = doc.Base          // or an explicit choice, which wins
//	o.ApplyProfileDefaults()
//	err := o.ApplyProfileDoc(doc)
//
// That order gives defaults, then base profile, then document, then whatever
// the caller sets afterwards.
func (o *Options) ApplyProfileDoc(d *ProfileDoc) error {
	if d == nil {
		return nil
	}

	// Decoding onto the existing values is what makes omission mean "leave
	// it alone": encoding/json writes only the fields the document names.
	if len(d.Options) > 0 {
		if err := checkProfileOptionKeys(d.Options); err != nil {
			return fmt.Errorf("profile %q: %w", d.Name, err)
		}
		if err := decodeOnto(d.Options, o); err != nil {
			return fmt.Errorf("profile %q: options: %w", d.Name, err)
		}
	}
	if len(d.Heuristics) > 0 {
		if err := decodeOnto(d.Heuristics, &o.Heuristics); err != nil {
			return fmt.Errorf("profile %q: heuristics: %w", d.Name, err)
		}
	}
	return nil
}

// decodeOnto unmarshals raw over an existing value, rejecting unknown keys.
func decodeOnto(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// profileOptionFields are the [Options] fields a profile may set.
//
// The rest describe one conversion rather than one device: an output path, a
// page range, or a title override has no place in a file shared between
// libraries, and metadata written into a profile would be silently applied to
// every book converted with it.
var profileOptionFields = map[string]bool{
	"Images":          true,
	"KeepSmallImages": true,
	"ImageMaxWidth":   true,
	"MaxChunkBytes":   true,
	"SplitAt":         true,
	"Tables":          true,
	"KeepHeaders":     true,
	"NoDehyphenate":   true,
	"Columns":         true,
}

// checkProfileOptionKeys rejects per-conversion settings in a device profile.
func checkProfileOptionKeys(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return fmt.Errorf("options is not an object: %w", err)
	}
	for k := range keys {
		if profileOptionFieldAllowed(k) {
			continue
		}
		// A typo and a deliberate per-conversion setting need different
		// answers: one is a mistake in the file, the other is a
		// misunderstanding of what a profile is for.
		if optionsFieldExists(k) {
			return fmt.Errorf("option %q describes one conversion rather than "+
				"one device and cannot be set by a profile", k)
		}
		return fmt.Errorf("unknown option %q; see `go doc decant.Options` for "+
			"the field names a profile may set", k)
	}
	return nil
}

// optionsFieldExists reports whether k names a field of Options at all.
func optionsFieldExists(k string) bool {
	t := reflect.TypeOf(Options{})
	for i := 0; i < t.NumField(); i++ {
		if equalFoldASCII(t.Field(i).Name, k) {
			return true
		}
	}
	return false
}

func profileOptionFieldAllowed(k string) bool {
	for name := range profileOptionFields {
		if equalFoldASCII(name, k) {
			return true
		}
	}
	return false
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// WriteProfileDoc writes a built-in profile as a document, which is the
// starting point for adapting one to a new device.
func WriteProfileDoc(w io.Writer, p Profile) error {
	if !p.Valid() {
		return &UsageError{Err: fmt.Errorf("unknown profile %q", p)}
	}

	o := DefaultOptions()
	o.Profile = p
	o.ApplyProfileDefaults()

	opts := map[string]any{
		"Images":          o.Images,
		"KeepSmallImages": o.KeepSmallImages,
		"ImageMaxWidth":   o.ImageMaxWidth,
		"MaxChunkBytes":   o.MaxChunkBytes,
		"SplitAt":         o.SplitAt,
		"Tables":          o.Tables,
		"KeepHeaders":     o.KeepHeaders,
		"NoDehyphenate":   o.NoDehyphenate,
		"Columns":         o.Columns,
	}
	optsRaw, err := json.Marshal(opts)
	if err != nil {
		return err
	}
	heurRaw, err := json.Marshal(o.Heuristics)
	if err != nil {
		return err
	}

	doc := ProfileDoc{
		Name: string(p),
		Description: fmt.Sprintf(
			"Dumped from decant's built-in %s profile. Every field is optional; "+
				"delete the ones you do not want to change.", p),
		Base:       p,
		Options:    optsRaw,
		Heuristics: heurRaw,
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
