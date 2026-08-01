package pdf

import "testing"

func TestParseCMapBFChar(t *testing.T) {
	src := `/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
3 beginbfchar
<0003> <0020>
<0024> <0041>
<0025> <0042>
endbfchar
endcmap
`
	cm := ParseCMap([]byte(src))

	for _, c := range []struct {
		code uint32
		want string
	}{
		{0x0003, " "},
		{0x0024, "A"},
		{0x0025, "B"},
	} {
		got, ok := cm.Text(c.code)
		if !ok {
			t.Errorf("code %04X has no mapping", c.code)
			continue
		}
		if string(got) != c.want {
			t.Errorf("code %04X = %q, want %q", c.code, string(got), c.want)
		}
	}

	if _, ok := cm.Text(0x9999); ok {
		t.Error("unmapped code reported a mapping")
	}
}

func TestParseCMapBFRange(t *testing.T) {
	src := `begincmap
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
2 beginbfrange
<0030> <0039> <0030>
<0041> <0043> [ <0058> <0059> <005A> ]
endbfrange
endcmap
`
	cm := ParseCMap([]byte(src))

	// Incrementing form: 0x30..0x39 maps to '0'..'9'.
	for i := uint32(0); i <= 9; i++ {
		got, ok := cm.Text(0x30 + i)
		if !ok {
			t.Errorf("code %04X has no mapping", 0x30+i)
			continue
		}
		want := rune('0' + i)
		if len(got) != 1 || got[0] != want {
			t.Errorf("code %04X = %q, want %q", 0x30+i, string(got), string(want))
		}
	}

	// Array form: explicit destinations per code.
	for i, want := range []rune{'X', 'Y', 'Z'} {
		got, ok := cm.Text(0x41 + uint32(i))
		if !ok || len(got) != 1 || got[0] != want {
			t.Errorf("code %04X = %q, want %q", 0x41+i, string(got), string(want))
		}
	}
}

func TestParseCMapSurrogatePair(t *testing.T) {
	// A destination outside the BMP arrives as a UTF-16 surrogate pair.
	src := `begincmap
1 beginbfchar
<0001> <D83DDE00>
endbfchar
endcmap
`
	cm := ParseCMap([]byte(src))
	got, ok := cm.Text(1)
	if !ok {
		t.Fatal("no mapping for code 1")
	}
	if len(got) != 1 || got[0] != 0x1F600 {
		t.Errorf("got %v, want [U+1F600]", got)
	}
}

func TestParseCMapCIDRange(t *testing.T) {
	src := `begincmap
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
1 begincidrange
<0020> <007E> 1
endcidrange
endcmap
`
	cm := ParseCMap([]byte(src))
	if got := cm.CID(0x20); got != 1 {
		t.Errorf("CID(0x20) = %d, want 1", got)
	}
	if got := cm.CID(0x21); got != 2 {
		t.Errorf("CID(0x21) = %d, want 2", got)
	}
}

func TestCMapNextCodeWidths(t *testing.T) {
	// A mixed-width CMap: one-byte codes below 0x80, two-byte above.
	src := `begincmap
2 begincodespacerange
<00> <7F>
<8000> <FFFF>
endcodespacerange
endcmap
`
	cm := ParseCMap([]byte(src))

	code, n, ok := cm.NextCode([]byte{0x41, 0x42})
	if !ok || n != 1 || code != 0x41 {
		t.Errorf("single-byte: code=%X n=%d ok=%v, want 41/1/true", code, n, ok)
	}

	code, n, ok = cm.NextCode([]byte{0x80, 0x01})
	if !ok || n != 2 || code != 0x8001 {
		t.Errorf("two-byte: code=%X n=%d ok=%v, want 8001/2/true", code, n, ok)
	}
}

func TestIdentityCMap(t *testing.T) {
	cm := IdentityCMap()
	code, n, ok := cm.NextCode([]byte{0x01, 0x2C, 0xFF})
	if !ok || n != 2 || code != 0x012C {
		t.Errorf("code=%X n=%d ok=%v, want 12C/2/true", code, n, ok)
	}
	if got := cm.CID(0x012C); got != 0x012C {
		t.Errorf("CID = %X, want 12C (identity)", got)
	}
}

func TestCMapNextCodeAlwaysProgresses(t *testing.T) {
	// A code matching no declared codespace must still consume bytes, or the
	// decode loop would spin.
	src := `begincmap
1 begincodespacerange
<0000> <00FF>
endcodespacerange
endcmap
`
	cm := ParseCMap([]byte(src))
	_, n, ok := cm.NextCode([]byte{0xFF, 0xFF})
	if !ok || n < 1 {
		t.Errorf("n=%d ok=%v, want at least one byte consumed", n, ok)
	}
}

func TestParseCMapTruncated(t *testing.T) {
	// A truncated CMap must return whatever it recovered rather than hang.
	src := `begincmap
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
2 beginbfchar
<0003> <0020>
<0024>`
	cm := ParseCMap([]byte(src))
	if got, ok := cm.Text(3); !ok || string(got) != " " {
		t.Errorf("the complete entry before truncation was lost: %q %v", string(got), ok)
	}
}

func TestParseCMapWMode(t *testing.T) {
	cm := ParseCMap([]byte("begincmap\n/WMode 1 def\nendcmap\n"))
	if !cm.Vertical() {
		t.Error("WMode 1 did not set vertical writing mode")
	}
}

func FuzzParseCMap(f *testing.F) {
	f.Add([]byte("begincmap\n1 beginbfchar\n<01> <0041>\nendbfchar\nendcmap"))
	f.Add([]byte("begincodespacerange <0000> <FFFF> endcodespacerange"))
	f.Add([]byte("1 begincidrange <00> <FF> 1 endcidrange"))
	f.Add([]byte("beginbfrange <0> <9> [ <41> <42> ]"))

	f.Fuzz(func(t *testing.T, data []byte) {
		cm := ParseCMap(data)
		if cm == nil {
			t.Fatal("ParseCMap returned nil")
		}
		// Exercise the lookup paths on the recovered map.
		_, _ = cm.Text(0x41)
		_ = cm.CID(0x41)
		_, n, _ := cm.NextCode([]byte{0x41, 0x42})
		if n < 0 {
			t.Fatalf("NextCode returned a negative width %d", n)
		}
	})
}
