// valley-go style: review's page, styled for a terminal. review pages a
// plain buffer, which the editor opens and notes are read from, and shows
// a rendering of it (reviewpage.go's page): the diff pager's, then
// style's. style comes last so that the pager never reads its escapes,
// and it changes no line's place, so the editor opens the buffer on the
// line the view shows.
//
// It styles what compare writes (compare.go) and leaves every other line
// as the pager rendered it: a comparison's title in bold, with "edited"
// in yellow and "unchanged" or "same file changes" in green, the lines
// under the title with hashes dim, and each section heading in cyan. The
// lines under the title end at the first section heading or diff. It
// uses only the basic attributes and the eight standard colours, which
// read on dark and light terminals alike.
//
// It styles only for a terminal, and never when NO_COLOR is set to
// anything but the empty string: when its output is a terminal, or with
// --paged, when the caller has checked that the page goes to one.

package main

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strings"
)

const styleUsage = "usage: valley-go style [--paged] < page"

var (
	styleTitle   = regexp.MustCompile(`^\S+ v[0-9]+ at [0-9a-f]+: (rebased|recommitted|edited) since `)
	styleHeading = regexp.MustCompile(`^── .+ ──$`)
	styleWord    = regexp.MustCompile(`\b(edited|unchanged|same file changes)\b`)
	styleHash    = regexp.MustCompile(`\b[0-9a-f]{7,64}\b`)
	styleEscape  = regexp.MustCompile("\x1b\\[[0-9;]*m")
)

func runStyle(args []string, stdin io.Reader, stdout io.Writer) error {
	paged := false
	for _, a := range args {
		if a != "--paged" {
			return failure(styleUsage)
		}
		paged = true
	}
	on := terminalOf(stdout).bold || paged && os.Getenv("NO_COLOR") == ""
	w := bufio.NewWriter(stdout)
	if !on {
		if _, err := io.Copy(w, stdin); err != nil {
			return err
		}
		return w.Flush()
	}
	r := bufio.NewReader(stdin)
	header := false
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			body, end := strings.CutSuffix(line, "\n")
			w.WriteString(styledLine(body, &header))
			if end {
				w.WriteString("\n")
			}
		}
		if err == io.EOF {
			return w.Flush()
		}
		if err != nil {
			return err
		}
	}
}

// styledLine is one line of the page styled, given whether it is under a
// comparison's title, which it updates.
func styledLine(line string, header *bool) string {
	plain := styleEscape.ReplaceAllString(line, "")
	switch {
	case styleHeading.MatchString(plain):
		*header = false
		return sgrSpan("36", "39", plain)
	case styleTitle.MatchString(plain):
		*header = true
		return sgrSpan("1", "22", styleWord.ReplaceAllStringFunc(plain, colourWord))
	case !*header:
		return line
	case strings.HasPrefix(plain, "diff --git "):
		*header = false
		return line
	}
	if !strings.HasPrefix(plain, " ") {
		plain = styleWord.ReplaceAllStringFunc(plain, colourWord)
	}
	return styleHash.ReplaceAllStringFunc(plain, func(h string) string {
		if !strings.ContainsAny(h, "0123456789") {
			return h
		}
		return sgrSpan("2", "22", h)
	})
}

func colourWord(w string) string {
	if w == "edited" {
		return sgrSpan("33", "39", w)
	}
	return sgrSpan("32", "39", w)
}

// sgrSpan is s between the SGR codes that turn an attribute on and off, the
// off code ending only that attribute, so that s can sit inside another.
func sgrSpan(on, off, s string) string {
	return "\x1b[" + on + "m" + s + "\x1b[" + off + "m"
}
