package decant

import (
	"errors"
	"fmt"

	"github.com/sroberts/decant/internal/hyphen"
	"github.com/sroberts/decant/internal/layout"
)

// dehyphenator adapts a hyphen.Hyphenator to the layout package's interface,
// keeping the internal hyphenation types out of the layout API.
type dehyphenator struct{ h *hyphen.Hyphenator }

// JoinFragments implements layout.Dehyphenator.
func (d dehyphenator) JoinFragments(left, right string) (bool, string) {
	dec := d.h.Join(left, right)
	return dec.Drop, dec.Reason
}

// resolveDehyphenator picks the pattern set for a document.
//
// Spec section 4.6 orders the sources: the --language flag, then the PDF
// /Lang entry, then XMP dc:language, then en-us. When no set ships for the
// resolved language, dehyphenation is disabled and the report says so, rather
// than guessing with English patterns against a language they do not
// describe.
func (c *Converter) resolveDehyphenator(lang string, rep *Report) layout.Dehyphenator {
	if c.opts.NoDehyphenate {
		rep.info("classify", -1,
			"dehyphenation disabled by --no-dehyphenate; line-break hyphens are preserved")
		return nil
	}

	h, err := hyphen.For(lang)
	if err == nil {
		rep.Hyphenation.Language = h.Language
		rep.Hyphenation.Patterns = h.PatternCount()
		return dehyphenator{h: h}
	}

	var missing *hyphen.ErrNoPatterns
	if errors.As(err, &missing) {
		rep.warn("classify", -1, fmt.Sprintf(
			"no hyphenation patterns ship for %q, so dehyphenation is disabled; "+
				"line-break hyphens are preserved verbatim (shipped: %v)",
			lang, hyphen.Languages()))
	} else {
		rep.warn("classify", -1, fmt.Sprintf(
			"hyphenation patterns for %q failed to load, so dehyphenation is "+
				"disabled: %v", lang, err))
	}
	return nil
}
