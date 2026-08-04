# Contributing

decant converts fixed-layout PDF into reflowable EPUB 3. Layout reconstruction
is heuristic, so most changes here are judgement calls about *when a rule
should fire* rather than about whether code compiles. That shapes how changes
are reviewed.

`spec.md` is the authoritative design document. Read the relevant section
before changing a stage, and update §13 when a design decision changes.

## Before you open a PR

```
go build ./... && go vet ./...
gofmt -l .                     # must print nothing
staticcheck ./...
go test ./...
```

CI enforces all four, plus `go test -race`, `epubcheck` against every corpus
output, and short fuzz runs.

**`epubcheck` and `poppler` must be on `PATH` or the validation tests skip
silently** — a green suite without them is not the same as a green suite. The
`.devcontainer/` in this repo installs everything, including a JVM for
epubcheck; open the repo in a container and it is done for you.

## What makes a change likely to be merged

**Measure before you tune.** The corpus is the tool for this. Run
`make corpus` once, then `make manifest` after your change and **read the
diff**. Drift on a file you did not mean to touch is exactly what the manifest
exists to surface. A heuristic change with no manifest diff attached will be
sent back.

**Say why in the commit body.** The commit history is the record of why the
code looks the way it does; several decisions here are only defensible with
their reasoning attached. A body explaining what you ruled out is worth more
than one restating the diff.

**Prove a test bites.** A test that passes before your fix is not a
regression test. Break the code deliberately, watch it fail, then fix it. This
has caught genuinely vacuous tests in this repo more than once — including two
that searched a ZIP archive for plain text and so could never have failed.

**Keep the guard rails.** Some rules in `CLAUDE.md` are load-bearing and
violating one is a design regression, not a style nit: pure Go with no cgo,
byte-identical deterministic output, no OCR ever, every threshold tunable
through `Heuristics`, and no internal types in the public API.

## Common reasons a change gets rejected

- **Tuning a threshold against one document.** §11 warns that fitting to a
  handful of files produces overfitted garbage. Show the corpus manifest.
- **A new magic number.** Thresholds live in `Heuristics` with a documented
  default and a stated reason, not inline.
- **Silently dropping content.** Principle 3 is fail loud, degrade gracefully.
  If a heuristic discards something, the report says so.
- **Public API surface for unimplemented behaviour.** Go compatibility makes
  an exported field permanent at v1; adding one back later is not a breaking
  change, removing it is. `Options.Jobs` and `TableMode`'s `image` value were
  both removed on this argument.
- **Changing tests to match new behaviour** without saying so. If a test
  encoded a decision you are reversing, say which decision and why in the PR.

## Adding a hyphenation language

Audit its licence first. decant ships MIT, so a `hyph-utf8` pattern file must
be MIT, BSD, or unrestricted. Russian and Swedish are deliberately absent
because theirs are LPPL-only. Record the audit in `THIRD_PARTY.md`;
`TestLPPLLanguagesAreNotShipped` guards this.

## Fuzzing

Malformed PDFs are a hostile input class. When you fix a parser bug, add a
seed to the relevant target in `internal/pdf`. Fuzzing is not optional here —
the parser must not panic or allocate without bound.

## Releasing

Maintainers only: `git tag -a vX.Y.Z && git push origin vX.Y.Z`. The tag's
annotation becomes the release notes. See `CLAUDE.md`.
