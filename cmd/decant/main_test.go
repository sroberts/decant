package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sroberts/decant/internal/testpdf"
)

// writeFixture writes a small valid PDF into a temp dir and returns its path.
// writeTableFixture writes a PDF carrying a ruled table, for the flags whose
// behaviour only shows up when a table is present.
func writeTableFixture(t *testing.T) string {
	t.Helper()
	body := testpdf.TextPage("F1", 11, 72, 720, 14, []string{
		"An introductory paragraph sitting above the table, long enough that",
		"the body font is identified from prose rather than from cell text.",
	})
	rows := [][]testpdf.TableCell{
		{{Text: "Region"}, {Text: "Revenue"}, {Text: "Growth"}},
		{{Text: "Northeast"}, {Text: "412000"}, {Text: "12 pct"}},
		{{Text: "Midwest"}, {Text: "298000"}, {Text: "4 pct"}},
		{{Text: "Pacific"}, {Text: "530000"}, {Text: "19 pct"}},
	}
	data := testpdf.New().
		AddPage(612, 792, body+testpdf.RuledTable("F1", 11, 72, 660, 90, 20, rows)).
		Build()

	path := filepath.Join(t.TempDir(), "table.pdf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

func writeFixture(t *testing.T) string {
	t.Helper()
	content := testpdf.TextPage("F1", 12, 72, 720, 15, []string{
		"A line of body text for the command line tests.",
		"A second line so paragraph reconstruction has work to do.",
	})
	data := testpdf.New().
		SetInfo("Title", "CLI Fixture").
		SetInfo("Author", "Tester").
		AddPage(612, 792, content).
		Build()

	path := filepath.Join(t.TempDir(), "fixture.pdf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

// runCLI invokes the command and returns its exit code plus captured output.
func runCLI(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestVersionSubcommand(t *testing.T) {
	code, stdout, _ := runCLI("version")
	if code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(stdout, "decant ") {
		t.Errorf("stdout = %q, want a version line", stdout)
	}
}

func TestNoArgsIsUsageError(t *testing.T) {
	code, _, stderr := runCLI()
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr does not show usage:\n%s", stderr)
	}
}

func TestConvertWritesEPUB(t *testing.T) {
	in := writeFixture(t)
	out := filepath.Join(t.TempDir(), "out.epub")

	code, _, stderr := runCLI("convert", in, "-o", out)
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, exitOK, stderr)
	}

	st, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output was not written: %v", err)
	}
	if st.Size() == 0 {
		t.Error("output file is empty")
	}
	if !strings.Contains(stderr, "quality score") {
		t.Errorf("summary was not printed:\n%s", stderr)
	}
}

func TestFlagsAfterPositionalArgument(t *testing.T) {
	// The stdlib flag package stops at the first positional; parseArgs must
	// recover the flags that follow it.
	in := writeFixture(t)
	out := filepath.Join(t.TempDir(), "out.epub")

	code, _, stderr := runCLI("convert", in, "-o", out, "--profile", "crosspoint", "--quiet")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, exitOK, stderr)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output was not written: %v", err)
	}
	if strings.Contains(stderr, "quality score") {
		t.Error("--quiet after a positional argument was not applied")
	}
}

func TestDefaultVerbAndDefaultOutputPath(t *testing.T) {
	in := writeFixture(t)

	// "decant book.pdf" is "decant convert book.pdf -o book.epub".
	code, _, stderr := runCLI(in, "--quiet")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, exitOK, stderr)
	}
	want := strings.TrimSuffix(in, ".pdf") + ".epub"
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected default output at %s: %v", want, err)
	}
}

func TestMissingInputIsRuntimeError(t *testing.T) {
	code, _, _ := runCLI("convert", "/nonexistent/file.pdf")
	if code != exitRuntime {
		t.Errorf("exit code = %d, want %d", code, exitRuntime)
	}
}

func TestTooManyInputsIsUsageError(t *testing.T) {
	in := writeFixture(t)
	code, _, _ := runCLI("convert", in, in)
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestBadProfileIsUsageError(t *testing.T) {
	in := writeFixture(t)
	code, _, stderr := runCLI("convert", in, "--profile", "nonsense")
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, exitUsage, stderr)
	}
}

func TestBadPageRangeIsUsageError(t *testing.T) {
	in := writeFixture(t)
	code, _, _ := runCLI("convert", in, "--pages", "not-a-range")
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestEncryptedInputExitsThree(t *testing.T) {
	data := testpdf.New().AddPage(612, 792, "").Build()
	data = bytes.Replace(data,
		[]byte("trailer\n<< /Size"),
		[]byte("trailer\n<< /Encrypt 99 0 R /Size"), 1)

	path := filepath.Join(t.TempDir(), "encrypted.pdf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runCLI("convert", path)
	if code != exitEncrypted {
		t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, exitEncrypted, stderr)
	}
	if !strings.Contains(stderr, "encrypted") {
		t.Errorf("stderr does not explain the failure:\n%s", stderr)
	}
}

func TestMalformedInputExitsSix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.pdf")
	if err := os.WriteFile(path, []byte("not a pdf at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ := runCLI("convert", path)
	if code != exitMalformed {
		t.Errorf("exit code = %d, want %d", code, exitMalformed)
	}
}

func TestMetaSubcommand(t *testing.T) {
	in := writeFixture(t)

	code, stdout, _ := runCLI("meta", in)
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	for _, want := range []string{"CLI Fixture", "Tester", "Pages:", "SHA-256:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("meta output missing %q:\n%s", want, stdout)
		}
	}
}

func TestMetaJSON(t *testing.T) {
	in := writeFixture(t)
	code, stdout, _ := runCLI("meta", in, "--json")
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "{") {
		t.Errorf("--json did not emit JSON on stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"identifier"`) {
		t.Errorf("JSON is missing the identifier field:\n%s", stdout)
	}
}

func TestProbeStages(t *testing.T) {
	in := writeFixture(t)

	for _, stage := range []string{"glyphs", "lines", "blocks", "structure"} {
		code, stdout, stderr := runCLI("probe", in, "--stage", stage)
		if code != exitOK {
			t.Errorf("stage %s: exit code = %d\nstderr:\n%s", stage, code, stderr)
			continue
		}
		if !strings.Contains(stdout, "page 1") {
			t.Errorf("stage %s: output has no page header:\n%s", stage, stdout)
		}
	}
}

func TestProbeUnknownStageIsUsageError(t *testing.T) {
	in := writeFixture(t)
	code, _, _ := runCLI("probe", in, "--stage", "nonsense")
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestReportIsWritten(t *testing.T) {
	in := writeFixture(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.epub")
	report := filepath.Join(dir, "report.json")

	code, _, stderr := runCLI("convert", in, "-o", out, "--report", report, "--quiet")
	if code != exitOK {
		t.Fatalf("exit code = %d\nstderr:\n%s", code, stderr)
	}
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("report was not written: %v", err)
	}
	for _, want := range []string{`"quality_score"`, `"pages"`, `"blocks"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("report is missing %q:\n%s", want, b)
		}
	}
}

func TestSourceDateEpochIsHonored(t *testing.T) {
	in := writeFixture(t)
	dir := t.TempDir()
	a := filepath.Join(dir, "a.epub")
	b := filepath.Join(dir, "b.epub")

	t.Setenv("SOURCE_DATE_EPOCH", "1750000000")
	if code, _, stderr := runCLI("convert", in, "-o", a, "--quiet"); code != exitOK {
		t.Fatalf("first run failed: %d\n%s", code, stderr)
	}
	if code, _, stderr := runCLI("convert", in, "-o", b, "--quiet"); code != exitOK {
		t.Fatalf("second run failed: %d\n%s", code, stderr)
	}

	ba, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if !bytes.Equal(ba, bb) {
		t.Error("output differed between runs at a fixed SOURCE_DATE_EPOCH")
	}

	// A different epoch must change the bytes, proving the value is used.
	c := filepath.Join(dir, "c.epub")
	t.Setenv("SOURCE_DATE_EPOCH", "1600000000")
	if code, _, _ := runCLI("convert", in, "-o", c, "--quiet"); code != exitOK {
		t.Fatal("third run failed")
	}
	bc, _ := os.ReadFile(c)
	if bytes.Equal(ba, bc) {
		t.Error("SOURCE_DATE_EPOCH had no effect on the output")
	}
}

func TestReservedFlagsWarn(t *testing.T) {
	// A flag accepted but not acted on has to say so; principle 3 rules out
	// silently ignoring it.
	//
	// This test has now outlived two subjects. It first asserted that
	// --table-mode printed a notice, which stopped being true when M5
	// implemented it. It then moved to --table-mode=image, which stopped
	// being true when that mode was removed rather than frozen into the v1
	// API as a permanent no-op. --jobs is the remaining case, and unlike the
	// other two it is reserved deliberately rather than pending work.
	in := writeFixture(t)
	out := filepath.Join(t.TempDir(), "out.epub")

	code, _, stderr := runCLI("convert", in, "-o", out, "--jobs", "8")
	if code != exitOK {
		t.Fatalf("exit code = %d\nstderr:\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "--jobs is reserved") {
		t.Errorf("no notice that --jobs does nothing:\n%s", stderr)
	}
}

func TestReservedFlagIsQuietUnlessPassed(t *testing.T) {
	// The notice is about the caller's choice, so it must not fire on the
	// default. Otherwise every conversion carries it.
	in := writeFixture(t)
	out := filepath.Join(t.TempDir(), "out.epub")

	code, _, stderr := runCLI("convert", in, "-o", out)
	if code != exitOK {
		t.Fatalf("exit code = %d\nstderr:\n%s", code, stderr)
	}
	if strings.Contains(stderr, "--jobs is reserved") {
		t.Errorf("the reserved-flag notice fired without the flag:\n%s", stderr)
	}
}

func TestRemovedTableModeIsRejected(t *testing.T) {
	// "image" was a documented mode that never did anything but degrade to
	// text. It was removed rather than carried into v1, so asking for it is
	// now a usage error rather than a silent substitution.
	in := writeTableFixture(t)
	out := filepath.Join(t.TempDir(), "out.epub")

	code, _, stderr := runCLI("convert", in, "-o", out, "--table-mode", "image")
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d for a removed mode\nstderr:\n%s",
			code, exitUsage, stderr)
	}
}

func TestFailedConversionLeavesNoPartialFile(t *testing.T) {
	// A malformed input must not leave a stray .epub behind.
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pdf")
	if err := os.WriteFile(bad, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.epub")

	runCLI("convert", bad, "-o", out)

	if _, err := os.Stat(out); err == nil {
		t.Error("a partial output file was left behind")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".decant-") {
			t.Errorf("temporary file %s was not cleaned up", e.Name())
		}
	}
}

func TestStdoutOutput(t *testing.T) {
	// "-o -" must stream the EPUB to stdout. run writes to os.Stdout for this
	// path, so verify the exit status and that nothing corrupts the summary.
	in := writeFixture(t)
	code, _, stderr := runCLI("convert", in, "-o", "-", "--quiet")
	if code != exitOK {
		t.Fatalf("exit code = %d\nstderr:\n%s", code, stderr)
	}
}
