package decant_test

import (
	"strings"
	"testing"

	"github.com/sroberts/decant"
	"github.com/sroberts/decant/internal/testpdf"
)

// --- XMP language fallback, spec 4.1 and 4.6 ---

func langDoc(lang, xmp string) []byte {
	b := testpdf.New().AddPage(612, 792, testpdf.TextPage("F1", 12, 72, 720, 16, []string{
		"Ein Absatz mit einem getrennten Wort am Zeilen-",
		"ende, damit die Silbentrennung etwas zu tun hat.",
	}))
	if lang != "" {
		b = b.SetLang(lang)
	}
	if xmp != "" {
		b = b.SetXMP(xmp)
	}
	return b.Build()
}

func TestXMPLanguageIsRead(t *testing.T) {
	// Spec 4.6 orders the sources: --language, then /Lang, then XMP
	// dc:language, then en-us. The third step was documented in the code and
	// never implemented, so a document like this fell through to English.
	src := langDoc("", "<dc:language><rdf:Bag><rdf:li>de</rdf:li></rdf:Bag></dc:language>")

	info, err := decant.Meta(contextTODO(), strings.NewReader(string(src)), int64(len(src)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Language != "de" {
		t.Errorf("Language = %q, want %q", info.Language, "de")
	}
}

func TestXMPLanguageBareForm(t *testing.T) {
	// Producers emit dc:language with and without the rdf container.
	src := langDoc("", "<dc:language>fr</dc:language>")
	info, err := decant.Meta(contextTODO(), strings.NewReader(string(src)), int64(len(src)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Language != "fr" {
		t.Errorf("Language = %q, want %q", info.Language, "fr")
	}
}

func TestCatalogLangBeatsXMP(t *testing.T) {
	// The catalog is the PDF's own statement about itself; XMP is a packet
	// tooling copies between files and lets go stale.
	src := langDoc("es", "<dc:language><rdf:Bag><rdf:li>de</rdf:li></rdf:Bag></dc:language>")
	info, err := decant.Meta(contextTODO(), strings.NewReader(string(src)), int64(len(src)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Language != "es" {
		t.Errorf("Language = %q, want the catalog's %q", info.Language, "es")
	}
}

func TestXMPDefaultLanguageIsIgnored(t *testing.T) {
	// x-default is XMP's placeholder for an unspecified alternative. It names
	// no language and must not select a pattern set.
	src := langDoc("", "<dc:language><rdf:Alt><rdf:li>x-default</rdf:li></rdf:Alt></dc:language>")
	info, err := decant.Meta(contextTODO(), strings.NewReader(string(src)), int64(len(src)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Language != "" {
		t.Errorf("Language = %q, want empty for x-default", info.Language)
	}
}

func TestNoMetadataLeavesLanguageEmpty(t *testing.T) {
	src := langDoc("", "")
	info, err := decant.Meta(contextTODO(), strings.NewReader(string(src)), int64(len(src)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Language != "" {
		t.Errorf("Language = %q, want empty", info.Language)
	}
}

func TestXMPLanguageSelectsThePatternSet(t *testing.T) {
	// The point of reading it: a German document with XMP metadata and no
	// /Lang used to dehyphenate with English patterns, which spec 4.6
	// explicitly wants avoided.
	src := langDoc("", "<dc:language><rdf:Bag><rdf:li>de</rdf:li></rdf:Bag></dc:language>")
	_, rep := buildDoc(t, src, defaultOpts())

	if got := rep.Hyphenation.Language; !strings.HasPrefix(got, "de") {
		t.Errorf("hyphenation language = %q, want German", got)
	}
}

func TestMalformedXMPIsIgnored(t *testing.T) {
	// Metadata is never worth failing a conversion over.
	for _, body := range []string{
		"<dc:language>",
		"<dc:language></dc:language>",
		"not xml at all <<<>>>",
		strings.Repeat("<dc:x>", 500),
	} {
		src := langDoc("", body)
		if _, err := decant.Meta(contextTODO(), strings.NewReader(string(src)), int64(len(src))); err != nil {
			t.Errorf("Meta failed on XMP %q: %v", body, err)
		}
	}
}
