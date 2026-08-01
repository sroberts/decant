package epub

// BaseCSS is the standard stylesheet. Spec section 4.9 caps it at 50 lines
// and rules out fixed widths and embedded fonts: reader defaults should win
// wherever possible, and CrossPoint's release notes attribute out-of-memory
// crashes to complex stylesheets.
const BaseCSS = `html {
  font-size: 100%;
}
body {
  margin: 0 5%;
  line-height: 1.4;
  text-align: justify;
}
h1, h2, h3, h4, h5, h6 {
  line-height: 1.2;
  text-align: left;
  page-break-after: avoid;
  break-after: avoid;
}
h1 { font-size: 1.6em; margin: 1.2em 0 0.6em; }
h2 { font-size: 1.4em; margin: 1.1em 0 0.5em; }
h3 { font-size: 1.2em; margin: 1em 0 0.4em; }
h4, h5, h6 { font-size: 1.05em; margin: 1em 0 0.4em; }
p {
  margin: 0;
  text-indent: 1.2em;
}
p.first, h1 + p, h2 + p, h3 + p, h4 + p, h5 + p, h6 + p {
  text-indent: 0;
}
blockquote {
  margin: 1em 1.5em;
  text-indent: 0;
}
pre {
  white-space: pre-wrap;
  word-wrap: break-word;
  text-align: left;
  text-indent: 0;
}
figure {
  margin: 1em 0;
  text-align: center;
  page-break-inside: avoid;
  break-inside: avoid;
}
figcaption {
  font-size: 0.9em;
  text-align: center;
  text-indent: 0;
}
img {
  max-width: 100%;
  height: auto;
}
`

// MinimalCSS strips the decorative rules for the constrained device profiles
// in spec section 5.
const MinimalCSS = `body {
  margin: 0 4%;
  line-height: 1.35;
}
h1, h2, h3, h4, h5, h6 {
  line-height: 1.2;
  text-align: left;
}
p {
  margin: 0;
  text-indent: 1.2em;
}
p.first, h1 + p, h2 + p, h3 + p, h4 + p, h5 + p, h6 + p {
  text-indent: 0;
}
pre {
  white-space: pre-wrap;
  text-indent: 0;
}
img {
  max-width: 100%;
  height: auto;
}
`
