package pdf

import (
	"strings"
	"testing"
)

func tokens(t *testing.T, src string) []object {
	t.Helper()
	l := newLexer([]byte(src))
	var out []object
	for {
		o, ok := l.next()
		if !ok {
			return out
		}
		out = append(out, o)
		if len(out) > 10000 {
			t.Fatal("lexer produced an implausible number of tokens")
		}
	}
}

func TestLexerNumbers(t *testing.T) {
	cases := []struct {
		src  string
		want float64
	}{
		{"0", 0},
		{"42", 42},
		{"-17", -17},
		{"+3", 3},
		{"3.14", 3.14},
		{".5", 0.5},
		{"-.5", -0.5},
		// Forms strconv rejects but real producers emit.
		{"4.", 4},
		{"--5", 5},
		{"6.-2", 6},
	}
	for _, c := range cases {
		got := tokens(t, c.src)
		if len(got) != 1 {
			t.Errorf("%q produced %d tokens, want 1", c.src, len(got))
			continue
		}
		if got[0].kind != kNum {
			t.Errorf("%q produced kind %d, want kNum", c.src, got[0].kind)
			continue
		}
		if got[0].num != c.want {
			t.Errorf("%q = %v, want %v", c.src, got[0].num, c.want)
		}
	}
}

func TestLexerLiteralStrings(t *testing.T) {
	cases := []struct{ src, want string }{
		{`(hello)`, "hello"},
		{`(a\(b\)c)`, "a(b)c"},
		{`(nested (parens) here)`, "nested (parens) here"},
		{`(tab\there)`, "tab\there"},
		{`(nl\nhere)`, "nl\nhere"},
		{`(octal\101)`, "octalA"},
		{`(octal\1013)`, "octalA3"},
		// A backslash before a newline is a line continuation.
		{"(line\\\ncont)", "linecont"},
		{`(unterminated`, "unterminated"},
	}
	for _, c := range cases {
		got := tokens(t, c.src)
		if len(got) == 0 {
			t.Errorf("%q produced no tokens", c.src)
			continue
		}
		if got[0].kind != kString {
			t.Errorf("%q produced kind %d, want kString", c.src, got[0].kind)
			continue
		}
		if string(got[0].str) != c.want {
			t.Errorf("%q = %q, want %q", c.src, got[0].str, c.want)
		}
	}
}

func TestLexerHexStrings(t *testing.T) {
	cases := []struct {
		src  string
		want []byte
	}{
		{`<48656C6C6F>`, []byte("Hello")},
		{`<48 65 6C>`, []byte("Hel")},
		{`<4>`, []byte{0x40}}, // odd digit count pads with zero
		{`<>`, nil},
		{`<zz48>`, []byte{0x48}}, // non-hex bytes are skipped
	}
	for _, c := range cases {
		got := tokens(t, c.src)
		if len(got) == 0 {
			t.Errorf("%q produced no tokens", c.src)
			continue
		}
		if string(got[0].str) != string(c.want) {
			t.Errorf("%q = %v, want %v", c.src, got[0].str, c.want)
		}
	}
}

func TestLexerNames(t *testing.T) {
	cases := []struct{ src, want string }{
		{"/Name", "Name"},
		{"/A#20B", "A B"},
		{"/", ""},
		{"/Type0", "Type0"},
	}
	for _, c := range cases {
		got := tokens(t, c.src)
		if len(got) == 0 {
			t.Fatalf("%q produced no tokens", c.src)
		}
		if got[0].kind != kName || got[0].name() != c.want {
			t.Errorf("%q = %q (kind %d), want name %q", c.src, got[0].str, got[0].kind, c.want)
		}
	}
}

func TestLexerArraysAndDicts(t *testing.T) {
	got := tokens(t, `[1 2 (three) /Four]`)
	if len(got) != 1 || got[0].kind != kArray {
		t.Fatalf("expected one array token, got %d tokens", len(got))
	}
	if len(got[0].arr) != 4 {
		t.Errorf("array has %d elements, want 4", len(got[0].arr))
	}

	got = tokens(t, `<< /Type /Font /Size 12 >>`)
	if len(got) != 1 || got[0].kind != kDict {
		t.Fatalf("expected one dict token, got %d tokens", len(got))
	}
	if got[0].dict["Type"].name() != "Font" {
		t.Errorf("dict /Type = %q, want Font", got[0].dict["Type"].name())
	}
	if got[0].dict["Size"].float() != 12 {
		t.Errorf("dict /Size = %v, want 12", got[0].dict["Size"].float())
	}
}

func TestLexerComments(t *testing.T) {
	got := tokens(t, "1 % this is a comment\n2")
	if len(got) != 2 {
		t.Fatalf("got %d tokens, want 2 (comment should be skipped)", len(got))
	}
	if got[0].float() != 1 || got[1].float() != 2 {
		t.Errorf("got %v %v, want 1 2", got[0].float(), got[1].float())
	}
}

func TestLexerNestingIsBounded(t *testing.T) {
	// Deeply nested arrays must not blow the stack.
	src := strings.Repeat("[", 5000) + "1" + strings.Repeat("]", 5000)
	got := tokens(t, src)
	if len(got) == 0 {
		t.Error("deeply nested input produced no tokens")
	}
}

func TestLexerUnbalancedClosersMakeProgress(t *testing.T) {
	got := tokens(t, "] ] 5 > > )")
	found := false
	for _, o := range got {
		if o.kind == kNum && o.num == 5 {
			found = true
		}
	}
	if !found {
		t.Error("lexer lost a token surrounded by unbalanced closers")
	}
}

func TestSkipInlineImage(t *testing.T) {
	// Binary payload containing the bytes "EI" without delimiters must not
	// terminate the scan early.
	src := "BI /W 2 /H 2 ID \x00EI\x01\xff\xfe binary\x00 EI\nQ"
	l := newLexer([]byte(src))

	o, ok := l.next()
	if !ok || !o.isOp("BI") {
		t.Fatal("expected a BI operator")
	}
	if !l.skipInlineImage() {
		t.Fatal("skipInlineImage did not find the terminator")
	}
	o, ok = l.next()
	if !ok || !o.isOp("Q") {
		t.Errorf("after the inline image, got %q, want Q", o.str)
	}
}

// FuzzLexer asserts the lexer terminates and never panics. Spec section 10
// calls malformed PDFs a hostile input class.
func FuzzLexer(f *testing.F) {
	seeds := []string{
		"BT /F1 12 Tf (hi) Tj ET",
		"[ (a) -250 (b) ] TJ",
		"<< /Type /Page >> 1 0 R",
		"q 1 0 0 1 10 20 cm Q",
		"BI /W 1 ID \x00\x01 EI",
		"(unterminated",
		"<<<<<<<<>>>>>>>>",
		"1 2 3 4 5 6 cm",
		"%comment only",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		l := newLexer(data)
		// A token can consume at most one byte at minimum, so the count is
		// bounded by the input length. Exceeding it means the lexer failed to
		// make progress.
		limit := len(data) + 16
		for i := 0; i < limit; i++ {
			if _, ok := l.next(); !ok {
				return
			}
		}
		t.Fatalf("lexer did not terminate within %d tokens for %d bytes", limit, len(data))
	})
}
