package pdf

import (
	"strconv"
	"strings"
)

// The simple-font encoding tables from PDF 32000-1 Annex D, flattened to
// code->rune. Entry 0 means "undefined at this code".
//
// These are the third step of the mapping precedence in spec section 4.2,
// after /ToUnicode and /Encoding /Differences.

// StandardEncoding is Adobe StandardEncoding.
var StandardEncoding = [256]rune{
	32: ' ', 33: '!', 34: '"', 35: '#', 36: '$', 37: '%', 38: '&', 39: '’',
	40: '(', 41: ')', 42: '*', 43: '+', 44: ',', 45: '-', 46: '.', 47: '/',
	48: '0', 49: '1', 50: '2', 51: '3', 52: '4', 53: '5', 54: '6', 55: '7',
	56: '8', 57: '9', 58: ':', 59: ';', 60: '<', 61: '=', 62: '>', 63: '?',
	64: '@', 65: 'A', 66: 'B', 67: 'C', 68: 'D', 69: 'E', 70: 'F', 71: 'G',
	72: 'H', 73: 'I', 74: 'J', 75: 'K', 76: 'L', 77: 'M', 78: 'N', 79: 'O',
	80: 'P', 81: 'Q', 82: 'R', 83: 'S', 84: 'T', 85: 'U', 86: 'V', 87: 'W',
	88: 'X', 89: 'Y', 90: 'Z', 91: '[', 92: '\\', 93: ']', 94: '^', 95: '_',
	96: '‘', 97: 'a', 98: 'b', 99: 'c', 100: 'd', 101: 'e', 102: 'f',
	103: 'g', 104: 'h', 105: 'i', 106: 'j', 107: 'k', 108: 'l', 109: 'm',
	110: 'n', 111: 'o', 112: 'p', 113: 'q', 114: 'r', 115: 's', 116: 't',
	117: 'u', 118: 'v', 119: 'w', 120: 'x', 121: 'y', 122: 'z', 123: '{',
	124: '|', 125: '}', 126: '~',
	161: '¡', 162: '¢', 163: '£', 164: '⁄', 165: '¥',
	166: 'ƒ', 167: '§', 168: '¤', 169: '\'', 170: '“',
	171: '«', 172: '‹', 173: '›', 174: 'ﬁ', 175: 'ﬂ',
	177: '–', 178: '†', 179: '‡', 180: '·', 182: '¶',
	183: '•', 184: '‚', 185: '„', 186: '”', 187: '»',
	188: '…', 189: '‰', 191: '¿', 193: '`', 194: '´',
	195: 'ˆ', 196: '˜', 197: '¯', 198: '˘', 199: '˙',
	200: '¨', 202: '˚', 203: '¸', 205: '˝', 206: '˛',
	207: 'ˇ', 208: '—', 225: 'Æ', 227: 'ª', 232: 'Ł',
	233: 'Ø', 234: 'Œ', 235: 'º', 241: 'æ', 245: 'ı',
	248: 'ł', 249: 'ø', 250: 'œ', 251: 'ß',
}

// WinAnsiEncoding is the PDF flavor of Windows code page 1252.
var WinAnsiEncoding = [256]rune{
	32: ' ', 33: '!', 34: '"', 35: '#', 36: '$', 37: '%', 38: '&', 39: '\'',
	40: '(', 41: ')', 42: '*', 43: '+', 44: ',', 45: '-', 46: '.', 47: '/',
	48: '0', 49: '1', 50: '2', 51: '3', 52: '4', 53: '5', 54: '6', 55: '7',
	56: '8', 57: '9', 58: ':', 59: ';', 60: '<', 61: '=', 62: '>', 63: '?',
	64: '@', 65: 'A', 66: 'B', 67: 'C', 68: 'D', 69: 'E', 70: 'F', 71: 'G',
	72: 'H', 73: 'I', 74: 'J', 75: 'K', 76: 'L', 77: 'M', 78: 'N', 79: 'O',
	80: 'P', 81: 'Q', 82: 'R', 83: 'S', 84: 'T', 85: 'U', 86: 'V', 87: 'W',
	88: 'X', 89: 'Y', 90: 'Z', 91: '[', 92: '\\', 93: ']', 94: '^', 95: '_',
	96: '`', 97: 'a', 98: 'b', 99: 'c', 100: 'd', 101: 'e', 102: 'f', 103: 'g',
	104: 'h', 105: 'i', 106: 'j', 107: 'k', 108: 'l', 109: 'm', 110: 'n',
	111: 'o', 112: 'p', 113: 'q', 114: 'r', 115: 's', 116: 't', 117: 'u',
	118: 'v', 119: 'w', 120: 'x', 121: 'y', 122: 'z', 123: '{', 124: '|',
	125: '}', 126: '~',
	128: '€', 130: '‚', 131: 'ƒ', 132: '„', 133: '…',
	134: '†', 135: '‡', 136: 'ˆ', 137: '‰', 138: 'Š',
	139: '‹', 140: 'Œ', 142: 'Ž', 145: '‘', 146: '’',
	147: '“', 148: '”', 149: '•', 150: '–', 151: '—',
	152: '˜', 153: '™', 154: 'š', 155: '›', 156: 'œ',
	158: 'ž', 159: 'Ÿ', 160: ' ', 161: '¡', 162: '¢',
	163: '£', 164: '¤', 165: '¥', 166: '¦', 167: '§',
	168: '¨', 169: '©', 170: 'ª', 171: '«', 172: '¬',
	173: '-', 174: '®', 175: '¯', 176: '°', 177: '±',
	178: '²', 179: '³', 180: '´', 181: 'µ', 182: '¶',
	183: '·', 184: '¸', 185: '¹', 186: 'º', 187: '»',
	188: '¼', 189: '½', 190: '¾', 191: '¿', 192: 'À',
	193: 'Á', 194: 'Â', 195: 'Ã', 196: 'Ä', 197: 'Å',
	198: 'Æ', 199: 'Ç', 200: 'È', 201: 'É', 202: 'Ê',
	203: 'Ë', 204: 'Ì', 205: 'Í', 206: 'Î', 207: 'Ï',
	208: 'Ð', 209: 'Ñ', 210: 'Ò', 211: 'Ó', 212: 'Ô',
	213: 'Õ', 214: 'Ö', 215: '×', 216: 'Ø', 217: 'Ù',
	218: 'Ú', 219: 'Û', 220: 'Ü', 221: 'Ý', 222: 'Þ',
	223: 'ß', 224: 'à', 225: 'á', 226: 'â', 227: 'ã',
	228: 'ä', 229: 'å', 230: 'æ', 231: 'ç', 232: 'è',
	233: 'é', 234: 'ê', 235: 'ë', 236: 'ì', 237: 'í',
	238: 'î', 239: 'ï', 240: 'ð', 241: 'ñ', 242: 'ò',
	243: 'ó', 244: 'ô', 245: 'õ', 246: 'ö', 247: '÷',
	248: 'ø', 249: 'ù', 250: 'ú', 251: 'û', 252: 'ü',
	253: 'ý', 254: 'þ', 255: 'ÿ',
}

// MacRomanEncoding is the PDF flavor of Mac OS Roman. It diverges from the
// platform encoding at a handful of codes, notably 219 (currency, not euro).
var MacRomanEncoding = [256]rune{
	32: ' ', 33: '!', 34: '"', 35: '#', 36: '$', 37: '%', 38: '&', 39: '\'',
	40: '(', 41: ')', 42: '*', 43: '+', 44: ',', 45: '-', 46: '.', 47: '/',
	48: '0', 49: '1', 50: '2', 51: '3', 52: '4', 53: '5', 54: '6', 55: '7',
	56: '8', 57: '9', 58: ':', 59: ';', 60: '<', 61: '=', 62: '>', 63: '?',
	64: '@', 65: 'A', 66: 'B', 67: 'C', 68: 'D', 69: 'E', 70: 'F', 71: 'G',
	72: 'H', 73: 'I', 74: 'J', 75: 'K', 76: 'L', 77: 'M', 78: 'N', 79: 'O',
	80: 'P', 81: 'Q', 82: 'R', 83: 'S', 84: 'T', 85: 'U', 86: 'V', 87: 'W',
	88: 'X', 89: 'Y', 90: 'Z', 91: '[', 92: '\\', 93: ']', 94: '^', 95: '_',
	96: '`', 97: 'a', 98: 'b', 99: 'c', 100: 'd', 101: 'e', 102: 'f', 103: 'g',
	104: 'h', 105: 'i', 106: 'j', 107: 'k', 108: 'l', 109: 'm', 110: 'n',
	111: 'o', 112: 'p', 113: 'q', 114: 'r', 115: 's', 116: 't', 117: 'u',
	118: 'v', 119: 'w', 120: 'x', 121: 'y', 122: 'z', 123: '{', 124: '|',
	125: '}', 126: '~',
	128: 'Ä', 129: 'Å', 130: 'Ç', 131: 'É', 132: 'Ñ',
	133: 'Ö', 134: 'Ü', 135: 'á', 136: 'à', 137: 'â',
	138: 'ä', 139: 'ã', 140: 'å', 141: 'ç', 142: 'é',
	143: 'è', 144: 'ê', 145: 'ë', 146: 'í', 147: 'ì',
	148: 'î', 149: 'ï', 150: 'ñ', 151: 'ó', 152: 'ò',
	153: 'ô', 154: 'ö', 155: 'õ', 156: 'ú', 157: 'ù',
	158: 'û', 159: 'ü', 160: '†', 161: '°', 162: '¢',
	163: '£', 164: '§', 165: '•', 166: '¶', 167: 'ß',
	168: '®', 169: '©', 170: '™', 171: '´', 172: '¨',
	173: '≠', 174: 'Æ', 175: 'Ø', 176: '∞', 177: '±',
	178: '≤', 179: '≥', 180: '¥', 181: 'µ', 182: '∂',
	183: '∑', 184: '∏', 185: 'π', 186: '∫', 187: 'ª',
	188: 'º', 189: 'Ω', 190: 'æ', 191: 'ø', 192: '¿',
	193: '¡', 194: '¬', 195: '√', 196: 'ƒ', 197: '≈',
	198: '∆', 199: '«', 200: '»', 201: '…', 202: ' ',
	203: 'À', 204: 'Ã', 205: 'Õ', 206: 'Œ', 207: 'œ',
	208: '–', 209: '—', 210: '“', 211: '”', 212: '‘',
	213: '’', 214: '÷', 215: '◊', 216: 'ÿ', 217: 'Ÿ',
	218: '⁄', 219: '¤', 220: '‹', 221: '›', 222: 'ﬁ',
	223: 'ﬂ', 224: '‡', 225: '·', 226: '‚', 227: '„',
	228: '‰', 229: 'Â', 230: 'Ê', 231: 'Á', 232: 'Ë',
	233: 'È', 234: 'Í', 235: 'Î', 236: 'Ï', 237: 'Ì',
	238: 'Ó', 239: 'Ô', 241: 'Ò', 242: 'Ú', 243: 'Û',
	244: 'Ù', 245: 'ı', 246: 'ˆ', 247: '˜', 248: '¯',
	249: '˘', 250: '˙', 251: '˚', 252: '¸', 253: '˝',
	254: '˛', 255: 'ˇ',
}

// PDFDocEncoding covers text strings in document metadata rather than page
// content. It matches Latin-1 above 160 and fills 24-31 and 128-160 with
// typographic characters.
var PDFDocEncoding = func() [256]rune {
	var e [256]rune
	copy(e[:], WinAnsiEncoding[:])
	for i := 128; i < 160; i++ {
		e[i] = 0
	}
	for _, kv := range []struct {
		c byte
		r rune
	}{
		{24, '˘'}, {25, 'ˇ'}, {26, 'ˆ'}, {27, '˙'},
		{28, '˝'}, {29, '˛'}, {30, '˚'}, {31, '˜'},
		{128, '•'}, {129, '†'}, {130, '‡'}, {131, '…'},
		{132, '—'}, {133, '–'}, {134, 'ƒ'}, {135, '⁄'},
		{136, '‹'}, {137, '›'}, {138, '−'}, {139, '‰'},
		{140, '„'}, {141, '“'}, {142, '”'}, {143, '‘'},
		{144, '’'}, {145, '‚'}, {146, '™'}, {147, 'ﬁ'},
		{148, 'ﬂ'}, {149, 'Ł'}, {150, 'Œ'}, {151, 'Š'},
		{152, 'Ÿ'}, {153, 'Ž'}, {154, 'ı'}, {155, 'ł'},
		{156, 'œ'}, {157, 'š'}, {158, 'ž'}, {160, '€'},
	} {
		e[kv.c] = kv.r
	}
	return e
}()

// encodingByName resolves a /BaseEncoding or /Encoding name.
func encodingByName(n string) (*[256]rune, bool) {
	switch n {
	case "WinAnsiEncoding":
		return &WinAnsiEncoding, true
	case "MacRomanEncoding":
		return &MacRomanEncoding, true
	case "StandardEncoding":
		return &StandardEncoding, true
	case "PDFDocEncoding":
		return &PDFDocEncoding, true
	case "MacExpertEncoding":
		// The expert set holds small caps and old-style figures whose glyph
		// names have no clean Unicode mapping. Standard is a closer guess than
		// leaving every code undefined.
		return &StandardEncoding, true
	}
	return nil, false
}

// glyphNames maps Adobe glyph names to runes. It covers every name reachable
// from the encoding tables above plus the names producers most often emit in
// /Differences. Names outside this set fall through to the algorithmic uniXXXX
// forms handled by GlyphNameToRune.
var glyphNames = map[string]rune{
	"space": ' ', "exclam": '!', "quotedbl": '"', "numbersign": '#',
	"dollar": '$', "percent": '%', "ampersand": '&', "quotesingle": '\'',
	"parenleft": '(', "parenright": ')', "asterisk": '*', "plus": '+',
	"comma": ',', "hyphen": '-', "period": '.', "slash": '/',
	"zero": '0', "one": '1', "two": '2', "three": '3', "four": '4',
	"five": '5', "six": '6', "seven": '7', "eight": '8', "nine": '9',
	"colon": ':', "semicolon": ';', "less": '<', "equal": '=',
	"greater": '>', "question": '?', "at": '@',
	"bracketleft": '[', "backslash": '\\', "bracketright": ']',
	"asciicircum": '^', "underscore": '_', "grave": '`',
	"braceleft": '{', "bar": '|', "braceright": '}', "asciitilde": '~',

	"quoteleft": '‘', "quoteright": '’',
	"quotedblleft": '“', "quotedblright": '”',
	"quotesinglbase": '‚', "quotedblbase": '„',
	"guilsinglleft": '‹', "guilsinglright": '›',
	"guillemotleft": '«', "guillemotright": '»',
	"endash": '–', "emdash": '—', "bullet": '•',
	"ellipsis": '…', "dagger": '†', "daggerdbl": '‡',
	"perthousand": '‰', "fraction": '⁄', "trademark": '™',
	"minus": '−', "florin": 'ƒ', "Euro": '€',
	"euro": '€', "currency": '¤',

	"fi": 'ﬁ', "fl": 'ﬂ', "ff": 'ﬀ',
	"ffi": 'ﬃ', "ffl": 'ﬄ',

	"exclamdown": '¡', "cent": '¢', "sterling": '£',
	"yen": '¥', "brokenbar": '¦', "section": '§',
	"dieresis": '¨', "copyright": '©', "ordfeminine": 'ª',
	"logicalnot": '¬', "registered": '®', "macron": '¯',
	"degree": '°', "plusminus": '±', "twosuperior": '²',
	"threesuperior": '³', "acute": '´', "mu": 'µ',
	"paragraph": '¶', "periodcentered": '·', "cedilla": '¸',
	"onesuperior": '¹', "ordmasculine": 'º', "onequarter": '¼',
	"onehalf": '½', "threequarters": '¾', "questiondown": '¿',
	"multiply": '×', "divide": '÷',
	"circumflex": 'ˆ', "tilde": '˜', "breve": '˘',
	"dotaccent": '˙', "ring": '˚', "hungarumlaut": '˝',
	"ogonek": '˛', "caron": 'ˇ', "dotlessi": 'ı',

	"Agrave": 'À', "Aacute": 'Á', "Acircumflex": 'Â',
	"Atilde": 'Ã', "Adieresis": 'Ä', "Aring": 'Å',
	"AE": 'Æ', "Ccedilla": 'Ç', "Egrave": 'È',
	"Eacute": 'É', "Ecircumflex": 'Ê', "Edieresis": 'Ë',
	"Igrave": 'Ì', "Iacute": 'Í', "Icircumflex": 'Î',
	"Idieresis": 'Ï', "Eth": 'Ð', "Ntilde": 'Ñ',
	"Ograve": 'Ò', "Oacute": 'Ó', "Ocircumflex": 'Ô',
	"Otilde": 'Õ', "Odieresis": 'Ö', "Oslash": 'Ø',
	"Ugrave": 'Ù', "Uacute": 'Ú', "Ucircumflex": 'Û',
	"Udieresis": 'Ü', "Yacute": 'Ý', "Thorn": 'Þ',
	"germandbls": 'ß',
	"agrave":     'à', "aacute": 'á', "acircumflex": 'â',
	"atilde": 'ã', "adieresis": 'ä', "aring": 'å',
	"ae": 'æ', "ccedilla": 'ç', "egrave": 'è',
	"eacute": 'é', "ecircumflex": 'ê', "edieresis": 'ë',
	"igrave": 'ì', "iacute": 'í', "icircumflex": 'î',
	"idieresis": 'ï', "eth": 'ð', "ntilde": 'ñ',
	"ograve": 'ò', "oacute": 'ó', "ocircumflex": 'ô',
	"otilde": 'õ', "odieresis": 'ö', "oslash": 'ø',
	"ugrave": 'ù', "uacute": 'ú', "ucircumflex": 'û',
	"udieresis": 'ü', "yacute": 'ý', "thorn": 'þ',
	"ydieresis": 'ÿ',

	"Lslash": 'Ł', "lslash": 'ł', "OE": 'Œ', "oe": 'œ',
	"Scaron": 'Š', "scaron": 'š', "Ydieresis": 'Ÿ',
	"Zcaron": 'Ž', "zcaron": 'ž',

	"notequal": '≠', "infinity": '∞', "lessequal": '≤',
	"greaterequal": '≥', "partialdiff": '∂', "summation": '∑',
	"product": '∏', "pi": 'π', "integral": '∫',
	"Omega": 'Ω', "radical": '√', "approxequal": '≈',
	"Delta": '∆', "lozenge": '◊',
}

// GlyphNameToRune resolves an Adobe glyph name. It handles the AGL table, the
// algorithmic uniXXXX and uXXXXXX forms, the "gNN"/"cidNN" fallbacks that
// carry no semantic value, and names with ".sc"-style suffixes.
//
// The second return is false when the name yields no usable rune, which the
// caller counts as a decode failure.
func GlyphNameToRune(name string) (rune, bool) {
	if name == "" {
		return 0, false
	}
	if r, ok := glyphNames[name]; ok {
		return r, true
	}

	// uniXXXX with one or more four-digit values; take the first.
	if strings.HasPrefix(name, "uni") && len(name) >= 7 {
		if v, err := strconv.ParseUint(name[3:7], 16, 32); err == nil {
			return rune(v), true
		}
	}
	// uXXXX through uXXXXXX.
	if strings.HasPrefix(name, "u") && len(name) >= 5 && len(name) <= 7 {
		if v, err := strconv.ParseUint(name[1:], 16, 32); err == nil {
			return rune(v), true
		}
	}

	// Suffixed variants: "a.sc", "one.oldstyle", "f_i.liga". Strip the suffix
	// and retry the base name.
	if i := strings.IndexByte(name, '.'); i > 0 {
		return GlyphNameToRune(name[:i])
	}

	// Ligature names joined by underscore: "f_i". Take the first component;
	// the remainder is lost, but a partial mapping beats U+FFFD.
	if i := strings.IndexByte(name, '_'); i > 0 {
		return GlyphNameToRune(name[:i])
	}

	// Single ASCII letter names used by minimal subsetted fonts.
	if len(name) == 1 && name[0] > 32 && name[0] < 127 {
		return rune(name[0]), true
	}

	return 0, false
}
