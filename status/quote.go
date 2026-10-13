package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// shellQuote quotes a word the way bash's `printf %q` does in a UTF-8
// locale, so the commands status suggests can be pasted back into a
// shell and say exactly what bin/valley's own printf said. A branch name
// may hold most punctuation, and every character a shell would read
// specially is escaped with a backslash. A word holding a character that
// is not printable, or bytes that are not UTF-8, is written in $'...'
// form instead, with each such byte as an octal escape.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !printable(s) {
		return ansiCQuote(s)
	}
	var b strings.Builder
	for i, r := range s {
		if r < utf8.RuneSelf && backslashed(byte(r), s, i) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// backslashed is bash's table of characters a backslash must quote, plus
// the two it quotes by position: # opening a word starts a comment, and ~
// opening a word or following : or = is a tilde expansion.
func backslashed(c byte, s string, i int) bool {
	switch c {
	case '\t', '\n', ' ', '!', '"', '$', '&', '\'', '(', ')', '*', ',', ';', '<', '>',
		'?', '[', '\\', ']', '^', '`', '{', '|', '}':
		return true
	case '#':
		return i == 0
	case '~':
		return i == 0 || s[i-1] == ':' || s[i-1] == '='
	}
	return false
}

func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// ansiCQuote is the $'...' form. A backslash and a single quote are
// escaped, the control characters with a name are written by it, and
// every other byte that is not part of a printable character is written
// as three octal digits.
func ansiCQuote(s string) string {
	var b strings.Builder
	b.WriteString("$'")
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\\' || r == '\'':
			b.WriteByte('\\')
			b.WriteRune(r)
		case named[r] != 0:
			b.WriteByte('\\')
			b.WriteByte(named[r])
		case (r != utf8.RuneError || size > 1) && unicode.IsPrint(r):
			b.WriteString(s[i : i+size])
		default:
			for _, c := range []byte(s[i : i+size]) {
				fmt.Fprintf(&b, "\\%03o", c)
			}
		}
		i += size
	}
	b.WriteByte('\'')
	return b.String()
}

var named = map[rune]byte{
	'\x1b': 'E', '\a': 'a', '\v': 'v', '\b': 'b', '\f': 'f', '\n': 'n', '\r': 'r', '\t': 't',
}
