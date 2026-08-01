package pdf

import (
	"strconv"
)

// objKind discriminates the token types a content stream can carry.
type objKind uint8

const (
	kNull objKind = iota
	kNum
	kString // literal or hex string, already unescaped
	kName   // /Name, without the slash
	kArray
	kDict
	kOp   // bare operator keyword
	kBool //nolint:unused // parsed for completeness; no operator consumes it in v1
)

// object is a content stream token. Content streams carry a small enough type
// universe that a tagged struct beats an interface here: the interpreter runs
// this over millions of tokens per document and an interface would allocate.
type object struct {
	kind objKind
	num  float64
	str  []byte // string bytes, name text, or operator text
	arr  []object
	dict map[string]object
	b    bool
}

func (o object) isOp(name string) bool {
	return o.kind == kOp && string(o.str) == name
}

// float returns the numeric value, or 0 for non-numeric tokens. Content
// streams in the wild pass strings and names where numbers belong; treating
// those as zero keeps the interpreter from having to check every operand.
func (o object) float() float64 {
	if o.kind == kNum {
		return o.num
	}
	return 0
}

func (o object) name() string {
	if o.kind == kName {
		return string(o.str)
	}
	return ""
}

// maxNesting caps array and dictionary recursion. Malformed input can nest
// without bound; the deepest legitimate content stream construct is a few
// levels, so this is generous.
const maxNesting = 64

// lexer tokenizes a decoded content stream. It never panics and never
// allocates without bound, both of which the fuzz target asserts.
type lexer struct {
	data []byte
	pos  int
}

func newLexer(data []byte) *lexer { return &lexer{data: data} }

func isWhitespace(c byte) bool {
	switch c {
	case 0, '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

func isDelimiter(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func isRegular(c byte) bool { return !isWhitespace(c) && !isDelimiter(c) }

// skipSpace advances past whitespace and comments.
func (l *lexer) skipSpace() {
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		switch {
		case isWhitespace(c):
			l.pos++
		case c == '%':
			for l.pos < len(l.data) && l.data[l.pos] != '\n' && l.data[l.pos] != '\r' {
				l.pos++
			}
		default:
			return
		}
	}
}

// next returns the next token. ok is false at end of stream.
func (l *lexer) next() (object, bool) { return l.nextDepth(0) }

func (l *lexer) nextDepth(depth int) (object, bool) {
	l.skipSpace()
	if l.pos >= len(l.data) {
		return object{}, false
	}

	c := l.data[l.pos]
	switch {
	case c == '/':
		l.pos++
		return object{kind: kName, str: l.readName()}, true

	case c == '(':
		l.pos++
		return object{kind: kString, str: l.readLiteralString()}, true

	case c == '<':
		if l.pos+1 < len(l.data) && l.data[l.pos+1] == '<' {
			l.pos += 2
			return l.readDict(depth)
		}
		l.pos++
		return object{kind: kString, str: l.readHexString()}, true

	case c == '[':
		l.pos++
		return l.readArray(depth)

	case c == ']' || c == '>' || c == ')' || c == '}':
		// Unbalanced closer. Consume it so we make progress and keep scanning;
		// damaged streams routinely carry these.
		l.pos++
		return l.nextDepth(depth)

	case c == '{':
		// PostScript calculator function body. Not valid in a content stream,
		// but harmless to skip.
		l.pos++
		return l.nextDepth(depth)

	case c == '+' || c == '-' || c == '.' || (c >= '0' && c <= '9'):
		return l.readNumber(), true

	default:
		kw := l.readKeyword()
		if len(kw) == 0 {
			// Not a regular character and not handled above; skip it rather
			// than spin.
			l.pos++
			return l.nextDepth(depth)
		}
		switch string(kw) {
		case "true":
			return object{kind: kBool, b: true}, true
		case "false":
			return object{kind: kBool, b: false}, true
		case "null":
			return object{kind: kNull}, true
		}
		return object{kind: kOp, str: kw}, true
	}
}

func (l *lexer) readKeyword() []byte {
	start := l.pos
	for l.pos < len(l.data) && isRegular(l.data[l.pos]) {
		l.pos++
	}
	return l.data[start:l.pos]
}

// readName reads a name after the leading slash, decoding #XX escapes.
func (l *lexer) readName() []byte {
	start := l.pos
	hasEscape := false
	for l.pos < len(l.data) && isRegular(l.data[l.pos]) {
		if l.data[l.pos] == '#' {
			hasEscape = true
		}
		l.pos++
	}
	raw := l.data[start:l.pos]
	if !hasEscape {
		return raw
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '#' && i+2 < len(raw) {
			if hi, ok := hexVal(raw[i+1]); ok {
				if lo, ok2 := hexVal(raw[i+2]); ok2 {
					out = append(out, hi<<4|lo)
					i += 2
					continue
				}
			}
		}
		out = append(out, raw[i])
	}
	return out
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func (l *lexer) readNumber() object {
	start := l.pos
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		if (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.' || c == 'e' || c == 'E' {
			l.pos++
			continue
		}
		break
	}
	raw := l.data[start:l.pos]
	v, err := strconv.ParseFloat(string(raw), 64)
	if err != nil {
		// Producers emit forms strconv rejects, such as "--5", "4.", or
		// ".5.7". Salvage the leading numeric prefix rather than discarding
		// the operand, which would shift every remaining operand by one.
		v = salvageNumber(raw)
	}
	return object{kind: kNum, num: v}
}

// salvageNumber parses the longest valid numeric prefix of raw.
func salvageNumber(raw []byte) float64 {
	end := 0
	seenDigit, seenDot := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == '+' || c == '-':
			if i != 0 {
				// A sign mid-token ends the number, except for the repeated
				// leading signs some producers emit ("--5"), which we skip.
				if !seenDigit && !seenDot {
					continue
				}
				return parsePrefix(raw[:end], seenDigit)
			}
		case c == '.':
			if seenDot {
				return parsePrefix(raw[:end], seenDigit)
			}
			seenDot = true
		case c >= '0' && c <= '9':
			seenDigit = true
		default:
			return parsePrefix(raw[:end], seenDigit)
		}
		end = i + 1
	}
	return parsePrefix(raw[:end], seenDigit)
}

func parsePrefix(b []byte, seenDigit bool) float64 {
	if !seenDigit {
		return 0
	}
	s := string(b)
	// Strip repeated leading signs, then let ParseFloat handle a trailing dot.
	neg := false
	for len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		if s[0] == '-' {
			neg = !neg
		}
		s = s[1:]
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		if len(s) > 0 && s[len(s)-1] == '.' {
			v, err = strconv.ParseFloat(s[:len(s)-1], 64)
		}
		if err != nil {
			return 0
		}
	}
	if neg {
		return -v
	}
	return v
}

// readLiteralString reads a (...) string, handling balanced parens and
// backslash escapes. The opening paren is already consumed.
func (l *lexer) readLiteralString() []byte {
	var out []byte
	depth := 1
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		l.pos++
		switch c {
		case '\\':
			if l.pos >= len(l.data) {
				return out
			}
			e := l.data[l.pos]
			l.pos++
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '(', ')', '\\':
				out = append(out, e)
			case '\r':
				// Line continuation; swallow an immediately following \n.
				if l.pos < len(l.data) && l.data[l.pos] == '\n' {
					l.pos++
				}
			case '\n':
				// Line continuation, emits nothing.
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for i := 0; i < 2 && l.pos < len(l.data); i++ {
						d := l.data[l.pos]
						if d < '0' || d > '7' {
							break
						}
						v = v*8 + int(d-'0')
						l.pos++
					}
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
		case '(':
			depth++
			out = append(out, c)
		case ')':
			depth--
			if depth == 0 {
				return out
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

// readHexString reads a <...> string. The opening angle bracket is consumed.
func (l *lexer) readHexString() []byte {
	var out []byte
	var cur byte
	half := false
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		l.pos++
		if c == '>' {
			break
		}
		v, ok := hexVal(c)
		if !ok {
			continue
		}
		if half {
			out = append(out, cur<<4|v)
			half = false
		} else {
			cur = v
			half = true
		}
	}
	if half {
		// An odd digit count pads with a trailing zero per the spec.
		out = append(out, cur<<4)
	}
	return out
}

func (l *lexer) readArray(depth int) (object, bool) {
	if depth >= maxNesting {
		l.skipToClose(']')
		return object{kind: kArray}, true
	}
	arr := []object{}
	for {
		l.skipSpace()
		if l.pos >= len(l.data) {
			break
		}
		if l.data[l.pos] == ']' {
			l.pos++
			break
		}
		o, ok := l.nextDepth(depth + 1)
		if !ok {
			break
		}
		arr = append(arr, o)
	}
	return object{kind: kArray, arr: arr}, true
}

func (l *lexer) readDict(depth int) (object, bool) {
	if depth >= maxNesting {
		l.skipToClose('>')
		return object{kind: kDict, dict: map[string]object{}}, true
	}
	d := map[string]object{}
	for {
		l.skipSpace()
		if l.pos >= len(l.data) {
			break
		}
		if l.data[l.pos] == '>' {
			l.pos++
			if l.pos < len(l.data) && l.data[l.pos] == '>' {
				l.pos++
			}
			break
		}
		if l.data[l.pos] != '/' {
			// Key slot holds a non-name. Consume one token to make progress.
			if _, ok := l.nextDepth(depth + 1); !ok {
				break
			}
			continue
		}
		l.pos++
		key := string(l.readName())
		val, ok := l.nextDepth(depth + 1)
		if !ok {
			break
		}
		d[key] = val
	}
	return object{kind: kDict, dict: d}, true
}

// skipToClose scans forward past a nesting-limited construct.
func (l *lexer) skipToClose(closer byte) {
	for l.pos < len(l.data) {
		if l.data[l.pos] == closer {
			l.pos++
			return
		}
		l.pos++
	}
}

// skipInlineImage advances past the binary payload of a BI/ID/EI sequence.
// The lexer cannot tokenize that payload, so the interpreter calls this on
// seeing BI. Returns false if no terminator is found.
func (l *lexer) skipInlineImage() bool {
	// Find ID, which ends the parameter dictionary.
	for l.pos < len(l.data) {
		o, ok := l.next()
		if !ok {
			return false
		}
		if o.isOp("ID") {
			break
		}
		if o.isOp("EI") {
			return true
		}
	}
	// One whitespace byte separates ID from the data.
	if l.pos < len(l.data) && isWhitespace(l.data[l.pos]) {
		l.pos++
	}
	// Scan for whitespace-delimited EI. Binary data can contain the byte pair,
	// so require the delimiters and a plausible continuation.
	for l.pos+1 < len(l.data) {
		if l.data[l.pos] == 'E' && l.data[l.pos+1] == 'I' {
			before := l.pos == 0 || isWhitespace(l.data[l.pos-1])
			afterPos := l.pos + 2
			after := afterPos >= len(l.data) ||
				isWhitespace(l.data[afterPos]) || isDelimiter(l.data[afterPos])
			if before && after {
				l.pos = afterPos
				return true
			}
		}
		l.pos++
	}
	l.pos = len(l.data)
	return false
}
