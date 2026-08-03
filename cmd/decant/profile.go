package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/sroberts/decant"
)

// cmdProfile writes a built-in device profile as a JSON document.
//
// It exists so adapting decant to a device it has never seen does not require
// reading the source: dump the closest built-in, edit the values, and pass
// the result to convert with --profile-file.
func cmdProfile(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("profile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: decant profile [--dump <name>] [-o file]\n\n")
		fmt.Fprintf(stderr, "Write a built-in profile as a document to adapt.\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(stderr, "\nApply one with:\n  decant convert book.pdf --profile-file mine.json\n")
	}

	dump := fs.String("dump", "standard", "built-in profile to write: standard, crosspoint, minimal")
	out := fs.String("o", "-", "output path; - writes to stdout")

	if err := fs.Parse(args); err != nil {
		return &decant.UsageError{Err: err}
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return &decant.UsageError{
			Err: fmt.Errorf("unexpected argument %q", fs.Arg(0)),
		}
	}

	w := stdout
	if *out != "-" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}

	if err := decant.WriteProfileDoc(w, decant.Profile(*dump)); err != nil {
		return err
	}
	if *out != "-" {
		fmt.Fprintf(stderr, "decant: wrote %s\n", *out)
	}
	return nil
}
