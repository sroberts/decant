package decant_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sroberts/decant"
)

// --- shareable device profiles ---

func loadProfile(t *testing.T, body string) *decant.ProfileDoc {
	t.Helper()
	d, err := decant.LoadProfileDoc(strings.NewReader(body))
	if err != nil {
		t.Fatalf("LoadProfileDoc(%s): %v", body, err)
	}
	return d
}

func applied(t *testing.T, body string) decant.Options {
	t.Helper()
	d := loadProfile(t, body)
	o := decant.DefaultOptions()
	if d.Base != "" {
		o.Profile = d.Base
	}
	o.ApplyProfileDefaults()
	if err := o.ApplyProfileDoc(d); err != nil {
		t.Fatalf("ApplyProfileDoc: %v", err)
	}
	return o
}

func TestProfileOverridesOnlyWhatItNames(t *testing.T) {
	// A document states what it changes; everything else keeps the base
	// profile's value. Otherwise a short file would silently zero the rest.
	o := applied(t, `{"name":"kobo","base":"crosspoint","options":{"ImageMaxWidth":1264}}`)

	if o.ImageMaxWidth != 1264 {
		t.Errorf("ImageMaxWidth = %d, want 1264", o.ImageMaxWidth)
	}
	if o.Images != decant.ImagesGrayscale {
		t.Errorf("Images = %q; the crosspoint base should have survived", o.Images)
	}
	if o.Tables != decant.TableText {
		t.Errorf("Tables = %q; the crosspoint base should have survived", o.Tables)
	}
}

func TestProfileOverridesHeuristics(t *testing.T) {
	o := applied(t, `{"name":"x","heuristics":{"HeadingSizeRatio":0.25}}`)
	if o.Heuristics.HeadingSizeRatio != 0.25 {
		t.Errorf("HeadingSizeRatio = %v, want 0.25", o.Heuristics.HeadingSizeRatio)
	}
	// An unnamed threshold keeps its default.
	if o.Heuristics.ColumnMinLines != decant.DefaultHeuristics().ColumnMinLines {
		t.Error("an unnamed heuristic was reset")
	}
}

func TestProfileBaseDefaultsToStandard(t *testing.T) {
	o := applied(t, `{"name":"x"}`)
	if o.Images != decant.DefaultOptions().Images {
		t.Errorf("Images = %q, want the standard default", o.Images)
	}
}

func TestProfileRejectsUnknownKeys(t *testing.T) {
	// A typo in a threshold would otherwise look like a threshold that did
	// nothing at all.
	if _, err := decant.LoadProfileDoc(strings.NewReader(
		`{"name":"x","colour":"red"}`)); err == nil {
		t.Error("an unknown top-level key was accepted")
	}

	d := loadProfile(t, `{"name":"x","heuristics":{"HedingSizeRatio":0.25}}`)
	o := decant.DefaultOptions()
	if err := o.ApplyProfileDoc(d); err == nil {
		t.Error("a misspelled heuristic was accepted")
	}
}

func TestProfileRejectsUnknownOption(t *testing.T) {
	d := loadProfile(t, `{"name":"x","options":{"Titel":"nope"}}`)
	o := decant.DefaultOptions()
	err := o.ApplyProfileDoc(d)
	if err == nil {
		t.Fatal("an unknown option was accepted")
	}
	if !strings.Contains(err.Error(), "unknown option") {
		t.Errorf("a typo reported as something else: %v", err)
	}
}

func TestProfileRejectsPerConversionSettings(t *testing.T) {
	// Metadata in a shared profile would be applied silently to every book
	// converted with it.
	for _, k := range []string{"Metadata", "Pages", "Strict"} {
		d := loadProfile(t, `{"name":"x","options":{"`+k+`":null}}`)
		o := decant.DefaultOptions()
		err := o.ApplyProfileDoc(d)
		if err == nil {
			t.Errorf("%s was accepted in a profile", k)
			continue
		}
		if !strings.Contains(err.Error(), "one conversion") {
			t.Errorf("%s rejected with the wrong reason: %v", k, err)
		}
	}
}

func TestProfileRejectsUnknownBase(t *testing.T) {
	if _, err := decant.LoadProfileDoc(strings.NewReader(
		`{"name":"x","base":"kindle"}`)); err == nil {
		t.Error("an unknown base was accepted")
	}
}

func TestProfileRoundTrips(t *testing.T) {
	// The dump is the documented starting point, so it has to load back.
	for _, p := range []decant.Profile{
		decant.ProfileStandard, decant.ProfileCrossPoint, decant.ProfileMinimal,
	} {
		var buf bytes.Buffer
		if err := decant.WriteProfileDoc(&buf, p); err != nil {
			t.Fatalf("WriteProfileDoc(%s): %v", p, err)
		}
		d, err := decant.LoadProfileDoc(&buf)
		if err != nil {
			t.Fatalf("dump of %s does not load back: %v", p, err)
		}

		want := decant.DefaultOptions()
		want.Profile = p
		want.ApplyProfileDefaults()

		got := decant.DefaultOptions()
		got.Profile = d.Base
		got.ApplyProfileDefaults()
		if err := got.ApplyProfileDoc(d); err != nil {
			t.Fatalf("applying the dump of %s: %v", p, err)
		}

		if got.Images != want.Images || got.ImageMaxWidth != want.ImageMaxWidth ||
			got.MaxChunkBytes != want.MaxChunkBytes || got.Tables != want.Tables {
			t.Errorf("%s did not round-trip:\n got %+v\nwant %+v",
				p, got.Images, want.Images)
		}
	}
}

func TestWriteProfileDocRejectsUnknownProfile(t *testing.T) {
	var buf bytes.Buffer
	if err := decant.WriteProfileDoc(&buf, decant.Profile("kindle")); err == nil {
		t.Error("dumped a profile that does not exist")
	}
}

func TestProfileDocDrivesAConversion(t *testing.T) {
	// End to end: a document is usable as an option set.
	d := loadProfile(t, `{"name":"x","base":"minimal","options":{"MaxChunkBytes":32768}}`)
	o := decant.DefaultOptions()
	o.Profile = d.Base
	o.ApplyProfileDefaults()
	if err := o.ApplyProfileDoc(d); err != nil {
		t.Fatal(err)
	}

	out, rep := buildDoc(t, simpleDoc(), o)
	if len(out) == 0 || rep.Chapters == 0 {
		t.Fatal("the profile produced no output")
	}
	if o.MaxChunkBytes != 32768 {
		t.Errorf("MaxChunkBytes = %d, want 32768", o.MaxChunkBytes)
	}
	// minimal drops images.
	if o.Images != decant.ImagesDrop {
		t.Errorf("Images = %q, want the minimal base's drop", o.Images)
	}
}

func TestOversizeProfileIsRefused(t *testing.T) {
	big := `{"name":"` + strings.Repeat("x", 2<<20) + `"}`
	if _, err := decant.LoadProfileDoc(strings.NewReader(big)); err == nil {
		t.Error("an oversize profile was accepted")
	}
}
