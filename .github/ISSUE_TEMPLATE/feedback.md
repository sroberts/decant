---
name: Conversion feedback
about: A PDF converted badly, or the output was wrong on your reader
title: ''
labels: ''
assignees: ''
---

## What went wrong

<!-- What the EPUB does that the PDF did not: scrambled reading order, a lost
figure, headings at the wrong level, text that never arrived. -->

## The conversion report

decant reports every heuristic that fired. Please run with `--report` and
paste the diagnostics and quality score:

```
decant convert yourfile.pdf -o out.epub --report report.json
```

<!-- Paste the summary decant printed, and any warnings from report.json. -->

## Where it went wrong in the pipeline

If you can, narrow it down — this saves more time than anything else:

```
decant probe yourfile.pdf --stage=lines  --page=N
decant probe yourfile.pdf --stage=blocks --page=N
```

<!-- Stage output for the affected page, if you have it. -->

## The file

A PDF we can run is worth far more than a description. If it cannot be
shared, a page or two extracted from it usually reproduces the problem.

- decant version (`decant version`):
- Reader / device, if the problem only appears there:
