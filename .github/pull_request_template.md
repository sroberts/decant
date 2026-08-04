## What changed, and why

<!--
The why matters more than the what here. If you ruled something out, say so —
that reasoning is the part a future reader cannot reconstruct from the diff.
-->

## Checks

- [ ] `gofmt -l .` prints nothing, `go vet ./...` and `staticcheck ./...` pass
- [ ] `go test ./...` passes, including `epubcheck` if it is installed locally
- [ ] Every new threshold lives in `Heuristics` with a documented default
- [ ] No internal types leaked into the public API
- [ ] `spec.md` updated if a stage's behaviour or a §13 decision changed

## Heuristic changes only

- [ ] `make manifest` run and the diff reviewed
- [ ] Drift on files I did not mean to touch is explained below

<!-- Paste the manifest diff, or write "no drift". -->

## Tests

- [ ] A new test fails without this change (say how you confirmed it)
- [ ] Any changed test is called out below with the decision it used to encode

## Known cost

<!--
Anything this makes worse: a slower path, a wider public surface, a heuristic
that now fires on something it did not before. "None" is a fine answer, but
say it deliberately.
-->
