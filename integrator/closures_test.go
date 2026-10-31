package main

import (
	"the-valley/integrator/verdict"

	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// inputsLine is the line attest inputs prints for a check, with digests
// made from its name so every check's are its own.
func inputsLine(name string) string {
	return fmt.Sprintf("%s valley-inputs-v1:%s sha256:%s\n", name, digestOf(name, "i"), digestOf(name, "d"))
}

func digestOf(name, kind string) string {
	return sha256hex([]byte(kind + name))
}

func TestParseInputsTakesExactlyTheChecksAskedFor(t *testing.T) {
	asked := []string{"a", "b"}
	a, b := inputsLine("a"), inputsLine("b")
	got, err := parseInputs(b+a, asked)
	if err != nil {
		t.Fatalf("a well-formed answer in another order was refused: %v", err)
	}
	if got["a"].Inputs != digestOf("a", "i") || got["a"].Derivation != digestOf("a", "d") || got["b"].Inputs != digestOf("b", "i") || len(got) != 2 {
		t.Errorf("closures were not matched to their checks by name: %v", got)
	}

	hex := digestOf("a", "i")
	for why, out := range map[string]string{
		"nothing":                  "",
		"a check missing":          a,
		"a check not asked for":    a + b + inputsLine("c"),
		"a check twice":            a + b + a,
		"two fields":               a + "b valley-inputs-v1:" + hex + "\n",
		"four fields":              a + strings.TrimSuffix(b, "\n") + " extra\n",
		"a doubled space":          a + strings.Replace(b, " ", "  ", 1),
		"a blank line":             a + "\n" + b,
		"another inputs scheme":    a + "b valley-inputs-v2:" + hex + " sha256:" + hex + "\n",
		"another derivation hash":  a + "b valley-inputs-v1:" + hex + " sha512:" + hex + "\n",
		"an uppercase digest":      a + "b valley-inputs-v1:" + strings.ToUpper(hex) + " sha256:" + hex + "\n",
		"a short digest":           a + "b valley-inputs-v1:" + hex[1:] + " sha256:" + hex + "\n",
		"a digest that is not hex": a + "b valley-inputs-v1:" + hex + " sha256:" + hex[1:] + "g\n",
	} {
		if got, err := parseInputs(out, asked); err == nil {
			t.Errorf("an answer with %s was taken: %v", why, got)
		}
	}
}

// fakeAttest is an attest that answers `attest inputs` from files in its
// directory and logs the checks of every call, one call per line. Asked
// for several checks, it prints batch.out and batch.err and exits with
// batch.status. Asked for one, it fails with <name>.err when that exists,
// and otherwise prints the check's line.
func fakeAttest(t *testing.T) (*integrator, string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
dir="$(dirname "$0")"
names=""
while [ $# -gt 0 ]; do
  case "$1" in --check) names="$names ${2%%=*}"; shift 2 ;; *) shift ;; esac
done
echo $names >> "$dir/calls"
set -- $names
if [ $# -gt 1 ]; then
  cat "$dir/batch.out" 2> /dev/null
  cat "$dir/batch.err" >&2 2> /dev/null
  exit "$(cat "$dir/batch.status")"
fi
if [ -f "$dir/$1.err" ]; then cat "$dir/$1.err" >&2; exit 1; fi
cat "$dir/$1.line"
`
	if err := os.WriteFile(filepath.Join(dir, "attest"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		write(t, dir, name+".line", inputsLine(name))
	}
	return &integrator{attest: filepath.Join(dir, "attest")}, dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeCalls is the checks of each call the fake attest was given.
func fakeCalls(t *testing.T, dir string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "calls"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// partitioned fails the test unless every check asked for is in exactly
// one of the two maps, and nothing else is in either.
func partitioned(t *testing.T, names []string, got map[string]verdict.Closure, bad map[string]string) {
	t.Helper()
	var all []string
	for name := range got {
		all = append(all, name)
		if _, both := bad[name]; both {
			t.Errorf("%s has both a closure and an error", name)
		}
	}
	for name, why := range bad {
		all = append(all, name)
		if why == "" {
			t.Errorf("%s has an empty error", name)
		}
	}
	sort.Strings(all)
	if strings.Join(all, " ") != strings.Join(names, " ") {
		t.Errorf("the checks accounted for are %v, not %v", all, names)
	}
}

func pure(names ...string) []verdict.Required {
	var required []verdict.Required
	for _, name := range names {
		required = append(required, verdict.Required{Name: name, Runner: "nix", Attribute: name})
	}
	return required
}

func TestClosuresAsksOnceForEveryPureCheck(t *testing.T) {
	in, dir := fakeAttest(t)
	wt := &worktree{dir: t.TempDir()}

	got, bad := in.closures(wt, "HEAD", []verdict.Required{{Name: "reachable", Runner: "command"}})
	if calls := fakeCalls(t, dir); len(calls) != 0 || len(got)+len(bad) != 0 {
		t.Errorf("no pure check, and attest was run %d times: %v %v", len(calls), got, bad)
	}

	write(t, dir, "batch.out", inputsLine("c")+inputsLine("a")+inputsLine("b"))
	write(t, dir, "batch.err", "warning: nix said something on standard error\n")
	write(t, dir, "batch.status", "0")
	got, bad = in.closures(wt, "HEAD", append(pure("c", "a", "b"), verdict.Required{Name: "reachable", Runner: "command"}))
	if calls := fakeCalls(t, dir); len(calls) != 1 || calls[0] != "a b c" {
		t.Errorf("three pure checks were asked for in %q, not once in sorted order", calls)
	}
	partitioned(t, []string{"a", "b", "c"}, got, bad)
	if len(bad) != 0 || got["b"].Inputs != digestOf("b", "i") {
		t.Errorf("a good answer was not taken whole: %v %v", got, bad)
	}
}

func TestClosuresAsksAgainOneAtATime(t *testing.T) {
	for _, c := range []struct {
		why, out, status string
	}{
		{"failed", inputsLine("a"), "1"},
		{"left a check out", inputsLine("a") + inputsLine("c"), "0"},
		{"printed a malformed line", inputsLine("a") + "b valley-inputs-v1:x sha256:y\n" + inputsLine("c"), "0"},
	} {
		t.Run(c.why, func(t *testing.T) {
			in, dir := fakeAttest(t)
			write(t, dir, "batch.out", c.out)
			write(t, dir, "batch.err", "attest: evaluating b: the batch went wrong\n")
			write(t, dir, "batch.status", c.status)
			write(t, dir, "b.err", "attest: evaluating b: exit status 1: error: b does not evaluate\nmore detail\n")
			got, bad := in.closures(&worktree{dir: t.TempDir()}, "HEAD", pure("a", "b", "c"))
			if calls := fakeCalls(t, dir); strings.Join(calls, ",") != "a b c,a,b,c" {
				t.Errorf("attest was asked for %q, not the batch and then each check", calls)
			}
			partitioned(t, []string{"a", "b", "c"}, got, bad)
			if bad["b"] != "attest: evaluating b: exit status 1: error: b does not evaluate" {
				t.Errorf("b's error is %q, not its own first line", bad["b"])
			}
			if got["a"].Inputs != digestOf("a", "i") || got["c"].Derivation != digestOf("c", "d") {
				t.Errorf("the checks that evaluate did not keep their closures: %v", got)
			}
		})
	}
}

func TestClosuresAsksOneCheckOnce(t *testing.T) {
	in, dir := fakeAttest(t)
	write(t, dir, "a.err", "attest: evaluating a: no such attribute\n")
	got, bad := in.closures(&worktree{dir: t.TempDir()}, "HEAD", pure("a"))
	if calls := fakeCalls(t, dir); len(calls) != 1 {
		t.Errorf("one failing check was asked for %d times", len(calls))
	}
	partitioned(t, []string{"a"}, got, bad)
	if bad["a"] != "attest: evaluating a: no such attribute" {
		t.Errorf("a's error is %q", bad["a"])
	}
}

func TestClosuresReportsAnAttestThatDoesNotStart(t *testing.T) {
	in := &integrator{attest: filepath.Join(t.TempDir(), "absent")}
	got, bad := in.closures(&worktree{dir: t.TempDir()}, "HEAD", pure("a", "b"))
	partitioned(t, []string{"a", "b"}, got, bad)
	if !strings.HasPrefix(bad["a"], "attest inputs: ") {
		t.Errorf("an attest that did not start is reported as %q", bad["a"])
	}
}
