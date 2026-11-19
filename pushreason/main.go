// Command pushreason makes what git printed for a failed push into the one
// line a valley host logs as the reason. It reads git's error output on
// stdin and writes the line on stdout.
//
// The text is the remote's to choose, and the line lands in the host's
// journal and in telemetry, so it is made fit to log there:
//
//   - invalid UTF-8 is replaced, a tab becomes a space, and other control
//     characters are deleted, since they would rewrite an operator's
//     terminal and make the journal store the line as binary;
//   - a line longer than lineLimit is replaced whole, without being held
//     whole, since a cut could leave part of a credential without the mark
//     that would drop it;
//   - git's own framing is dropped, which leaves the remote's refusal (its
//     remote: lines and the rejected ref) or what stopped the connection;
//   - any word holding one of the marks, the signs of an address or a
//     parameter, is replaced whole, so no part of a credential such a
//     word carries is kept. Nothing tells a reference's shape apart from
//     another's: every shape git, ssh or a remote can print holds a mark
//     where it holds a credential. A word ends only at a space, since any
//     other whitespace, such as a no-break space, can sit inside a
//     password;
//   - the last keptLines lines are kept, joined, and cut to their last
//     reasonLimit bytes, starting on a whole character, where a long
//     refusal ends with its conclusion.
//
// A secret the remote prints as a plain word passes through: the line is
// for diagnosis, and whoever reads the journal reads it.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// lineLimit is the most of one line kept, in bytes.
	lineLimit = 1024
	// keptLines is how many of the last lines make the reason.
	keptLines = 5
	// reasonLimit is the most of the reason kept, in bytes.
	reasonLimit = 600

	tooLong   = "(a line too long to keep)"
	separator = " · "
	dropped   = "[…]"
)

// marks are what make a word an address or a parameter: a scheme's ://,
// a user's @, and a query's ?, = and the % of an encoded value.
var marks = []string{"://", "@", "?", "=", "%"}

func main() {
	line, err := reason(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pushreason: reading git's output: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(line)
}

// reason reads r to its end and returns the reason line.
func reason(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	var kept []string
	for {
		raw, cut, err := readLine(br)
		if len(raw) > 0 || cut {
			if line := clean(raw, cut); line != "" {
				kept = append(kept, line)
				if len(kept) > keptLines {
					kept = kept[1:]
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return tail(strings.Join(kept, separator)), nil
}

// readLine returns the next line of r without its newline. It holds at
// most lineLimit bytes of it, and cut reports a line longer than that.
func readLine(r *bufio.Reader) (line []byte, cut bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		chunk = trimNewline(chunk)
		if !cut && len(line)+len(chunk) <= lineLimit {
			line = append(line, chunk...)
		} else {
			cut, line = true, nil
		}
		if err != bufio.ErrBufferFull {
			return line, cut, err
		}
	}
}

func trimNewline(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		return b[:n-1]
	}
	return b
}

// clean makes one line fit to log, or returns "" for a line that says
// nothing of the remote's.
func clean(raw []byte, cut bool) string {
	if cut {
		return tooLong
	}
	text := strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, strings.ToValidUTF8(string(raw), "\uFFFD"))
	var words []string
	for _, w := range strings.Split(text, " ") {
		if w != "" {
			words = append(words, w)
		}
	}
	line := strings.Join(words, " ")
	if framing(line) {
		return ""
	}
	for i, w := range words {
		for _, m := range marks {
			if strings.Contains(w, m) {
				words[i] = dropped
				break
			}
		}
	}
	return strings.Join(words, " ")
}

// framing reports a line git prints around the remote's words: the
// destination, its hints, and its closing error. A blank line too.
func framing(line string) bool {
	return line == "" ||
		strings.HasPrefix(line, "To ") ||
		strings.HasPrefix(line, "hint:") ||
		strings.HasPrefix(line, "error: failed to push")
}

// tail keeps the last reasonLimit bytes of s, from the start of a whole
// character.
func tail(s string) string {
	if len(s) <= reasonLimit {
		return s
	}
	s = s[len(s)-reasonLimit:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return strings.TrimLeft(s, " ")
}
