package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// countNix puts a nix and a nix-store ahead of the real ones on the
// fixture's PATH. Each writes the command it was given to the log it
// returns, one call per line, and then runs the real program with the same
// arguments, so every answer a test sees is nix's own.
func countNix(t *testing.T, f *nixFixture) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	for _, tool := range []string{"nix", "nix-store"} {
		real, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"%s $*\" >> '%s'\nexec '%s' \"$@\"\n", tool, log, real)
		if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.env = append(f.env, "PATH="+dir+":"+os.Getenv("PATH"))
	return log
}

// calls reads the log countNix keeps and empties it, so each run is
// counted on its own.
func calls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// count is how many logged calls begin with prefix.
func count(lines []string, prefix string) int {
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

// noBuilds fails the test if any logged call could have built something
// on purpose: a nix build, or a nix-store asked to realise a path.
func noBuilds(t *testing.T, lines []string) {
	t.Helper()
	for _, l := range lines {
		if strings.HasPrefix(l, "nix build") || (strings.HasPrefix(l, "nix-store") && strings.Contains(l, "--realise")) {
			t.Errorf("attest inputs built something: %s", l)
		}
	}
}

// inputs runs `attest inputs` over the fixture's HEAD with one --check
// flag per check given, and returns what it printed and its exit status.
func (f nixFixture) inputs(t *testing.T, checks ...string) (stdout, stderr string, status int) {
	t.Helper()
	args := []string{"inputs", "--repo", f.repo}
	for _, c := range checks {
		args = append(args, "--check", c)
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = f.env
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		status = exit.ExitCode()
	}
	return out.String(), errb.String(), status
}

// Several checks asked for at once are evaluated together, in one nix
// evaluation, and nothing is built. Each line printed is the line that
// asking for that check alone prints, in the order asked: an alias carries
// its own name and its attribute's digests, and two names for one
// attribute each get a line.
func TestInputsEvaluatesEveryCheckTogether(t *testing.T) {
	f := newNixFixture(t, map[string]string{
		"a": `check "a" "echo a > $out"`,
		"b": `check "b" "echo b > $out"`,
		"c": `check "c" "echo c > $out"`,
	})
	log := countNix(t, &f)
	asked := []string{"c", "a", "alias=b", "b", "again=a"}

	var alone strings.Builder
	for _, check := range asked {
		stdout, stderr, status := f.inputs(t, check)
		if status != 0 {
			t.Fatalf("attest inputs --check %s exited %d:\n%s", check, status, stderr)
		}
		alone.WriteString(stdout)
		lines := calls(t, log)
		if n := count(lines, "nix eval"); n != 1 {
			t.Errorf("one check alone took %d nix evaluations:\n%s", n, strings.Join(lines, "\n"))
		}
		noBuilds(t, lines)
	}

	stdout, stderr, status := f.inputs(t, asked...)
	if status != 0 {
		t.Fatalf("attest inputs over %d checks exited %d:\n%s", len(asked), status, stderr)
	}
	if stdout != alone.String() {
		t.Errorf("the checks together printed\n%s\nand one at a time\n%s", stdout, alone.String())
	}
	lines := calls(t, log)
	if n := count(lines, "nix eval"); n != 1 {
		t.Errorf("%d checks took %d nix evaluations, not one:\n%s", len(asked), n, strings.Join(lines, "\n"))
	}
	noBuilds(t, lines)

	byName := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		name, digests, _ := strings.Cut(line, " ")
		byName[name] = digests
	}
	if byName["alias"] != byName["b"] || byName["again"] != byName["a"] || byName["a"] == byName["b"] {
		t.Errorf("a name for an attribute does not carry that attribute's digests:\n%s", stdout)
	}
}

// A check that does not evaluate ends the command with its error, as it
// always has: what is printed on standard output is every check asked for
// before it, and nothing after, and the error is the one evaluating that
// check alone reports. Whether the shared evaluation survives the error is
// what it costs: nix can catch a missing attribute and a throw, so only
// the broken check is evaluated again, but not a type error, which ends
// the shared evaluation and has every check evaluated alone.
func TestInputsStopsAtTheFirstError(t *testing.T) {
	for _, c := range []struct {
		name   string
		broken string // "" leaves the check undefined
		says   string
		evals  int
	}{
		{"an absent attribute", "", "does not provide attribute", 2},
		{"a throw", `throw "the broken check does not evaluate"`, "the broken check does not evaluate", 2},
		{"a type error", `check "broken" (1 + "the broken check does not evaluate")`, "cannot add a string to an integer", 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			checks := map[string]string{
				"fine": `check "fine" "echo fine > $out"`,
				"also": `check "also" "echo also > $out"`,
			}
			if c.broken != "" {
				checks["broken"] = c.broken
			}
			f := newNixFixture(t, checks)
			log := countNix(t, &f)
			fine, _, status := f.inputs(t, "fine")
			if status != 0 {
				t.Fatalf("the fine check alone exited %d", status)
			}
			_, aloneErr, status := f.inputs(t, "broken")
			if status != 1 || !strings.HasPrefix(aloneErr, "attest: evaluating ") || !strings.Contains(aloneErr, c.says) {
				t.Fatalf("the broken check alone exited %d with:\n%s", status, aloneErr)
			}
			calls(t, log)

			stdout, stderr, status := f.inputs(t, "fine", "broken", "also")
			if status != 1 {
				t.Errorf("attest inputs exited %d, not 1", status)
			}
			if stdout != fine {
				t.Errorf("standard output is\n%q\nnot the line before the error\n%q", stdout, fine)
			}
			if !strings.HasPrefix(stderr, "attest: evaluating ") || !strings.Contains(stderr, ".broken: exit status 1: ") || !strings.Contains(stderr, c.says) {
				t.Errorf("standard error does not report the broken check's evaluation:\n%s", stderr)
			}
			lines := calls(t, log)
			if n := count(lines, "nix eval"); n != c.evals {
				t.Errorf("%d nix evaluations, not %d:\n%s", n, c.evals, strings.Join(lines, "\n"))
			}
			noBuilds(t, lines)
		})
	}
}
