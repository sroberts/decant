# Third-party material

decant is MIT licensed. Everything vendored or depended on here is
license-compatible with redistribution under those terms.

Releases ship statically linked binaries, so the transitive dependencies
below are redistributed too, not just the direct ones.

## Go dependencies

Direct:

| Module | License | Role |
|---|---|---|
| `github.com/pdfcpu/pdfcpu` | Apache-2.0 | xref parsing, object model, image extraction |
| `golang.org/x/image` | BSD-3-Clause | `font/sfnt` metrics and cmap, Catmull-Rom resampling |
| `golang.org/x/text` | BSD-3-Clause | Unicode normalization |

Indirect, pulled in by pdfcpu and redistributed in the release binaries. All
permissive; verified against the LICENSE file in each module:

| Module | License |
|---|---|
| `github.com/clipperhouse/uax29/v2` | MIT |
| `github.com/hhrutter/lzw` | BSD-3-Clause |
| `github.com/hhrutter/pkcs7` | MIT |
| `github.com/mattn/go-runewidth` | MIT |
| `github.com/pkg/errors` | BSD-2-Clause |
| `golang.org/x/crypto` | BSD-3-Clause |
| `gopkg.in/yaml.v2` | Apache-2.0 |
| `github.com/hhrutter/tiff` | BSD-3-Clause | CMYK TIFF decode, which `x/image/tiff` rejects |

## Hyphenation patterns

`internal/hyphen/patterns/` vendors TeX hyphenation pattern files from the
[hyph-utf8](https://github.com/hyphenation/tex-hyphen) distribution, embedded
into the binary with `go:embed`. Files are vendored **verbatim**, including
their copyright headers, which several of these licenses require.

Spec section 4.6 sets the rule: vendor only files under MIT, BSD, or
unrestricted terms, and **drop the language rather than complicate decant's
license**. Every file below was audited against that rule.

### Shipped

| Language | File | Copyright | License |
|---|---|---|---|
| English (US) | `hyph-en-us.tex` | 1990, 2004, 2005 Gerard D.C. Kuiken | All-permissive: copying and distribution permitted in any medium without royalty, copyright notice preserved |
| German (1996 orthography) | `hyph-de-1996.tex` | 2013–2024 the hyph-de authors | MIT |
| Spanish | `hyph-es.tex` | 1993, 1997, 2001–2019 Javier Bezos, CervanTeX | MIT/X11 |
| French | `hyph-fr.tex` | 1994–2002 Daniel Flipo, Bernard Gaulle; 2016 Arthur Reutenauer | MIT |
| Italian | `hyph-it.tex` | 2008–2011 Claudio Beccari | Dual LPPL or MIT; taken under **MIT** |
| Dutch | `hyph-nl.tex` | 1996 Piet Tutelaers | MIT |
| Polish | `hyph-pl.tex` | 1987–1995 Hanna Kołodziejska, Bogusław Jackowski, Marek Ryćko | Dual, including MIT; taken under **MIT** |
| Portuguese | `hyph-pt.tex` | 1987, 1994, 1996, 2015 Pedro J. de Rezende; 1996, 2015 J. João Dias Almeida; 2024 Leonardo Araujo, Aline Benevides | BSD-3-Clause |

### Deliberately not shipped

| Language | File | License | Why |
|---|---|---|---|
| Russian | `hyph-ru.tex` | LPPL 1.2 or later, no alternative | LPPL requires a modified file to be renamed and carries distribution conditions beyond attribution. Spec section 4.6 says drop the language rather than complicate the license. |
| Swedish | `hyph-sv.tex` | LPPL 1.2 or later, no alternative | Same. |

Spec section 4.6 lists Russian and Swedish among the v1 languages, but the
same section rules out share-alike and renaming conditions and says to drop
the language instead. The license rule is the narrower and later constraint,
so it governs. Documents in those languages convert normally; dehyphenation
is disabled for them and the conversion report records that.

Adding either language would require relicensing decant or shipping the
patterns under separate terms. If a permissively licensed Russian or Swedish
pattern set appears, it can be dropped into `internal/hyphen/patterns/` and
registered in `internal/hyphen/hyphen.go`.
