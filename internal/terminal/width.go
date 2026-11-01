package terminal

import "unicode"

// DisplayWidth counts terminal cells for ordinary text, CJK wide characters,
// and combining marks. East Asian ambiguous characters use one cell.
func DisplayWidth(text string) int {
	width := 0
	for _, character := range text {
		width += runeWidth(character)
	}
	return width
}

func runeWidth(character rune) int {
	if unicode.IsControl(character) || unicode.Is(unicode.Mn, character) || unicode.Is(unicode.Me, character) || unicode.Is(unicode.Cf, character) ||
		(character >= 0x1160 && character <= 0x11ff) {
		return 0
	}
	if character >= 0x1100 && (character <= 0x115f || character == 0x2329 || character == 0x232a ||
		(character >= 0x2e80 && character <= 0xa4cf && character != 0x303f) ||
		(character >= 0xac00 && character <= 0xd7a3) || (character >= 0xf900 && character <= 0xfaff) ||
		(character >= 0xfe10 && character <= 0xfe19) || (character >= 0xfe30 && character <= 0xfe6f) ||
		(character >= 0xff00 && character <= 0xff60) || (character >= 0xffe0 && character <= 0xffe6) ||
		(character >= 0x1f300 && character <= 0x1faff) || (character >= 0x20000 && character <= 0x3fffd)) {
		return 2
	}
	return 1
}
