# Commit convention

This repository does **not** use Conventional Commits. There is no `feat:` /
`fix:` prefix vocabulary. What it uses instead, consistently, is:

```
Imperative subject under ~72 characters

Prose body, wrapped at 76, explaining why the change is correct and what
was ruled out. Not a restatement of the diff — the diff is already in the
commit.

Co-Authored-By: ...
```

## The subject

Imperative mood, no trailing period, no prefix. It should read as an
instruction to the codebase:

```
Emit inline bold and italic; close out M6
Read XMP dc:language; remove the image-per-page escape hatch
Cut pdfcpu off from the user's config directory
```

Not `feat: add emphasis` or `Added emphasis support`.

## The body

This is the part that matters. The history here is the record of *why* the
code looks the way it does, and several decisions are only defensible with
their reasoning attached. A good body covers:

- **What was measured**, when the change rests on a measurement. Numbers, not
  adjectives: "6,947 spurious elements before, 24 after", not "much better".
- **What was ruled out and why.** The alternative you rejected is invisible in
  the diff and expensive to rediscover.
- **What the change makes worse**, if anything.
- **Which tests changed and which decision they used to encode.** Changing a
  test to match new behaviour is fine; doing it silently is not.

## Trailers

Machine-generated commits carry `Co-Authored-By`. Nothing else is required.

## Why not Conventional Commits

Nothing here consumes commit prefixes — releases take their notes from the
annotated tag, not from commit parsing, and the changelog is written for
people rather than generated. A prefix vocabulary would add ceremony without
a consumer. If a release-note generator is ever wired up, revisit this.
