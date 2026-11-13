package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pending fits its text to 80 columns when stdout is no terminal: a long
// title is cut short with …, and a branch or a handle is printed whole,
// even where the line it is on cannot fit. A stack of branches four deep
// is listed at one indentation, so its deepest entry has the room its
// second has. Nothing it prints is an escape code.
func TestPendingFitsEightyColumnsAndKeepsNamesWhole(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VALLEY_KNOWN_SIGNERS", "")
	origin, work := filepath.Join(dir, "origin.git"), filepath.Join(dir, "work")
	sh := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if out, err := exec.Command("git", "init", "--quiet", "--bare", "--initial-branch=main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "clone", "--quiet", origin, work).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	sh("config", "user.name", "a committer whose name is so long that the line it is on has to cut it short")
	sh("config", "user.email", "committer@localhost")
	sh("config", "valley.principal", "alice")
	sh("checkout", "--quiet", "-b", "main")
	sh("commit", "--quiet", "--allow-empty", "-m", "base")
	sh("push", "--quiet", "origin", "main")

	long := "a-branch-whose-name-runs-on-well-past-the-eightieth-column-of-any-terminal-it-is-shown-in"
	title := "a subject that says far more than one line has room for, " + strings.Repeat("and then some more ", 4)
	sh("checkout", "--quiet", "-b", long, "main")
	sh("commit", "--quiet", "--allow-empty", "-m", title+"on the long branch")
	sh("push", "--quiet", "origin", long)
	sh("checkout", "--quiet", "-b", "short", "main")
	sh("commit", "--quiet", "--allow-empty", "-m", title)
	sh("push", "--quiet", "origin", "short")
	from := "main"
	for _, layer := range []string{"1", "2", "3", "4"} {
		sh("checkout", "--quiet", "-b", "layer-"+layer, from)
		sh("commit", "--quiet", "--allow-empty", "-m", "layer "+layer+": "+title)
		sh("push", "--quiet", "origin", "layer-"+layer)
		from = "layer-" + layer
	}

	t.Chdir(work)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"ready", "--change", strings.Repeat("q", 32), "short"}, &stdout, &stderr, os.Exit); code != 0 {
		t.Fatalf("ready: %s", stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"pending", "--branches"}, &stdout, &stderr, os.Exit); code != 0 {
		t.Fatalf("pending: %s", stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "\x1b") {
		t.Errorf("pending printed an escape code to no terminal:\n%s", out)
	}
	if !strings.Contains(out, "\n"+long+"\n") {
		t.Errorf("the long branch is not whole, on a line of its own:\n%s", out)
	}
	if !strings.Contains(out, "\n  1  qqqq v1") {
		t.Errorf("the change is not named by its whole handle and version:\n%s", out)
	}
	cut := 0
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.Contains(line, long) {
			continue
		}
		if columns(line) > 80 {
			t.Errorf("a line is %d columns wide: %q", columns(line), line)
		}
		if strings.HasPrefix(strings.TrimLeft(line, " "), "a subject that says") {
			if columns(line) != 80 || !strings.HasSuffix(line, "…") {
				t.Errorf("a long title is not cut to 80 columns with …: %q", line)
			}
			cut++
		}
		if strings.HasPrefix(line, "layer-4 ") && !strings.HasSuffix(line, "…") {
			t.Errorf("the author is not cut short to keep the line within 80 columns: %q", line)
		}
	}
	lines := strings.Split(out, "\n")
	for layer, lead := range map[string]string{"4": "  ", "3": "  │   ", "2": "  │   ", "1": "      "} {
		listed := false
		for _, line := range lines {
			if strings.HasPrefix(line, lead+"layer "+layer+": ") {
				listed = true
				if columns(line) != 80 || !strings.HasSuffix(line, "…") {
					t.Errorf("layer %s's title is not cut to 80 columns with …: %q", layer, line)
				}
			}
		}
		if !listed {
			t.Errorf("layer %s's title is not at %q:\n%s", layer, lead, out)
		}
	}
	if !strings.Contains(out, "\n  approving it lands the 3 below too\n") || strings.Contains(out, "on layer-") {
		t.Errorf("the stack four deep is not one list, each layer on the next:\n%s", out)
	}
	if cut != 2 {
		t.Errorf("%d titles were cut short, where the change's and the branch's should be:\n%s", cut, out)
	}
}

// fit cuts by columns, not bytes: a wide character takes two, and a
// combining mark none.
func TestFitCountsColumns(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly ten", 11, "exactly ten"},
		{"one word too many", 12, "one word to…"},
		{"漢字漢字漢字", 7, "漢字漢…"},
		{"café au lait", 5, "café au lait"[:6] + "…"},
	} {
		if got := fit(c.s, c.n); got != c.want || columns(got) > c.n {
			t.Errorf("fit(%q, %d) = %q, %d columns; want %q", c.s, c.n, got, columns(got), c.want)
		}
	}
}

// toolsOnPath puts on PATH, for the test, a shim for each tool named that
// logs each start to the file it returns and runs the real tool. It
// skips the test when a tool is missing, unless VALLEY_TEST_TOOLS is
// required.
func toolsOnPath(t *testing.T, dir string, tools ...string) (starts string) {
	t.Helper()
	bin, starts := filepath.Join(dir, "bin"), filepath.Join(dir, "starts")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		real, err := exec.LookPath(tool)
		if err != nil {
			if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
				t.Fatalf("no %s on PATH, and VALLEY_TEST_TOOLS=required", tool)
			}
			t.Skipf("no %s to run; the nix check provides one", tool)
		}
		shim := fmt.Sprintf("#!/bin/sh\necho %s >> '%s'\nexec '%s' \"$@\"\n", tool, starts, real)
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(shim), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return starts
}

// testSchema is the verification schema, read from the package's
// directory before a test changes to another.
var testSchema, _ = filepath.Abs(filepath.Join("..", "schema", "verification.cue"))

// policyProject is a repository at root with its own origin, whose floor
// has one class requiring checks c-1 to c-<checks> of every path under
// src/, made the current directory. head pushes a branch off main that
// adds src/<branch> and readies it as the change id.
func policyProject(t *testing.T, root string, checks int) (sh func(...string), head func(branch, id string)) {
	t.Helper()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VALLEY_KNOWN_SIGNERS", "")
	t.Setenv("VALLEY_VERIFICATION_SCHEMA", testSchema)
	origin, work := filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	sh = func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if out, err := exec.Command("git", "init", "--quiet", "--bare", "--initial-branch=main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "clone", "--quiet", origin, work).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	sh("config", "user.name", "operator")
	sh("config", "user.email", "operator@example.org")
	sh("config", "valley.principal", "operator")
	sh("checkout", "--quiet", "-b", "main")
	var floor strings.Builder
	floor.WriteString("package verification\nfloor: {\n")
	for i := 1; i <= checks; i++ {
		fmt.Fprintf(&floor, "\tchecks: \"c-%d\": {runner: \"nix\", attribute: \"c-%d\"}\n", i, i)
		fmt.Fprintf(&floor, "\tclasses: src: {paths: \"src/**\": true, requires: \"c-%d\": true}\n", i)
	}
	floor.WriteString("\tunclassified: \"c-1\": true\n}\n")
	if err := os.MkdirAll(filepath.Join(work, "policy", "instance"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "policy", "instance", "floor.cue"), []byte(floor.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	sh("add", ".")
	sh("commit", "--quiet", "-m", "base")
	sh("push", "--quiet", "origin", "main")
	t.Chdir(work)
	head = func(branch, id string) {
		t.Helper()
		sh("checkout", "--quiet", "-b", branch, "main")
		if err := os.MkdirAll(filepath.Join(work, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, "src", branch), []byte(branch+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		sh("add", ".")
		sh("commit", "--quiet", "-m", branch)
		sh("push", "--quiet", "origin", branch)
		var stdout, stderr bytes.Buffer
		if code := run([]string{"ready", "--change", id, branch}, &stdout, &stderr, os.Exit); code != 0 {
			t.Fatalf("ready: %s", stderr.String())
		}
	}
	return sh, head
}

// runCounted runs valley with args, and returns what it printed and how
// many processes the shims on PATH logged, by tool, and in all under "".
func runCounted(t *testing.T, starts string, args ...string) (string, map[string]int) {
	t.Helper()
	os.Remove(starts)
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, os.Exit); code != 0 {
		t.Fatalf("%s: %s", strings.Join(args, " "), stderr.String())
	}
	log, _ := os.ReadFile(starts)
	started := map[string]int{}
	for _, tool := range strings.Fields(string(log)) {
		started[tool]++
		started[""]++
	}
	return stdout.String(), started
}

// pending reads the policy at main's tip once for a project, and asks cue
// no requirement's pin: the cue processes it starts, with --checks or
// without, are as many for a class requiring nine checks and four ready
// heads as for one requiring three and one head.
func TestPendingStartsCueOncePerProject(t *testing.T) {
	dir := t.TempDir()
	starts := toolsOnPath(t, dir, "cue")

	// project lists a project whose class requires checks c-1 to
	// c-<checks>, with heads ready changes touching it. It returns what
	// each listing printed and how many cue processes it started.
	project := func(checks, heads int) (printed map[string]string, started map[string]int) {
		t.Helper()
		_, head := policyProject(t, filepath.Join(dir, fmt.Sprintf("%d-%d", checks, heads)), checks)
		for i := 1; i <= heads; i++ {
			head(fmt.Sprintf("head-%d", i), strings.Repeat(string(rune('p'+i)), 32))
		}
		printed, started = map[string]string{}, map[string]int{}
		for _, args := range [][]string{{"pending"}, {"pending", "--checks"}} {
			listing := strings.Join(args, " ")
			out, n := runCounted(t, starts, args...)
			printed[listing], started[listing] = out, n["cue"]
		}
		return printed, started
	}

	_, small := project(3, 1)
	out, large := project(9, 4)
	if want := "requires 9 quick checks, 0 slow"; strings.Count(out["pending --checks"], want) != 4 {
		t.Fatalf("pending --checks does not say %q of each of four heads:\n%s", want, out["pending --checks"])
	}
	for listing, n := range small {
		if n == 0 || large[listing] != n {
			t.Errorf("%s started %d cue processes for three checks and one head, and %d for nine and four heads", listing, n, large[listing])
		}
	}
}

// pending --checks reads a draft's checks only when --drafts lists the
// drafts. A listing starts attest only to read checks, once for each head
// to digest its tree. Without --drafts, two drafts start no attest, add
// nothing to what --checks starts, and change no row; with it, each
// draft's tree is digested and its block shows its checks.
func TestPendingReadsDraftChecksOnlyWhenListed(t *testing.T) {
	dir := t.TempDir()
	starts := toolsOnPath(t, dir, "git", "cue")
	// attest logs its start as the shims do and answers with one digest:
	// when origin holds no evidence, a listing asks it only to digest each
	// head's tree, and the digest decides nothing.
	stand := fmt.Sprintf("#!/bin/sh\necho attest >> '%s'\necho valley-tree-v1:0123\n", starts)
	if err := os.WriteFile(filepath.Join(dir, "bin", "attest"), []byte(stand), 0o755); err != nil {
		t.Fatal(err)
	}
	_, head := policyProject(t, filepath.Join(dir, "project"), 2)
	head("ready-1", strings.Repeat("p", 32))
	// listing is pending with args and --checks: what it printed, its ages
	// left out, since a row's age ticks, the attest processes it started,
	// and how many processes --checks added.
	ages := regexp.MustCompile(`(?m) +[0-9]+[smhd]$`)
	listing := func(args ...string) (out string, attest, added int) {
		t.Helper()
		_, without := runCounted(t, starts, append([]string{"pending"}, args...)...)
		out, with := runCounted(t, starts, append([]string{"pending", "--checks"}, args...)...)
		return ages.ReplaceAllString(out, ""), with["attest"], with[""] - without[""]
	}
	before, attest, added := listing()
	if attest != 1 {
		t.Fatalf("pending --checks started attest %d times for one ready head", attest)
	}
	for _, id := range []string{"q", "r"} {
		head("draft-"+id, strings.Repeat(id, 32))
		var stdout, stderr bytes.Buffer
		if code := run([]string{"unready", strings.Repeat(id, 32)}, &stdout, &stderr, os.Exit); code != 0 {
			t.Fatalf("unready: %s", stderr.String())
		}
	}
	after, drafted, addedNow := listing()
	if drafted != attest || addedNow != added {
		t.Errorf("with two drafts not listed, pending --checks started attest %d times, not %d, and --checks added %d processes, not %d",
			drafted, attest, addedNow, added)
	}
	if want := before + "\n2 drafts · valley pending --drafts\n"; after != want {
		t.Errorf("pending --checks printed, with two drafts:\n%s\nand with none:\n%s", after, before)
	}
	out, listed, _ := listing("--drafts")
	if listed != attest+2 {
		t.Errorf("pending --drafts --checks started attest %d times for one ready head and two drafts", listed)
	}
	for _, d := range []string{"qqqq v1 on draft-q  2 checks pending", "rrrr v1 on draft-r  2 checks pending"} {
		if !strings.Contains(out, "\n"+d) {
			t.Errorf("pending --drafts --checks does not open a block with %q:\n%s", d, out)
		}
	}
	if n := strings.Count(out, "\n  checks: 2 pending\n"); n != 2 {
		t.Errorf("pending --drafts --checks gives %d drafts their checks line, not 2:\n%s", n, out)
	}
}
