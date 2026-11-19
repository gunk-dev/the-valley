package main

import (
	"bufio"
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

func mustReason(t *testing.T, in string) string {
	t.Helper()
	got, err := reason(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A refusal as git prints it: the remote's lines, padded the way git pads
// them when its output is not a terminal, then its own framing.
func TestRefusal(t *testing.T) {
	in := "remote: main is protected        \n" +
		"To ../mirror.git\n" +
		" ! [remote rejected] main -> main (pre-receive hook declined)\n" +
		"error: failed to push some refs to '../mirror.git'\n" +
		"hint: Updates were rejected\n\n"
	want := "remote: main is protected · ! [remote rejected] main -> main (pre-receive hook declined)"
	if got := mustReason(t, in); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEmpty(t *testing.T) {
	if got := mustReason(t, ""); got != "" {
		t.Errorf("got %q from no output", got)
	}
}

func TestControlCharacters(t *testing.T) {
	in := "remote: \x1b[2Kerased\r\x7f \u009b31mred\x00\n"
	got := mustReason(t, in)
	if want := "remote: [2Kerased 31mred"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInvalidUTF8(t *testing.T) {
	got := mustReason(t, "remote: bad \xff\xfe byte\n")
	if !utf8.ValidString(got) || !strings.Contains(got, "bad") {
		t.Errorf("got %q, want valid UTF-8 keeping the text", got)
	}
}

// A long line is replaced whole, however long, and the lines around it
// are kept.
func TestLongLine(t *testing.T) {
	long := "remote: https://operator:s3cret-token@" + strings.Repeat("x", 1<<20)
	in := io.MultiReader(
		strings.NewReader("remote: before\n"),
		strings.NewReader(long),
		strings.NewReader("\nremote: after\n"),
	)
	got, err := reason(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := "remote: before · " + tooLong + " · remote: after"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// readLine holds a line of exactly lineLimit bytes whole, and cuts one
// byte more.
func TestLineLimit(t *testing.T) {
	exact := strings.Repeat("y", lineLimit)
	for _, c := range []struct {
		in  string
		cut bool
	}{{exact, false}, {exact + "y", true}} {
		line, cut, _ := readLine(bufio.NewReader(strings.NewReader(c.in + "\n")))
		if cut != c.cut || (!cut && string(line) != c.in) {
			t.Errorf("a line of %d bytes: cut %v, want %v", len(c.in), cut, c.cut)
		}
	}
}

// A word holding any of the marks is replaced whole, whatever its shape,
// and a word or phrase holding none stands.
func TestWords(t *testing.T) {
	for _, w := range []string{
		"git@example.org:app.git?access_token=s3cret",
		"https://example.org/a,https://operator:s3cret@example.org/b",
		"https://example.org/rules?access_token=s3cret",
		"https://example.org?token=a@s3cret",
		"https://operator:s3cret@example.org/app.git",
		"https://operator:prefix\u00a0S3CRET\u00a0suffix@example.org/app.git",
		"ssh://git@example.org/app.git",
		"operator:s3cret@example.org",
		"access_token%3Ds3cret",
		"token=s3cret",
		"'https://example.org/app.git/':",
	} {
		got := mustReason(t, "remote: see "+w+" now\n")
		if want := "remote: see " + dropped + " now"; got != want {
			t.Errorf("%q: got %q, want %q", w, got, want)
		}
	}
	for _, p := range []string{
		"refs/heads/main",
		"! [remote rejected] main -> main (pre-receive hook declined)",
		"main is protected: changes must be made through a review, and 2 checks must pass.",
		"host:path",
		"../mirror.git",
		"fatal: Could not read from remote repository.",
	} {
		if got := mustReason(t, "remote: "+p+"\n"); got != "remote: "+p {
			t.Errorf("%q: got %q", p, got)
		}
	}
}

// A tab separates words as a space does, so a credential behind one is a
// word of its own, and is dropped.
func TestTab(t *testing.T) {
	got := mustReason(t, "remote: see\thttps://operator:s3cret@example.org\tnow\n")
	if want := "remote: see " + dropped + " now"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLastLines(t *testing.T) {
	var in strings.Builder
	for _, w := range []string{"one", "two", "three", "four", "five", "six", "seven"} {
		in.WriteString("remote: " + w + "\n")
	}
	want := "remote: three · remote: four · remote: five · remote: six · remote: seven"
	if got := mustReason(t, in.String()); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The reason keeps its last reasonLimit bytes, and a multi-byte character
// at the cut is dropped rather than split.
func TestCutAtACharacter(t *testing.T) {
	// "é" is two bytes, and the filler puts the cut one byte into one.
	prefix, filler, last := "remote: ", strings.Repeat("é", 400), "remote: the conclusion!"
	cutAt := len(prefix+filler+separator+last) - reasonLimit
	if cutAt < len(prefix) || (cutAt-len(prefix))%2 != 1 {
		t.Fatalf("the cut at %d falls outside a character, so the test proves nothing", cutAt)
	}
	got := mustReason(t, prefix+filler+"\n"+last+"\n")
	if len(got) > reasonLimit {
		t.Errorf("the reason is %d bytes, over %d", len(got), reasonLimit)
	}
	if !utf8.ValidString(got) {
		t.Errorf("the cut split a character: %q", got[:8])
	}
	if !strings.HasPrefix(got, "é") || !strings.HasSuffix(got, separator+last) {
		t.Errorf("got %q, want whole characters up to the conclusion", got)
	}
}
