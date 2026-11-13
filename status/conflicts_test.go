package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// conflictProject is a repository at root with its own origin, made the
// current directory, whose main holds f and g, each "0". It returns git
// run in it, git's output, and commit, which commits files on a new
// branch off from and pushes it, returning the head. A git shim on PATH
// logs the arguments of each git start to starts (runMerges).
func conflictProject(t *testing.T, root string) (git func(...string) string, commit func(branch, from string, files map[string]string) string, starts string) {
	t.Helper()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VALLEY_KNOWN_SIGNERS", "")
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git to run")
	}
	bin, starts := filepath.Join(root, "bin"), filepath.Join(root, "starts")
	os.MkdirAll(bin, 0o755)
	shim := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> '%s'\nexec '%s' \"$@\"\n", starts, real)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	origin, work := filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.Command(real, args...)
		cmd.Dir = work
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}
	if out, err := exec.Command(real, "init", "--quiet", "--bare", "--initial-branch=main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command(real, "clone", "--quiet", origin, work).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	git("config", "user.name", "operator")
	git("config", "user.email", "operator@example.org")
	git("config", "valley.principal", "operator")
	git("checkout", "--quiet", "-b", "main")
	git("commit", "--quiet", "--allow-empty", "-m", "first")
	commit = func(branch, from string, files map[string]string) string {
		t.Helper()
		git("checkout", "--quiet", "-B", branch, from)
		for name, text := range files {
			if err := os.WriteFile(filepath.Join(work, name), []byte(text+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git("add", ".")
		git("commit", "--quiet", "-m", branch)
		git("push", "--quiet", "--force", "origin", branch)
		return git("rev-parse", "HEAD")
	}
	commit("main", "main", map[string]string{"f": "0", "g": "0"})
	t.Chdir(work)
	return git, commit, starts
}

// runMerges runs valley with args, and returns what it printed and how
// many git merge-tree processes the shim logged to starts.
func runMerges(t *testing.T, starts string, args ...string) (string, int) {
	t.Helper()
	os.Remove(starts)
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, os.Exit); code != 0 {
		t.Fatalf("%s: %s", strings.Join(args, " "), stderr.String())
	}
	log, _ := os.ReadFile(starts)
	merges := 0
	for _, line := range strings.Split(string(log), "\n") {
		if slices.Contains(strings.Fields(line), "merge-tree") {
			merges++
		}
	}
	return stdout.String(), merges
}

// readyAs readies branch as the change id.
func readyAs(t *testing.T, id, branch string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"ready", "--change", id, branch}, &stdout, &stderr, os.Exit); code != 0 {
		t.Fatalf("ready: %s", stderr.String())
	}
}

// underRow is the lines indented under the row that names name.
func underRow(out, name string) []string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "  ") || !strings.Contains(line, " "+name+" v") {
			continue
		}
		var under []string
		for _, l := range lines[i+1:] {
			if !strings.HasPrefix(l, "    ") {
				break
			}
			under = append(under, strings.TrimSpace(l))
		}
		return under
	}
	return nil
}

// pendingJSON is valley-pending/v1 or valley-changes/v1, with args, as
// the changes it lists by handle.
func pendingJSON(t *testing.T, args ...string) map[string]jsonChange {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(append(args, "--json"), &stdout, &stderr, os.Exit); code != 0 {
		t.Fatalf("%s: %s", strings.Join(args, " "), stderr.String())
	}
	var doc jsonChanges
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil || len(doc.Repos) != 1 {
		t.Fatalf("%s --json does not read: %v", strings.Join(args, " "), err)
	}
	byHandle := map[string]jsonChange{}
	for _, c := range doc.Repos[0].Changes {
		byHandle[c.Handle] = c
	}
	return byHandle
}

// conflictsOf is a change's conflicts and the pairs not computed, as text
// a test compares: "<handle> <paths> <total>" and "<handle>: <why>".
func conflictsOf(c jsonChange) string {
	if c.Conflicts == nil || c.ConflictsUnknown == nil {
		return "absent"
	}
	var says []string
	for _, x := range *c.Conflicts {
		says = append(says, fmt.Sprintf("%s %s %d", x.Handle, strings.Join(x.Paths, ","), x.PathsTotal))
	}
	for _, x := range *c.ConflictsUnknown {
		says = append(says, x.Handle+": "+x.Why)
	}
	return strings.Join(says, "; ")
}

// pending marks each ready change's conflicts with the others, by the
// merge onto main's tip: two that change one file conflict, and the
// third shows nothing. A warm listing runs no merge; a cold one runs one
// for each pair, up to the bound, and says the rest were not compared.
// An approved change under moving is paired with a waiting one, layers
// of one stack are not paired, and a change not on main's tip awaits a
// restack, in text and JSON, never reading as clean.
func TestPendingMarksReadyChangesThatConflict(t *testing.T) {
	root := t.TempDir()
	git, commit, starts := conflictProject(t, root)
	first := git("rev-parse", "main~1")
	commit("one", "main", map[string]string{"f": "1"})
	commit("two", "main", map[string]string{"f": "2"})
	commit("three", "main", map[string]string{"h": "3"})
	k, m, n := strings.Repeat("k", 32), strings.Repeat("m", 32), strings.Repeat("n", 32)
	readyAs(t, k, "one")
	readyAs(t, m, "two")
	readyAs(t, n, "three")

	bound := pairBound
	pairBound = 2
	out, merges := runMerges(t, starts, "pending")
	pairBound = bound
	if merges != 2 || !strings.Contains(out, "\n1 pair not compared: past the bound of 2 merges\n") {
		t.Fatalf("with a bound of 2 merges, pending ran %d and printed:\n%s", merges, out)
	}

	os.RemoveAll(filepath.Join(root, "cache"))
	cold, merges := runMerges(t, starts, "pending")
	if merges != 3 {
		t.Errorf("a cold pending ran %d merges for three pairs", merges)
	}
	for name, want := range map[string]string{"kkkk": "conflicts with mmmm", "mmmm": "conflicts with kkkk", "nnnn": ""} {
		under := underRow(cold, name)
		if len(under) == 0 || strings.Join(under[1:], "\n") != want {
			t.Errorf("under %s pending prints %q, not its title and %q:\n%s", name, under, want, cold)
		}
	}
	if strings.Contains(cold, "not compared") {
		t.Errorf("pending says a pair was not compared:\n%s", cold)
	}
	warm, merges := runMerges(t, starts, "pending")
	ages := regexp.MustCompile(`(?m) +[0-9]+[smhd]$`)
	if merges != 0 || ages.ReplaceAllString(warm, "") != ages.ReplaceAllString(cold, "") {
		t.Errorf("a warm pending ran %d merges and printed:\n%s\nwhere the cold one printed:\n%s", merges, warm, cold)
	}
	verbose, _ := runMerges(t, starts, "pending", "-v")
	if under := strings.Join(underRow(verbose, "kkkk"), "\n") + "\n"; !strings.Contains(under, "\nconflicts with mmmm: f\n") {
		t.Errorf("pending -v does not name the conflicted path under kkkk:\n%s", verbose)
	}
	for _, verb := range []string{"pending", "changes"} {
		got := pendingJSON(t, verb)
		for name, want := range map[string]string{"kkkk": "mmmm f 1", "mmmm": "kkkk f 1", "nnnn": ""} {
			if says := conflictsOf(got[name]); says != want {
				t.Errorf("%s --json gives %s the conflicts %q, not %q", verb, name, says, want)
			}
		}
	}

	// mmmm is approved, pppp is a layer on kkkk, and rrrr is on main's
	// parent, not its tip.
	git("push", "--quiet", "origin", "two:refs/the-valley/integration-requests/main/c-"+m)
	commit("four", "one", map[string]string{"l": "4"})
	commit("five", first, map[string]string{"r": "5"})
	p, r := strings.Repeat("p", 32), strings.Repeat("r", 32)
	readyAs(t, p, "four")
	readyAs(t, r, "five")
	out, _ = runMerges(t, starts, "pending")
	if under := underRow(out, "mmmm"); !strings.Contains(out, "◐ mmmm v1  approved") || strings.Join(under, "\n") != "conflicts with kkkk, pppp" {
		t.Errorf("the approved mmmm does not say it conflicts with kkkk and pppp:\n%s", out)
	}
	if under := underRow(out, "pppp"); len(under) != 2 || under[1] != "conflicts with mmmm" {
		t.Errorf("the row of the stack of kkkk and pppp does not say it conflicts with mmmm:\n%s", out)
	}
	if !strings.Contains(out, "\n4 pairs not compared: awaiting restack\n") {
		t.Errorf("pending does not say rrrr's 4 pairs await a restack:\n%s", out)
	}
	got := pendingJSON(t, "pending")
	for name, want := range map[string]string{
		"kkkk": "mmmm f 1; rrrr: awaiting restack",
		"pppp": "mmmm f 1; rrrr: awaiting restack",
		"mmmm": "kkkk f 1; pppp f 1; rrrr: awaiting restack",
		"nnnn": "rrrr: awaiting restack",
		"rrrr": "kkkk: awaiting restack; mmmm: awaiting restack; nnnn: awaiting restack; pppp: awaiting restack",
	} {
		if says := conflictsOf(got[name]); says != want {
			t.Errorf("pending --json gives %s the conflicts %q, not %q", name, says, want)
		}
	}
}

// Two heads that share an unlanded ancestor are compared on main's tip,
// as the integrator merges the second once the first lands, and not on
// their own merge base. The ancestor changes f from 0 to 1, one head
// restores 0 and the other makes it 2: from the ancestor that is a
// conflict, and landing either head first, the integrator merges the
// other cleanly, so pending marks no conflict.
func TestPendingComparesForkedHeadsAsTheIntegratorMerges(t *testing.T) {
	git, commit, starts := conflictProject(t, t.TempDir())
	tip := git("rev-parse", "main")
	commit("ancestor", "main", map[string]string{"f": "1"})
	a := commit("restores", "ancestor", map[string]string{"f": "0"})
	b := commit("changes", "ancestor", map[string]string{"f": "2"})
	if out, err := exec.Command("git", "merge-tree", "--write-tree", a, b).Output(); err == nil {
		t.Fatalf("the heads merge cleanly from their own merge base:\n%s", out)
	}
	// integrate lands head on main as the integrator does
	// (integrator/refs.go): one commit on the tip whose tree is the head's
	// when the head's merge base with the tip is the tip, and otherwise
	// the merge of tip and head from that merge base (mergeDelta).
	integrate := func(tip, head string) (landed string, clean bool) {
		t.Helper()
		tree := head + "^{tree}"
		if base := git("merge-base", tip, head); base != tip {
			out, err := exec.Command("git", "merge-tree", "--write-tree", "--merge-base="+base, tip, head).Output()
			if err != nil {
				return "", false
			}
			tree, _, _ = strings.Cut(string(out), "\n")
		}
		return git("commit-tree", tree, "-p", tip, "-m", "landed"), true
	}
	for _, order := range [][2]string{{a, b}, {b, a}} {
		landed, _ := integrate(tip, order[0])
		next, clean := integrate(landed, order[1])
		if !clean || git("show", next+":f") != "2" {
			t.Fatalf("the integrator does not land the second head cleanly with f at 2 after the first")
		}
	}
	readyAs(t, strings.Repeat("k", 32), "restores")
	readyAs(t, strings.Repeat("m", 32), "changes")
	out, merges := runMerges(t, starts, "pending")
	if merges != 1 || strings.Contains(out, "conflicts with") || strings.Contains(out, "not compared") {
		t.Errorf("pending ran %d merges and marks the forked heads as the integrator does not:\n%s", merges, out)
	}
	for name, c := range pendingJSON(t, "pending") {
		if says := conflictsOf(c); says != "" {
			t.Errorf("pending --json gives %s the conflicts %q", name, says)
		}
	}
}
