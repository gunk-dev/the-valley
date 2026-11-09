package main

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestVerdictLines(t *testing.T) {
	cases := []struct {
		name  string
		v     verdict
		want  string
		glyph string // the glyph as a terminal shows it
	}{
		{"every check covered",
			verdict{outcome: "approved", name: "nqyw", version: "3", required: 4, passed: 4},
			"✓ nqyw v3  approved · checks passed · landing on main\n", "\x1b[32m✓"},
		{"checks built here",
			verdict{outcome: "approved", name: "nqyw", version: "3", required: 4, passed: 4, took: "42s"},
			"✓ nqyw v3  approved · 4 checks passed in 42s · landing on main\n", "\x1b[32m✓"},
		{"no check required",
			verdict{outcome: "approved", name: "topic/quiet", required: 0, passed: 0},
			"✓ topic/quiet  approved · no checks required · landing on main\n", "\x1b[32m✓"},
		{"a worker builds the rest",
			verdict{outcome: "approved", name: "nqyw", version: "3", required: 4, passed: 1, worker: true, waiting: []string{"a", "b", "c"}},
			"◐ nqyw v3  approved · lands when 3 checks pass (1/4 passed)\n", "\x1b[33m◐"},
		{"counts not read",
			verdict{outcome: "approved", name: "nqyw", version: "3", required: -1, passed: -1, worker: true, waiting: []string{"a", "b", "c"}},
			"◐ nqyw v3  approved · waiting for 3 checks\n", "\x1b[33m◐"},
		{"nothing builds the rest",
			verdict{outcome: "approved", name: "nqyw", version: "3", required: 2, passed: 0, waiting: []string{"a", "b"}, run: "valley approve --build-here nqyw"},
			"◇ nqyw v3  approved · waiting for 2 checks (no worker building)\n  run: valley approve --build-here nqyw\n", "\x1b[36m◇"},
		{"state unknown",
			verdict{outcome: "unknown", name: "nqyw", version: "3", required: -1, passed: -1},
			"? nqyw v3  approved · state unknown · see: valley status nqyw\n", "?"},
		{"a check failed",
			verdict{outcome: "refused", name: "nqyw", version: "3", required: 4, passed: -1, failed: []string{"valley-request"}},
			"⊘ nqyw v3  not approved · 1 of 4 checks failed\n  ✗ valley-request — see: valley status nqyw\n", "\x1b[31m⊘"},
		{"a draft",
			verdict{outcome: "refused", name: "nqyw", version: "3", required: -1, passed: -1, why: "change is a draft", run: "valley ready nqyw"},
			"⊘ nqyw v3  not approved · change is a draft\n  run: valley ready nqyw\n", "\x1b[31m⊘"},
		{"a stale stack",
			verdict{outcome: "refused", name: "nqyw", version: "3", required: -1, passed: -1, why: "stack is behind main (includes kpxm v2)", run: "valley rebase nqyw"},
			"⊘ nqyw v3  not approved · stack is behind main (includes kpxm v2)\n  run: valley rebase nqyw\n", "\x1b[31m⊘"},
	}
	for _, c := range cases {
		c.v.target = "main"
		if got := c.v.lines(terminal{width: 80}); got != c.want {
			t.Errorf("%s, plain:\n got %q\nwant %q", c.name, got, c.want)
		}
		shown := c.v.lines(terminal{width: 80, bold: true, live: true})
		if plain := sgr.ReplaceAllString(shown, ""); plain != c.want {
			t.Errorf("%s, on a terminal, without its styling:\n got %q\nwant %q", c.name, plain, c.want)
		}
		if !strings.HasPrefix(shown, c.glyph) || !strings.Contains(shown, "\x1b[1m"+c.v.label()+"\x1b[0m") {
			t.Errorf("%s, on a terminal: want the glyph as %q and the name in bold, got %q", c.name, c.glyph, shown)
		}
	}
}

func TestVerdictPrompt(t *testing.T) {
	header := func() []string { return []string{"nqyw v3", "checks: 4 passed", "behind main (integrator merges)"} }
	keys := []string{"[a]pprove", "[v]iew diff", "[r]eject", "[s]kip"}
	want := "nqyw v3 · checks: 4 passed · behind main (integrator merges)\n[a]pprove  [v]iew diff  [r]eject  [s]kip: "
	if got := promptLines(terminal{width: 80}, header(), keys); got != want {
		t.Errorf("plain:\n got %q\nwant %q", got, want)
	}
	shown := promptLines(terminal{width: 80, bold: true}, header(), keys)
	if sgr.ReplaceAllString(shown, "") != want || !strings.Contains(shown, "[\x1b[1ma\x1b[0m]pprove") {
		t.Errorf("on a terminal: got %q", shown)
	}
}

// checking keeps a passing run's output back, and shows the failed check's
// log and the refusal when one fails.
func TestVerdictChecking(t *testing.T) {
	v := verdict{name: "nqyw", version: "3", required: 3, passed: 1, target: "main"}
	var out, errs bytes.Buffer
	pass := exec.Command("sh", "-c", `echo "check   a nix passed"; echo "check   b nix passed"; echo "stored refs/x -> y"`)
	if status := v.checking([]string{"a", "b"}, pass, false, &out, &errs, nil, nil); status != 0 {
		t.Fatalf("a passing run exited %d", status)
	}
	if out.String() != "◐ nqyw v3  checking 2 checks: a, b\n" || errs.Len() != 0 {
		t.Errorf("a passing run printed %q and %q", out.String(), errs.String())
	}

	out.Reset()
	errs.Reset()
	fail := exec.Command("sh", "-c", `echo "check   a nix passed"; echo "check   b nix failed"; echo; echo "--- b ---"; echo "b: build log"; echo boom; exit 1`)
	if status := v.checking([]string{"a", "b"}, fail, false, &out, &errs, nil, nil); status != 1 {
		t.Fatalf("a failing run exited %d", status)
	}
	got := errs.String()
	for _, want := range []string{"--- b ---\nb: build log\nboom\n", "⊘ nqyw v3  not approved · 1 of 3 checks failed\n  ✗ b — see: valley status nqyw\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("a failing run's stderr lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "check   a") {
		t.Errorf("a failing run showed the output of a check that passed:\n%s", got)
	}
}
