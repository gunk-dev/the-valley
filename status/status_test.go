package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	sha1a   = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
	sha1b   = "2222222222222222222222222222222222222222"
	sha256a = "3333333333333333333333333333333333333333333333333333333333333333"
	fp      = "0123456789abcdef"
)

// An outcome record is read as a decision only when every field is the
// shape the integrator writes. Everything else is unknown, and none of it
// may read as a landing.
func TestMemoAcceptsOnlyTheRecordsTheIntegratorWrites(t *testing.T) {
	for _, c := range []struct {
		record, decision, failedAt string
	}{
		{sha1a + " " + sha1b + " " + fp + " land\n", "land", ""},
		{sha1a + " " + sha1b + " " + fp + " stale", "stale", ""},
		{sha1a + " " + sha1b + " " + fp + " reject\n\n\n", "reject", ""},
		{sha1a + " " + sha1b + " " + fp + " failed 2026-09-21T12:00:00Z\n", "failed", "2026-09-21T12:00:00Z"},
		{sha256a + " " + sha256a + " " + fp + " land\n", "land", ""},
		{"  " + sha1a + "\t" + sha1b + "  " + fp + "\tland  \n", "land", ""},

		{"", "unknown", ""},
		{"not an outcome\n", "unknown", ""},
		{sha1a + " " + sha1b + " " + fp + "\n", "unknown", ""},
		{sha1a + " " + sha1b + " " + fp + " merged\n", "unknown", ""},
		{sha1a + " " + sha1b + " " + fp + " land 2026-09-21T12:00:00Z\n", "unknown", ""},
		{sha1a + " " + sha1b + " " + fp + " failed\n", "unknown", ""},
		{sha1a + " " + sha1b + " " + fp + " failed 2026-09-21T12:00:00Z extra\n", "unknown", ""},
		{sha1a + " " + sha1b + " " + fp + " land\n" + sha1a + "\n", "unknown", ""},
		{sha1a + " " + sha1b + " " + fp + " land\x00\n", "unknown", ""},
		{strings.ToUpper(sha1a) + " " + sha1b + " " + fp + " land\n", "unknown", ""},
		{sha1a[:39] + " " + sha1b + " " + fp + " land\n", "unknown", ""},
		{sha1a + "1 " + sha1b + " " + fp + " land\n", "unknown", ""},
		{sha1a + " HEAD " + fp + " land\n", "unknown", ""},
		{sha1a + " " + sha1b + " " + fp[:15] + " land\n", "unknown", ""},
		{sha1a + " " + sha1b + " " + fp + "0 land\n", "unknown", ""},
		{sha1a + " " + sha1b + " " + fp + " land\r\n", "unknown", ""},
	} {
		got := parseMemo(c.record)
		if got.decision != c.decision || got.failedAt != c.failedAt {
			t.Errorf("parseMemo(%q) = %q, %q; want %q, %q",
				c.record, got.decision, got.failedAt, c.decision, c.failedAt)
		}
	}
}

// The commands status suggests are pasted back into a shell, and before
// this program they were printed by bash's printf %q. Every printable
// ASCII character is held to what bash itself prints, alone, inside a
// word and opening one, and so are the control characters a shell names.
func TestShellQuoteMatchesBash(t *testing.T) {
	words := []string{
		"topic", "topic/one", `odd/"q"&<x>`, "#hash", "a#b", "~home", "a:~b", "a=~b", "a~b",
		"a b", "tab\there", "nl\nx", "\x7f", "\x1b[0m", "\a\b\f\v\r",
	}
	for c := byte(' '); c < 0x7f; c++ {
		words = append(words, string(c), "a"+string(c)+"b", string(c)+"z")
	}
	compareWithBash(t, "C", words)

	// Beyond ASCII, bash's answer depends on the locale. Compare against a
	// UTF-8 one where this machine has it, and skip where it does not.
	if bashQuote(t, "C.UTF-8", []string{"é"})[0] != "é" {
		t.Skip("no UTF-8 locale for bash here; the non-ASCII cases are not compared")
	}
	compareWithBash(t, "C.UTF-8", []string{
		"café", "топик/один", "日本 語", "bad\xffbyte", "\xc3", "a\u0085b", "mixed'é\"",
	})
}

func compareWithBash(t *testing.T, locale string, words []string) {
	t.Helper()
	want := bashQuote(t, locale, words)
	for i, w := range words {
		if got := shellQuote(w); got != want[i] {
			t.Errorf("shellQuote(%q) = %s; bash says %s", w, got, want[i])
		}
	}
}

func bashQuote(t *testing.T, locale string, words []string) []string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to compare with")
	}
	cmd := exec.Command(bash, append([]string{"-c", `for w; do printf '%q\0' "$w"; done`, "bash"}, words...)...)
	cmd.Env = append(os.Environ(), "LC_ALL="+locale)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(got) != len(words) {
		t.Fatalf("bash quoted %d words of %d", len(got), len(words))
	}
	return got
}

// A change name JSON cannot carry exactly is refused before origin is
// read, so the JSON form never prints an object naming some other change.
func TestJSONRefusesANameThatIsNotUTF8(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"status", "--json", "topic/\xff"}, &stdout, &stderr); code == 0 {
		t.Fatal("a change name that is not UTF-8 was accepted")
	}
	if stdout.Len() != 0 {
		t.Errorf("a refusal printed %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "not UTF-8") {
		t.Errorf("the refusal did not say why: %q", stderr.String())
	}
}
