package decant_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sroberts/decant"
)

// The corpus is py-pdf/sample-files, fetched by "make corpus" into
// testdata/corpus/py-pdf and pinned to a commit in the Makefile.
//
// It is deliberately not vendored: the files are CC-BY-SA-4.0, and spec
// section 4.6 rules out carrying share-alike material in an MIT repository.
// Every test here skips when the corpus is absent, so a fresh clone still
// runs green; CI fetches it and therefore enforces these checks.
const corpusEnv = "DECANT_CORPUS"

var defaultCorpusDir = filepath.Join("testdata", "corpus", "py-pdf")

// updateManifest regenerates the golden manifest instead of comparing
// against it. Run with "make manifest".
var updateManifest = flag.Bool("update", false, "regenerate the corpus golden manifest")

const manifestPath = "testdata/corpus_manifest.json"

// corpusDir returns the corpus root, or skips the test when it is missing.
func corpusDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(corpusEnv)
	if dir == "" {
		dir = defaultCorpusDir
	}
	if _, err := os.Stat(filepath.Join(dir, "files.json")); err != nil {
		t.Skipf("corpus not present at %s; run \"make corpus\"", dir)
	}
	return dir
}

// corpusFile is one entry of the corpus's own files.json, which decant treats
// as an independent oracle for page counts and encryption.
type corpusFile struct {
	Path      string `json:"path"`
	Producer  string `json:"producer"`
	Pages     *int   `json:"pages"`
	Encrypted *bool  `json:"encrypted"`
	Images    *int   `json:"images"`
	Forms     *int   `json:"forms"`
}

func loadCorpusIndex(t *testing.T, dir string) []corpusFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "files.json"))
	if err != nil {
		t.Fatalf("reading files.json: %v", err)
	}
	var wrapper struct {
		Data []corpusFile `json:"data"`
	}
	if err := json.Unmarshal(b, &wrapper); err != nil {
		t.Fatalf("parsing files.json: %v", err)
	}
	sort.Slice(wrapper.Data, func(i, j int) bool {
		return wrapper.Data[i].Path < wrapper.Data[j].Path
	})
	return wrapper.Data
}

// outcome classifies what decant did with a file, which is the coarsest and
// most stable thing to assert on.
type outcome string

const (
	outcomeOK          outcome = "ok"
	outcomeEncrypted   outcome = "encrypted"
	outcomeNoTextLayer outcome = "no_text_layer"
	outcomeMalformed   outcome = "malformed"
	outcomeUsage       outcome = "usage"
	outcomeError       outcome = "error"
)

func classify(err error) outcome {
	if err == nil {
		return outcomeOK
	}
	var enc *decant.EncryptedError
	var scan *decant.NoTextLayerError
	var mal *decant.MalformedError
	var use *decant.UsageError
	switch {
	case errors.As(err, &enc):
		return outcomeEncrypted
	case errors.As(err, &scan):
		return outcomeNoTextLayer
	case errors.As(err, &mal):
		return outcomeMalformed
	case errors.As(err, &use):
		return outcomeUsage
	}
	return outcomeError
}

// manifestEntry records what decant produced for one corpus file.
//
// Per spec section 10 the golden is a structure fingerprint and a text
// digest, never byte-identical XHTML, so formatting refactors do not churn
// the corpus. Counts and rates ride along because they are the numbers a
// regression actually moves.
type manifestEntry struct {
	Path    string  `json:"path"`
	Outcome outcome `json:"outcome"`

	Pages  int `json:"pages,omitempty"`
	Blocks int `json:"blocks,omitempty"`
	// Kinds counts blocks by kind, and Headings counts them by level.
	Kinds    map[string]int `json:"kinds,omitempty"`
	Headings map[string]int `json:"headings,omitempty"`

	Chapters         int `json:"chapters,omitempty"`
	MultiColumnPages int `json:"multi_column_pages,omitempty"`

	// DecodeFailureBucket is the decode failure rate rounded to a coarse
	// bucket. An exact rate would churn on any font change; the bucket only
	// moves when text quality meaningfully shifts.
	DecodeFailureBucket string `json:"decode_failure_bucket,omitempty"`

	QualityScore int `json:"quality_score,omitempty"`
	Warnings     int `json:"warnings,omitempty"`

	// FingerprintSHA is a digest of the full structure fingerprint;
	// FingerprintHead is its first elements, kept legible for debugging.
	FingerprintSHA  string `json:"fingerprint_sha,omitempty"`
	FingerprintHead string `json:"fingerprint_head,omitempty"`
	// TextSHA digests the concatenated block text.
	TextSHA string `json:"text_sha,omitempty"`

	// TextRecallBucket is how much of pdftotext's text reached the EPUB,
	// rounded to 5% so it does not churn on tokenization noise.
	//
	// It is recorded rather than asserted. pdftotext is a different tool
	// making different decisions about what counts as content: it extracts
	// text that displays sideways or upside down on a rotated page, and it
	// renders form widget values. decant deliberately drops both. A gate
	// would therefore demand decant reproduce text a reader cannot read.
	// Tracking the number instead means a change that silently drops text
	// shows up in the manifest diff, which is what this file is for.
	TextRecallBucket string `json:"text_recall_bucket,omitempty"`
}

// analyzeCorpusFile runs a conversion and summarizes it into a manifest entry.
func analyzeCorpusFile(t *testing.T, path string) manifestEntry {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	conv, err := decant.New(defaultOpts())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	doc, err := conv.Analyze(context.Background(), bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return manifestEntry{Outcome: classify(err)}
	}

	var out bytes.Buffer
	rep, err := conv.Write(context.Background(), doc, &out)
	if err != nil {
		return manifestEntry{Outcome: classify(err)}
	}

	e := manifestEntry{
		Outcome:          outcomeOK,
		Pages:            rep.PagesConverted,
		Blocks:           len(doc.Blocks),
		Kinds:            map[string]int{},
		Headings:         map[string]int{},
		Chapters:         rep.Chapters,
		MultiColumnPages: rep.MultiColumnPages,
		QualityScore:     rep.QualityScore,
		Warnings:         rep.Warnings(),
	}
	for k, n := range rep.Blocks {
		e.Kinds[string(k)] = n
	}
	for lv, n := range rep.Headings {
		e.Headings[itoa(lv)] = n
	}
	e.DecodeFailureBucket = bucketRate(rep.DecodeFailureRate())

	fp := fingerprint(doc)
	sum := sha256.Sum256([]byte(fp))
	e.FingerprintSHA = hex.EncodeToString(sum[:])[:16]
	e.FingerprintHead = headOf(fp, 24)

	var textBuf strings.Builder
	for _, b := range doc.Blocks {
		textBuf.WriteString(b.Text)
		textBuf.WriteByte('\n')
	}
	tsum := sha256.Sum256([]byte(textBuf.String()))
	e.TextSHA = hex.EncodeToString(tsum[:])[:16]
	e.TextRecallBucket = textRecallBucket(t, path, out.Bytes())

	return e
}

// textRecallBucket measures how much of pdftotext's text reached the EPUB.
//
// Empty when pdftotext is unavailable, so the manifest stays comparable on a
// machine without poppler; CI installs it. See TextRecallBucket for why this
// is a recorded number rather than an assertion.
func textRecallBucket(t *testing.T, path string, epubBytes []byte) string {
	t.Helper()
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		return ""
	}
	ref, err := exec.Command(bin, "-q", path, "-").Output()
	if err != nil {
		return ""
	}
	refWords := words(string(ref))
	if len(refWords) < 10 {
		return ""
	}

	have := map[string]int{}
	for _, w := range words(epubText(t, epubBytes)) {
		have[w]++
	}
	hit := 0
	for _, w := range refWords {
		if have[w] > 0 {
			have[w]--
			hit++
		}
	}
	// Multiset recall, not set recall: counting distinct words would score a
	// document that kept one copy of a repeated phrase as perfect.
	return bucketPercent(float64(hit) / float64(len(refWords)))
}

// bucketPercent rounds to the nearest 5% so the manifest moves only on a
// meaningful change, not on tokenization noise.
func bucketPercent(r float64) string {
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	return itoa(int(r*20+0.5)*5) + "%"
}

// bucketRate coarsens a decode failure rate so the manifest only moves on a
// meaningful change in text quality.
func bucketRate(r float64) string {
	switch {
	case r == 0:
		return "none"
	case r < 0.001:
		return "<0.1%"
	case r < 0.01:
		return "<1%"
	case r < 0.05:
		return "<5%"
	case r < 0.15:
		return "<15%"
	default:
		return ">=15%"
	}
}

func headOf(fp string, n int) string {
	parts := strings.Fields(fp)
	if len(parts) <= n {
		return fp
	}
	return strings.Join(parts[:n], " ") + " ..."
}

// corpusPDFs returns every PDF in the corpus, relative to its root.
func corpusPDFs(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.EqualFold(filepath.Ext(p), ".pdf") {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walking corpus: %v", err)
	}
	sort.Strings(out)
	return out
}

// TestCorpusMatchesIndex checks decant against the corpus's own metadata,
// which is an oracle produced by a completely independent implementation.
func TestCorpusMatchesIndex(t *testing.T) {
	dir := corpusDir(t)

	for _, cf := range loadCorpusIndex(t, dir) {
		t.Run(cf.Path, func(t *testing.T) {
			path := filepath.Join(dir, filepath.FromSlash(cf.Path))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("not present: %v", err)
			}

			info, err := decant.Meta(context.Background(),
				bytes.NewReader(data), int64(len(data)))

			if cf.Encrypted != nil && *cf.Encrypted {
				var enc *decant.EncryptedError
				if !errors.As(err, &enc) {
					t.Errorf("encrypted file: got %v, want an EncryptedError", err)
				}
				return
			}

			if err != nil {
				// A file the index says is readable but decant rejects is a
				// real gap. Known ones are listed in the manifest, so this
				// only reports rather than fails.
				t.Skipf("decant cannot read this file (tracked in the manifest): %v", err)
			}
			if cf.Pages != nil && info.PageCount != *cf.Pages {
				t.Errorf("page count = %d, want %d per files.json",
					info.PageCount, *cf.Pages)
			}
		})
	}
}

// TestCorpusManifest is the regression gate: it converts every corpus file
// and compares a structural summary against a checked-in golden.
//
// Run "make manifest" to regenerate after an intentional change, then review
// the diff. A drifting fingerprint on a file you did not mean to touch is the
// signal this exists to produce.
func TestCorpusManifest(t *testing.T) {
	dir := corpusDir(t)

	entries := make([]manifestEntry, 0, 40)
	for _, rel := range corpusPDFs(t, dir) {
		e := analyzeCorpusFile(t, filepath.Join(dir, filepath.FromSlash(rel)))
		e.Path = rel
		entries = append(entries, e)
	}

	got, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("encoding manifest: %v", err)
	}
	got = append(got, '\n')

	if *updateManifest {
		if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d entries)", manifestPath, len(entries))
		return
	}

	want, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Skipf("no golden manifest at %s; run \"make manifest\"", manifestPath)
	}
	if bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
		return
	}

	// Report a per-file diff rather than dumping both documents.
	var wantEntries []manifestEntry
	if err := json.Unmarshal(want, &wantEntries); err != nil {
		t.Fatalf("parsing golden manifest: %v", err)
	}
	byPath := make(map[string]manifestEntry, len(wantEntries))
	for _, e := range wantEntries {
		byPath[e.Path] = e
	}

	for _, g := range entries {
		w, ok := byPath[g.Path]
		if !ok {
			t.Errorf("%s: new file in the corpus, not in the manifest", g.Path)
			continue
		}
		delete(byPath, g.Path)
		if diff := describeDiff(w, g); diff != "" {
			t.Errorf("%s:\n%s", g.Path, diff)
		}
	}
	for path := range byPath {
		t.Errorf("%s: in the manifest but missing from the corpus", path)
	}
	t.Log("run \"make manifest\" to accept these changes, then review the diff")
}

// describeDiff renders the fields that changed between two manifest entries.
func describeDiff(want, got manifestEntry) string {
	var b strings.Builder
	cmp := func(name, w, g string) {
		if w != g {
			b.WriteString("    " + name + ": " + w + " -> " + g + "\n")
		}
	}
	cmp("outcome", string(want.Outcome), string(got.Outcome))
	cmp("pages", itoa(want.Pages), itoa(got.Pages))
	cmp("blocks", itoa(want.Blocks), itoa(got.Blocks))
	cmp("chapters", itoa(want.Chapters), itoa(got.Chapters))
	cmp("multi_column_pages", itoa(want.MultiColumnPages), itoa(got.MultiColumnPages))
	cmp("decode_failure_bucket", want.DecodeFailureBucket, got.DecodeFailureBucket)
	cmp("quality_score", itoa(want.QualityScore), itoa(got.QualityScore))
	cmp("warnings", itoa(want.Warnings), itoa(got.Warnings))
	cmp("kinds", mapString(want.Kinds), mapString(got.Kinds))
	cmp("headings", mapString(want.Headings), mapString(got.Headings))
	cmp("fingerprint", want.FingerprintSHA, got.FingerprintSHA)
	cmp("text", want.TextSHA, got.TextSHA)
	if want.FingerprintSHA != got.FingerprintSHA {
		b.WriteString("      was: " + want.FingerprintHead + "\n")
		b.WriteString("      now: " + got.FingerprintHead + "\n")
	}
	return b.String()
}

func mapString(m map[string]int) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+itoa(m[k]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// TestCorpusDeterminism converts every readable corpus file twice and
// requires byte-identical output, which is the guarantee in spec section 9.
func TestCorpusDeterminism(t *testing.T) {
	dir := corpusDir(t)

	for _, rel := range corpusPDFs(t, dir) {
		t.Run(rel, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
			if err != nil {
				t.Skip(err)
			}

			// Each run builds its own Converter, so the comparison covers
			// process state as well as input. It used to vary --jobs, but
			// that option never did anything and no longer exists, so the
			// comparison held for the wrong reason.
			run := func() ([]byte, error) {
				conv, err := decant.New(defaultOpts())
				if err != nil {
					return nil, err
				}
				var buf bytes.Buffer
				_, err = conv.Convert(context.Background(),
					bytes.NewReader(data), int64(len(data)), &buf)
				return buf.Bytes(), err
			}

			a, errA := run()
			if errA != nil {
				t.Skipf("not convertible: %v", classify(errA))
			}
			b, errB := run()
			if errB != nil {
				t.Fatalf("second run failed after the first succeeded: %v", errB)
			}
			if !bytes.Equal(a, b) {
				t.Errorf("two conversions of one file differed (%d vs %d bytes)",
					len(a), len(b))
			}
		})
	}
}

// TestCorpusEPUBCheck validates every converted corpus file with epubcheck,
// which spec section 12 makes a merge gate.
func TestCorpusEPUBCheck(t *testing.T) {
	dir := corpusDir(t)

	bin, err := exec.LookPath("epubcheck")
	if err != nil {
		t.Skip("epubcheck not installed; CI enforces this gate")
	}
	if testing.Short() {
		t.Skip("skipping epubcheck in short mode")
	}

	tmp := t.TempDir()
	for _, rel := range corpusPDFs(t, dir) {
		t.Run(rel, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
			if err != nil {
				t.Skip(err)
			}
			conv, err := decant.New(defaultOpts())
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if _, err := conv.Convert(context.Background(),
				bytes.NewReader(data), int64(len(data)), &buf); err != nil {
				t.Skipf("not convertible: %v", classify(err))
			}

			out := filepath.Join(tmp, strings.ReplaceAll(rel, "/", "_")+".epub")
			if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := exec.Command(bin, out).CombinedOutput()
			if err != nil || bytes.Contains(res, []byte("ERROR")) {
				t.Errorf("epubcheck reported problems:\n%s", res)
			}
		})
	}
}

// TestCorpusReadingOrder checks extracted word order against pdftotext, the
// reference in spec section 10's property tests.
//
// Only single-column pages are compared: pdftotext's own column handling
// differs from decant's, so a mismatch there would say nothing useful. The
// assertion is that decant's words appear as a subsequence of pdftotext's,
// which catches scrambled reading order without demanding identical
// tokenization, hyphenation, or ligature handling.
func TestCorpusReadingOrder(t *testing.T) {
	dir := corpusDir(t)

	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Skip("pdftotext not installed; install poppler to enable this check")
	}

	// Documents carrying form fields are excluded. Spec section 1 puts PDF
	// forms out of scope, so decant extracts only page content while
	// pdftotext also renders widget appearances and orders them by its own
	// layout rules. On 012-libreoffice-form that makes pdftotext emit
	// "First Name Alice ... Bob Last Name" where decant emits the three
	// labels in page order, and the reference is the less faithful of the
	// two. Comparing them would assert the wrong thing.
	hasForms := map[string]bool{}
	for _, cf := range loadCorpusIndex(t, dir) {
		if cf.Forms != nil && *cf.Forms > 0 {
			hasForms[cf.Path] = true
		}
	}

	for _, rel := range corpusPDFs(t, dir) {
		t.Run(rel, func(t *testing.T) {
			if hasForms[rel] {
				t.Skip("document carries form fields; forms are out of scope per spec 1")
			}
			path := filepath.Join(dir, filepath.FromSlash(rel))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skip(err)
			}

			conv, err := decant.New(defaultOpts())
			if err != nil {
				t.Fatal(err)
			}
			doc, err := conv.Analyze(context.Background(),
				bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Skipf("not convertible: %v", classify(err))
			}
			if doc.Report().MultiColumnPages > 0 {
				t.Skip("multi-column document; pdftotext orders columns differently")
			}

			ref, err := exec.Command(bin, "-q", path, "-").Output()
			if err != nil {
				t.Skipf("pdftotext failed: %v", err)
			}

			refWords := significantWords(string(ref))
			var sb strings.Builder
			for _, b := range doc.Blocks {
				sb.WriteString(b.Text)
				sb.WriteByte(' ')
			}
			gotWords := significantWords(sb.String())

			if len(gotWords) < 10 || len(refWords) < 10 {
				t.Skip("too little text to compare")
			}
			// 0.7 rather than 1.0: the two tools tokenize, dehyphenate, and
			// resolve ligatures differently, so a perfect match is not the
			// goal. Scrambled reading order collapses the greedy scan well
			// below this, while cosmetic differences stay above it.
			const minSubsequence = 0.7
			if n, total := subsequenceRatio(gotWords, refWords); n < minSubsequence {
				t.Errorf("only %.0f%% of %d extracted words appear in pdftotext order; "+
					"reading order may be scrambled", n*100, total)
			}
		})
	}
}

// significantWords lowercases and strips tokens that differ for uninteresting
// reasons: punctuation, hyphenation artifacts, and very short words.
func significantWords(s string) []string {
	var out []string
	for _, f := range strings.Fields(strings.ToLower(s)) {
		f = strings.Trim(f, ".,;:!?()[]{}\"'`\u201c\u201d\u2018\u2019")
		if len(f) < 4 {
			continue
		}
		if strings.ContainsAny(f, "\ufffd-") {
			// U+FFFD is a decode failure and a hyphen may be a line-break
			// artifact; neither says anything about ordering.
			continue
		}
		out = append(out, f)
	}
	return out
}

// subsequenceRatio returns the fraction of got that appears in ref in order,
// using a greedy scan. It is a similarity measure, not an exact match.
func subsequenceRatio(got, ref []string) (float64, int) {
	if len(got) == 0 {
		return 1, 0
	}
	// Index ref positions per word so the scan stays linear-ish.
	pos := make(map[string][]int, len(ref))
	for i, w := range ref {
		pos[w] = append(pos[w], i)
	}

	matched, cursor := 0, -1
	for _, w := range got {
		idxs := pos[w]
		// First occurrence at or after the cursor.
		lo := sort.SearchInts(idxs, cursor+1)
		if lo < len(idxs) {
			cursor = idxs[lo]
			matched++
		}
	}
	return float64(matched) / float64(len(got)), len(got)
}

// TestCorpusSerializationLosesNoText is the oracle-free half of spec section
// 10's content check, run across every convertible real document.
//
// Stages 7 and 8 format text, they do not select it, so every word the block
// tree holds must reach the EPUB. Unlike the reading-order property this
// needs no external tool and no threshold, and unlike the manifest's recall
// bucket it is an assertion: there is no legitimate reason for the serializer
// to drop a word.
func TestCorpusSerializationLosesNoText(t *testing.T) {
	dir := corpusDir(t)

	for _, rel := range corpusPDFs(t, dir) {
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(dir, filepath.FromSlash(rel))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skip(err)
			}

			conv, err := decant.New(defaultOpts())
			if err != nil {
				t.Fatal(err)
			}
			doc, err := conv.Analyze(context.Background(),
				bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Skipf("not convertible: %v", classify(err))
			}

			var out bytes.Buffer
			if _, err := conv.Write(context.Background(), doc, &out); err != nil {
				t.Skipf("not writable: %v", classify(err))
			}

			var blockText strings.Builder
			for _, b := range doc.Blocks {
				blockText.WriteString(b.Text)
				blockText.WriteByte(' ')
			}
			want := words(blockText.String())
			if len(want) < 10 {
				t.Skip("too little text to compare")
			}

			have := map[string]int{}
			for _, w := range words(epubText(t, out.Bytes())) {
				have[w]++
			}

			var missing []string
			for _, w := range want {
				if have[w] > 0 {
					have[w]--
					continue
				}
				missing = append(missing, w)
			}
			if len(missing) > 0 {
				n := len(missing)
				if n > 12 {
					n = 12
				}
				t.Errorf("%d of %d words did not survive serialization: %s",
					len(missing), len(want), strings.Join(missing[:n], " "))
			}
		})
	}
}

// TestCorpusScopeWarnings is the end-to-end half of spec section 1's
// detect-and-warn requirement.
//
// The unit tests in script_internal_test.go cover the predicate and the
// threshold; this covers the path from a real bidirectional PDF through glyph
// extraction and line assembly to the report, which a synthetic fixture
// cannot reach because testpdf writes literal bytes against a base-14 font
// and a Hebrew string decodes back as Latin.
func TestCorpusScopeWarnings(t *testing.T) {
	dir := corpusDir(t)

	for _, rel := range corpusPDFs(t, dir) {
		if !strings.Contains(rel, "arabic") {
			continue
		}
		t.Run(rel, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
			if err != nil {
				t.Skip(err)
			}
			conv, err := decant.New(defaultOpts())
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			rep, err := conv.Convert(context.Background(),
				bytes.NewReader(data), int64(len(data)), &out)
			if err != nil {
				t.Skipf("not convertible: %v", classify(err))
			}

			if rep.RTLLetterRatio < 0.2 {
				t.Fatalf("RTLLetterRatio = %.2f on an Arabic document",
					rep.RTLLetterRatio)
			}
			found := false
			for _, d := range rep.Diagnostics {
				if d.Severity == decant.SeverityWarning &&
					strings.Contains(d.Message, "right-to-left") {
					found = true
				}
			}
			if !found {
				t.Error("an Arabic document converted with no scope warning")
			}
			// Detect and warn, not refuse.
			if out.Len() == 0 {
				t.Error("the warning suppressed the output")
			}
		})
	}
}
