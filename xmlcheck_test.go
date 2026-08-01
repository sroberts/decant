package decant_test

import (
	"encoding/xml"
	"io"
	"strings"
)

// checkWellFormed parses a document with encoding/xml purely to prove it is
// well formed. epubcheck is the real gate in CI; this catches escaping
// mistakes without needing Java on a developer's machine.
func checkWellFormed(s string) error {
	dec := xml.NewDecoder(strings.NewReader(s))
	// The XHTML doctype references no external entities, but the decoder
	// still needs to be told not to chase them.
	dec.Strict = true
	dec.Entity = xml.HTMLEntity
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
