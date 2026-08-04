// Command decant converts fixed-layout PDF to reflowable EPUB 3.
//
// It is a thin wrapper over the decant package: this file parses flags and
// maps errors to exit codes, and holds no conversion logic.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/sroberts/decant"
)

// Exit codes, from spec section 3.
const (
	exitOK          = 0
	exitRuntime     = 1
	exitUsage       = 2
	exitEncrypted   = 3
	exitNoTextLayer = 4
	exitStrict      = 5
	exitMalformed   = 6
)

// version is set by the linker at release time. It is empty otherwise, so
// buildVersion can fall back to what the toolchain recorded.
var version = ""

// buildVersion reports the binary's version.
//
// Three sources, in order. A linker-set value wins, which is how a release
// build stamps an exact string. Otherwise the module version the toolchain
// embedded is used, which is what makes "go install ...@v1.0.0" report
// v1.0.0 rather than a placeholder. A build from a working tree has no module
// version, so the VCS revision stands in, suffixed when the tree was dirty.
func buildVersion() string {
	if version != "" {
		return version
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}

	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "devel"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		return "devel-" + rev + "-dirty"
	}
	return "devel-" + rev
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}

	verb, rest := args[0], args[1:]
	switch verb {
	case "convert":
		return exitCode(cmdConvert(ctx, rest, stdout, stderr), stderr)
	case "probe":
		return exitCode(cmdProbe(ctx, rest, stdout, stderr), stderr)
	case "meta":
		return exitCode(cmdMeta(ctx, rest, stdout, stderr), stderr)
	case "profile":
		return exitCode(cmdProfile(rest, stdout, stderr), stderr)
	case "version":
		fmt.Fprintf(stdout, "decant %s (%s %s/%s)\n",
			buildVersion(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return exitOK
	case "-h", "--help", "help":
		usage(stdout)
		return exitOK
	default:
		if strings.HasPrefix(verb, "-") {
			// Flags with no verb mean the default verb with flags, e.g.
			// "decant -o out.epub book.pdf".
			return exitCode(cmdConvert(ctx, args, stdout, stderr), stderr)
		}
		// "decant book.pdf" is "decant convert book.pdf".
		return exitCode(cmdConvert(ctx, args, stdout, stderr), stderr)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `decant converts fixed-layout PDF to reflowable EPUB 3.

Usage:
  decant convert <input.pdf> [-o output.epub] [flags]
  decant probe   <input.pdf> [--stage=glyphs|lines|blocks|structure] [--page=N] [--json]
  decant meta    <input.pdf> [--json]
  decant profile [--dump standard|crosspoint|minimal] [-o file]
  decant version

convert is the default verb, so "decant book.pdf" writes book.epub.

Run "decant convert -h" for the full flag list.

Exit codes:
  0  success
  1  runtime failure
  2  usage error
  3  encrypted PDF (unsupported)
  4  no usable text layer (scanned document)
  5  converted with warnings and --strict was set
  6  malformed PDF beyond repair
`)
}

// exitCode maps an error to a process exit status and reports it on stderr.
func exitCode(err error, stderr io.Writer) int {
	if err == nil {
		return exitOK
	}

	var enc *decant.EncryptedError
	var scan *decant.NoTextLayerError
	var mal *decant.MalformedError
	var use *decant.UsageError
	var strict *strictError

	switch {
	case errors.As(err, &strict):
		fmt.Fprintf(stderr, "decant: %v\n", strict)
		return exitStrict
	case errors.As(err, &enc):
		fmt.Fprintf(stderr, "error: %v\n", enc)
		return exitEncrypted
	case errors.As(err, &scan):
		fmt.Fprintf(stderr, "error: %v\n", scan)
		return exitNoTextLayer
	case errors.As(err, &mal):
		fmt.Fprintf(stderr, "error: %v\n", mal)
		return exitMalformed
	case errors.As(err, &use):
		fmt.Fprintf(stderr, "usage error: %v\n", use)
		return exitUsage
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(stderr, "decant: interrupted")
		return exitRuntime
	default:
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitRuntime
	}
}

// parseArgs parses flags that may appear before, after, or between positional
// arguments. The stdlib flag package stops at the first non-flag argument, so
// "decant convert book.pdf -o out.epub" would otherwise fail. Re-parsing what
// is left after each positional lets flag apply its own knowledge of which
// flags take a value.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
}

func usageError(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return &decant.UsageError{Err: err}
}

// strictError signals that --strict was set and warnings were recorded.
type strictError struct {
	warnings int
	score    int
}

func (e *strictError) Error() string {
	return fmt.Sprintf("converted with %d warning(s), quality score %d; --strict was set",
		e.warnings, e.score)
}
