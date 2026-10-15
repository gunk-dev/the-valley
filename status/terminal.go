package main

import (
	"io"
	"os"
	"strings"
	"unicode"
)

// terminal is what valley pending's and valley plan's text is laid out
// for: a width in columns, and whether text is styled, with names in bold
// and plan's glyphs in colour.
type terminal struct {
	width int
	bold  bool
}

// terminalOf is the terminal w writes to. Its width is the terminal's
// own, or 80 columns when w is no terminal or the terminal does not know
// its width. Text is styled on a terminal, unless NO_COLOR is set to
// anything but the empty string.
func terminalOf(w io.Writer) terminal {
	t := terminal{width: 80}
	f, ok := w.(*os.File)
	if !ok {
		return t
	}
	cols, isTerminal := windowColumns(f)
	if !isTerminal {
		return t
	}
	if cols > 0 {
		t.width = cols
	}
	t.bold = os.Getenv("NO_COLOR") == ""
	return t
}

// strong is s in bold, on a terminal that shows it.
func (t terminal) strong(s string) string {
	if !t.bold {
		return s
	}
	return "\x1b[1m" + s + "\x1b[0m"
}

// tint is s in the colour an SGR code names, on a terminal that shows
// it. An empty code leaves s as it is.
func (t terminal) tint(code, s string) string {
	if !t.bold || code == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// fit is s cut to the columns a line has left after lead.
func (t terminal) fit(s, lead string) string {
	return fit(s, t.width-columns(lead))
}

// fit is s cut to at most n columns, ending in … where it was cut.
func fit(s string, n int) string {
	if columns(s) <= n {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		if used+runeColumns(r) > n-1 {
			break
		}
		b.WriteRune(r)
		used += runeColumns(r)
	}
	return strings.TrimRight(b.String(), " ") + "…"
}

// columns is how many columns a terminal gives s.
func columns(s string) int {
	n := 0
	for _, r := range s {
		n += runeColumns(r)
	}
	return n
}

// runeColumns is how many columns a terminal gives r: none for a mark that
// combines with the character before it, two for a wide character, and
// one for any other.
func runeColumns(r rune) int {
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	for _, w := range wideRunes {
		if r >= w[0] && r <= w[1] {
			return 2
		}
	}
	return 1
}

// wideRunes are the main blocks of East Asian wide and fullwidth
// characters, and of emoji, which a terminal draws two columns wide.
var wideRunes = [][2]rune{
	{0x1100, 0x115f}, {0x2e80, 0x303e}, {0x3041, 0x33ff}, {0x3400, 0x4dbf},
	{0x4e00, 0x9fff}, {0xa000, 0xa4cf}, {0xac00, 0xd7a3}, {0xf900, 0xfaff},
	{0xfe30, 0xfe4f}, {0xff00, 0xff60}, {0xffe0, 0xffe6}, {0x1f300, 0x1f64f},
	{0x1f900, 0x1f9ff}, {0x20000, 0x3fffd},
}
