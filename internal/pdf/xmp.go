package pdf

import (
	"regexp"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// maxXMPBytes bounds the metadata stream read. XMP is a catalog-level
// description, not content, and a packet larger than this is a hostile input
// rather than a document.
const maxXMPBytes = 1 << 20

// dcLanguage matches the Dublin Core language element inside an XMP packet.
//
// XMP is RDF/XML and a conforming reader would parse it as such. This does
// not, and deliberately: decant wants one value out of it, spec section 4.6's
// third-choice language fallback, and running an XML parser over an untrusted
// packet to reach a single string buys risk rather than correctness. The two
// forms below are what producers actually emit.
//
//	<dc:language><rdf:Bag><rdf:li>en-GB</rdf:li></rdf:Bag></dc:language>
//	<dc:language>en-GB</dc:language>
//
// A namespace prefix other than dc: is legal but vanishingly rare, and
// guessing at arbitrary prefixes would match far more than it should.
var dcLanguage = regexp.MustCompile(
	`(?s)<dc:language[^>]*>(?:\s*<rdf:(?:Bag|Seq|Alt)[^>]*>\s*(?:<rdf:li[^>]*>)?)?([^<>]{1,64}?)\s*<`)

// xmpLanguage returns the document's dc:language from its XMP metadata
// stream, or "" when there is none.
//
// Spec section 4.1 lists XMP among the things the parse stage extracts, and
// section 4.6 gives it the one job decant needs it for: choosing a
// hyphenation pattern set when the catalog declares no /Lang. Without it a
// document carrying XMP metadata and no /Lang falls through to English
// patterns silently, which is the failure section 4.6 exists to avoid.
func xmpLanguage(xref *model.XRefTable) (lang string) {
	// pdfcpu panics on damaged streams, and metadata is never worth failing a
	// conversion over.
	defer func() {
		if r := recover(); r != nil {
			lang = ""
		}
	}()

	cat, err := xref.Catalog()
	if err != nil || cat == nil {
		return ""
	}
	sd, _, err := xref.DereferenceStreamDict(cat["Metadata"])
	if err != nil || sd == nil {
		return ""
	}
	if err := sd.Decode(); err != nil {
		return ""
	}
	data := sd.Content
	if len(data) == 0 || len(data) > maxXMPBytes {
		return ""
	}

	m := dcLanguage.FindSubmatch(data)
	if m == nil {
		return ""
	}
	// "x-default" is XMP's placeholder for an unspecified alternative, which
	// names no language and must not select a pattern set.
	v := strings.TrimSpace(string(m[1]))
	if v == "" || strings.EqualFold(v, "x-default") {
		return ""
	}
	return v
}
