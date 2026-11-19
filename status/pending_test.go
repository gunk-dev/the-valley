package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"the-valley/note"
	"the-valley/telemetry"
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

// pending starts as many git processes for a project with five landed
// changes, five withdrawn ones and five branches no change is on as for
// one with one of each, beside a draft: what the landed heads reach, what
// the branches reach, and what the heads a draft may include reach, is
// read in one batch each, however many there are.
func TestPendingStartsGitAsOftenWhateverItLists(t *testing.T) {
	dir := t.TempDir()
	starts := toolsOnPath(t, dir, "git", "cue")
	project := func(n int) (string, int) {
		t.Helper()
		sh, head := policyProject(t, filepath.Join(dir, fmt.Sprint(n)), 1)
		for i := 0; i < n; i++ {
			branch := fmt.Sprintf("landed-%d", i)
			head(branch, strings.Repeat(string(rune('p'+i)), 32))
			sh("checkout", "--quiet", "main")
			sh("merge", "--quiet", "--ff-only", branch)
			sh("push", "--quiet", "origin", "main")
		}
		for i := 0; i < n; i++ {
			sh("checkout", "--quiet", "-b", fmt.Sprintf("stray-%d", i), "main")
			sh("commit", "--quiet", "--allow-empty", "-m", fmt.Sprintf("stray %d", i))
			sh("push", "--quiet", "origin", "HEAD")
		}
		valley := func(args ...string) {
			t.Helper()
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr, os.Exit); code != 0 {
				t.Fatalf("%s: %s", strings.Join(args, " "), stderr.String())
			}
		}
		for i := 0; i < n; i++ {
			id := strings.Repeat(string(rune('u'+i)), 32)
			head(fmt.Sprintf("withdrawn-%d", i), id)
			valley("withdraw", id)
		}
		draft := strings.Repeat("z", 32)
		head("draft", draft)
		valley("unready", draft)
		out, started := runCounted(t, starts, "pending")
		return out, started["git"]
	}
	few, small := project(1)
	many, large := project(5)
	if !strings.Contains(few, "1 branch no change is on") || !strings.Contains(many, "5 branches no change is on") {
		t.Fatalf("pending does not count the stray branches:\n%s\n%s", few, many)
	}
	if small == 0 || large != small {
		t.Errorf("pending started git %d times for one landed change, withdrawn change and stray branch, and %d times for five of each", small, large)
	}
}

// A host that refuses the read of the worker's telemetry and sources, as
// it does when the principal already has every bounded request it may
// have open, leaves main's line without the worker's runs and prints no
// host entry: pending asks each operation once, says why once on stderr
// (no extra warning for sources), and prints main's line from the walk
// alone.
func TestPendingAsksARefusingHostOnce(t *testing.T) {
	dir := t.TempDir()
	starts := toolsOnPath(t, dir, "git", "cue")
	refusal := fmt.Sprintf(`{"kind":"error","error":%q}`, telemetry.SlotRefusal("operator", telemetry.BoundedSlots, "bounded"))
	ssh := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> '%s'\necho '%s'\n", starts, refusal)
	if err := os.WriteFile(filepath.Join(dir, "bin", "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	followProject(t, filepath.Join(dir, "origin"))

	os.Remove(starts)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pending"}, &stdout, &stderr, os.Exit); code != 0 {
		t.Fatalf("pending: %s", stderr.String())
	}
	log, _ := os.ReadFile(starts)
	if n := strings.Count(string(log), "valley-telemetry read"); n != 1 {
		t.Errorf("pending asked the refusing host for worker reads %d times:\n%s", n, log)
	}
	if n := strings.Count(string(log), "valley-telemetry sources"); n != 1 {
		t.Errorf("pending asked the refusing host for sources %d times:\n%s", n, log)
	}
	if n := strings.Count(stderr.String(), "refused"); n != 1 || strings.Count(stderr.String(), "\n") != 1 {
		t.Errorf("pending said, of the refusal:\n%s", stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "main  ◐ clearing · f-1 due · covers ") || strings.Contains(stdout.String(), "host  ") {
		t.Errorf("pending's main line, with the host refusing:\n%s", stdout.String())
	}
}

// followProject is policyProject with a class that check f-1 follows, a
// landing on main that sets it due, and its telemetry read from an ssh
// host, so valley pending waits on the worker's telemetry for main's line.
func followProject(t *testing.T, root string) {
	t.Helper()
	sh, head := policyProject(t, root, 1)
	sh("config", "valley.telemetry.remote", "ssh://git@valley-host/origin.git")
	floor := "package verification\nfloor: {\n" +
		"\tchecks: \"c-1\": {runner: \"nix\", attribute: \"c-1\"}\n" +
		"\tchecks: \"f-1\": {runner: \"nix\", attribute: \"f-1\"}\n" +
		"\tclasses: src: {paths: \"src/**\": true, requires: \"c-1\": true, follows: \"f-1\": true}\n" +
		"\tunclassified: \"c-1\": true\n}\n"
	if err := os.WriteFile(filepath.Join("policy", "instance", "floor.cue"), []byte(floor), 0o644); err != nil {
		t.Fatal(err)
	}
	sh("commit", "--quiet", "-a", "-m", "follow f-1")
	sh("push", "--quiet", "origin", "main")
	head("landed", strings.Repeat("p", 32))
	sh("checkout", "--quiet", "main")
	sh("merge", "--quiet", "--ff-only", "landed")
	sh("push", "--quiet", "origin", "main")
}

// A host that accepts the read of the worker's telemetry and then stalls
// does not hold valley pending, nor outlive it: with no follow check and
// nothing ready, nothing waits on the read, so pending cancels it as it
// returns, and no ssh it started is left running.
func TestPendingEndsAReadNobodyWaitsFor(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc to find a child left running in")
	}
	dir := t.TempDir()
	toolsOnPath(t, dir, "git", "cue")
	// ssh is the host: it answers sources with nothing unhealthy, and on
	// the worker read says the answer's first line and stalls as a sleep
	// of a length nothing else in this process sleeps.
	header := `{"kind":"header","host":"valley-host","format":"valley-telemetry/v1"}`
	stall := fmt.Sprintf("#!/bin/sh\necho '%s'\ncase \"$*\" in *\"valley-telemetry sources\"*) echo '{\"kind\":\"end\"}'; exit 0 ;; esac\nexec sleep 86399\n", header)
	if err := os.WriteFile(filepath.Join(dir, "bin", "ssh"), []byte(stall), 0o755); err != nil {
		t.Fatal(err)
	}
	sh, _ := policyProject(t, filepath.Join(dir, "origin"), 1)
	sh("config", "valley.telemetry.remote", "ssh://git@valley-host/origin.git")

	began := time.Now()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"pending"}, &stdout, &stderr, os.Exit); code != 0 {
		t.Fatalf("pending: %s", stderr.String())
	}
	// The read's own time limit is 30 s: a pending that waited for it
	// would take that long.
	if took := time.Since(began); took > 20*time.Second {
		t.Errorf("pending took %s beside a stalled host", took)
	}
	if !strings.Contains(stdout.String(), "nothing needs you") || stderr.Len() != 0 {
		t.Errorf("pending beside a stalled host printed:\n%s\nand said:\n%s", stdout.String(), stderr.String())
	}
	stats, _ := filepath.Glob("/proc/[0-9]*/stat")
	for _, stat := range stats {
		data, err := os.ReadFile(stat)
		if err != nil {
			continue
		}
		f := strings.Fields(string(data[bytes.LastIndexByte(data, ')')+1:]))
		cmdline, _ := os.ReadFile(filepath.Join(filepath.Dir(stat), "cmdline"))
		if len(f) > 1 && f[1] == fmt.Sprint(os.Getpid()) && strings.Contains(string(cmdline), "86399") {
			t.Errorf("pending left its ssh running: %s", filepath.Dir(stat))
		}
	}
}

// pending --checks reads a draft's checks only when --drafts lists the
// drafts. With no evidence on origin it starts no attest: a head's tree
// digest is worked out in-process. Without --drafts, two drafts add
// nothing to what --checks starts and change no row; with it, each
// draft's checks are read and its block shows them.
func TestPendingReadsDraftChecksOnlyWhenListed(t *testing.T) {
	dir := t.TempDir()
	starts := toolsOnPath(t, dir, "git", "cue")
	// attest logs its start as the shims do. Origin holds no evidence, and
	// a head's tree digest is worked out in-process, so nothing starts it.
	stand := fmt.Sprintf("#!/bin/sh\necho attest >> '%s'\n", starts)
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
	listing() // so every count is of a listing whose caches are warm
	before, attest, added := listing()
	if attest != 0 {
		t.Fatalf("pending --checks started attest %d times for one ready head and no evidence", attest)
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
	out, listed, addedListed := listing("--drafts")
	if listed != 0 || addedListed <= added {
		t.Errorf("pending --drafts --checks started attest %d times, and --checks added %d processes, not more than %d, for one ready head and two drafts",
			listed, addedListed, added)
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

// pending --checks says when the worker's newest build of a pending
// check failed, for a ready change and for a draft alike, apart from a
// check whose failed build a later one passed and a check never built, and
// says where the worker's report is read. With --json, the check's state
// stays pending and the worker's report is beside it. A batch that failed
// before any check ran is said the same way. A host whose telemetry does
// not read leaves the listing as it is with no worker records.
func TestPendingSaysWhenTheWorkersBuildFailed(t *testing.T) {
	dir := t.TempDir()
	toolsOnPath(t, dir, "git", "cue")
	// ssh answers the worker's read from a file, as the host would.
	answer := filepath.Join(dir, "answer")
	if err := os.WriteFile(filepath.Join(dir, "bin", "ssh"), []byte(fmt.Sprintf("#!/bin/sh\ncat '%s'\n", answer)), 0o755); err != nil {
		t.Fatal(err)
	}
	sh, head := policyProject(t, filepath.Join(dir, "origin"), 3)
	sh("config", "valley.telemetry.remote", "ssh://git@valley-host/origin.git")
	ready, draft := strings.Repeat("p", 32), strings.Repeat("q", 32)
	head("ready-1", ready)
	head("draft-1", draft)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unready", draft}, &stdout, &stderr, os.Exit); code != 0 {
		t.Fatalf("unready: %s", stderr.String())
	}
	// The worker's records about a tree are about the ready change's and
	// the draft's alike: each is written once for each head's tree.
	var trees []string
	for _, branch := range []string{"ready-1", "draft-1"} {
		tree, err := rememberedDigest(git{env: gitEnvironment(os.Environ())}, branch)
		if err != nil {
			t.Fatal(err)
		}
		trees = append(trees, tree)
	}

	// records makes the host's answer: one page of the worker's records,
	// each about each head's tree where it names the tree "0123".
	records := func(given ...string) {
		t.Helper()
		var lines []string
		for _, l := range given {
			for _, tree := range trees {
				lines = append(lines, strings.ReplaceAll(l, `"tree":"0123"`, `"tree":"`+tree+`"`))
			}
		}
		page := `{"kind":"header","host":"valley-host","format":"valley-telemetry/v1"}` + "\n"
		for i, l := range lines {
			page += fmt.Sprintf(`{"kind":"record","stream":"worker","seq":%d,%s}`+"\n", i+1, l)
		}
		page += fmt.Sprintf(`{"kind":"end","cursor":"worker=0123456789abcdef:%d","truncated":false}`+"\n", len(lines))
		if err := os.WriteFile(answer, []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	at := func(ago time.Duration) string { return telemetry.Stamp(time.Now().Add(-ago)) }
	// build is a check.result of the draft's version, as attest records
	// one.
	build := func(when, run, tree, check, result string) string {
		return fmt.Sprintf(`"type":"check.result","time":%q,"run":%q,"project":"origin","change":%q,"version":1,"tree":%q,"check":%q,`+
			`"data":{"result":%q,"build_ms":1000,"eval_ms":4000,"log_bytes":10,"cached":false}`, when, run, draft, tree, check, result)
	}
	ages := regexp.MustCompile(`(?m) +[0-9]+[smhd]$| +[0-9]+ [a-z]+ ago · operator$`)
	pending := func(args ...string) (string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := run(append([]string{"pending", "--checks", "--drafts"}, args...), &stdout, &stderr, os.Exit); code != 0 {
			t.Fatalf("pending: %s", stderr.String())
		}
		return ages.ReplaceAllString(stdout.String(), ""), stderr.String()
	}
	checksOf := func() map[string]*jsonVersionChecks {
		t.Helper()
		out, _ := pending("--json")
		var doc jsonChanges
		if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Repos) != 1 || doc.Repos[0].Drafts == nil {
			t.Fatalf("pending --json printed no listing of one project (%v):\n%s", err, out)
		}
		checks := map[string]*jsonVersionChecks{}
		for _, c := range append(doc.Repos[0].Changes, *doc.Repos[0].Drafts...) {
			checks[c.ChangeID] = c.Versions[c.Active-1].Checks
		}
		if len(checks) != 2 {
			t.Fatalf("pending --json lists %d changes, not the ready change and the draft:\n%s", len(checks), out)
		}
		return checks
	}
	see := "  see: valley telemetry read --source worker --logs"

	records()
	plain, _ := pending()

	// c-1's newest build failed; c-2 failed, then passed; c-3 was built
	// only for another tree.
	failed := at(5*time.Minute + 10*time.Second)
	records(build(at(20*time.Minute), "r1", "0123", "c-1", "failed"), build(at(20*time.Minute), "r1", "0123", "c-2", "failed"),
		build(failed, "r2", "0123", "c-1", "failed"), build(at(5*time.Minute), "r2", "0123", "c-2", "passed"),
		build(at(time.Minute), "r3", "4567", "c-3", "failed"))
	out, _ := pending()
	says := "c-1 failed in the worker's build, 5m ago"
	for _, want := range []string{
		"\n  1  pppp v1  ✗ " + says + "\n     ready-1\n     requires 3 quick checks, 0 slow\n" +
			"     checks: 3 pending (" + says + ")\n     " + see + "\n",
		"\nqqqq v1 on draft-1  ✗ " + says + "\n",
		"\n  requires 3 quick checks, 0 slow\n  checks: 3 pending (" + says + ") in 2s (eval 4s)\n  " + see + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pending --checks --drafts does not say\n%s\nin:\n%s", want, out)
		}
	}
	for id, c := range checksOf() {
		if c == nil || c.State != checkPending || c.Worker != nil || len(c.Required) != 3 {
			t.Fatalf("%s's checks are not three, pending, with no batch reported: %+v", id, c)
		}
		if q := c.Required[0]; q.State != checkPending || q.Worker == nil || *q.Worker != (jsonWorkerReport{Result: checkFailed, Time: failed, Run: "r2"}) {
			t.Errorf("%s's c-1 is not pending with the worker's report of r2's failure: %+v", id, q)
		}
		for _, q := range c.Required[1:] {
			if q.State != checkPending || q.Worker != nil {
				t.Errorf("%s's %s is not pending with no worker's report: %+v", id, q.Name, q)
			}
		}
	}

	// A batch that failed before any check ran recorded no check's result.
	ended := at(time.Minute + 10*time.Second)
	records(build(at(20*time.Minute), "r1", "0123", "c-1", "passed"),
		fmt.Sprintf(`"type":"batch.end","time":%q,"run":"p1","project":"origin","change":%q,"version":1,"tree":"0123",`+
			`"data":{"result":"failed","attest_run":"r4","reason":"attest run r4: exit status 1"}`, ended, ready))
	out, _ = pending()
	says = "the worker's batch failed, 1m ago"
	if want := "\n  1  pppp v1  ✗ " + says + "\n     ready-1\n     requires 3 quick checks, 0 slow\n" +
		"     checks: 3 pending (" + says + ")\n     " + see + "\n"; !strings.Contains(out, want) {
		t.Errorf("pending --checks does not say\n%s\nin:\n%s", want, out)
	}
	for id, c := range checksOf() {
		if c.Worker == nil || *c.Worker != (jsonWorkerReport{Result: checkFailed, Time: ended, Run: "r4"}) || c.State != checkPending {
			t.Errorf("%s's checks do not carry the worker's report of the failed batch: %+v", id, c)
		}
	}

	// A host that refuses: the listing is as it is with no worker records.
	if err := os.WriteFile(answer, []byte(`{"kind":"error","error":"operator holds no observe grant"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errs := pending()
	if out != plain {
		t.Errorf("with the worker's telemetry refused, pending printed:\n%s\nand with no worker records:\n%s", out, plain)
	}
	if n := strings.Count(errs, "no check lists its build times"); n != 1 {
		t.Errorf("a refusing host was said %d times on stderr, not once:\n%s", n, errs)
	}
	for id, c := range checksOf() {
		reported := c.Worker != nil
		for _, q := range c.Required {
			reported = reported || q.Worker != nil
		}
		if reported {
			t.Errorf("with the worker's telemetry refused, %s carries a worker's report: %+v", id, c)
		}
	}
}

// unheard opens a FIFO to write and closes it, so a reader still waiting
// on it reads nothing and ends.
func unheard(fifo string) {
	if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
		f.Close()
	}
}

// bareGit is git run in the bare repository origin, with stdin, giving
// what it printed.
func bareGit(t *testing.T, origin string) func(stdin string, args ...string) string {
	return func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"--git-dir", origin}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}
}

// publishSigners publishes the host's signers in origin, a bare
// repository whose main carries its own instance: a key for each name,
// and the integrator's name, integrator.
func publishSigners(t *testing.T, origin string, names ...string) {
	t.Helper()
	in := bareGit(t, origin)
	var keys strings.Builder
	for _, name := range names {
		pub, _, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatal(err)
		}
		keys.WriteString(note.NewVerifierKey(name, pub).String() + "\n")
	}
	blob := func(text string) string { return in(text, "hash-object", "-w", "--stdin") }
	in("", "update-ref", signersRef, in(fmt.Sprintf("100644 blob %s\tintegrator-name\n100644 blob %s\tknown-signers\n",
		blob("integrator\n"), blob(keys.String())), "mktree"))
}

// Each reader of evidence reads the host's signers as its use says. An
// assessment, as approve, review's approve step and checks --build make
// one, reads the publication afresh each time, so a worker key removed or
// published again between two assessments in one command is honoured by
// the second. A listing, which displays and authorises nothing, reads it
// once in the command, with transfer verification and main's walk.
func TestEvidenceReadsTheSignersAsItsUseSays(t *testing.T) {
	dir := t.TempDir()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git to run")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	starts, bin := filepath.Join(dir, "starts"), filepath.Join(dir, "bin")
	origin := filepath.Join(dir, "origin", "origin.git")
	policyProject(t, filepath.Join(dir, "origin"), 1)
	publish := func(names ...string) { publishSigners(t, origin, names...) }
	publish("integrator", "worker")
	// The worker's note about c-1, stored for the digest the attest
	// stand-in gives every tree (fakeAttest), which verifies a note's
	// signer when the known-signers file holds its key (fakeVerify).
	in := bareGit(t, origin)
	stored := in("predicateType "+predicatePure+"\npredicate.check.name c-1\npredicate.check.runner nix\n"+
		"predicate.check.attribute c-1\npredicate.result passed\nsigner worker+00000001\n\n— worker signature\n",
		"hash-object", "-w", "--stdin")
	check := in(fmt.Sprintf("100644 blob %s\tstatement.note\n", stored), "mktree")
	in("", "update-ref", attestationRefs+strings.Repeat("ab", 32)+"/00000001",
		in(fmt.Sprintf("040000 tree %s\tc-1\n", check), "mktree"))
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	shim := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> '%s'\nexec '%s' \"$@\"\n", starts, real)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(bin, "attest")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VALLEY_TEST_ATTEST_VERIFY", "batch")
	reads := func() int {
		log, _ := os.ReadFile(starts)
		n := 0
		for _, line := range strings.Split(string(log), "\n") {
			if f := strings.Fields(line); slices.Contains(f, "ls-remote") && slices.Contains(f, signersRef) {
				n++
			}
		}
		return n
	}

	g := git{env: gitEnvironment(os.Environ()), stderr: io.Discard}
	before := verification
	verification = &signerSource{g: g, stderr: io.Discard}
	defer func() { verification = before }()
	tip, err := g.output(true, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	checks := []nixCheck{{"c-1", "c-1"}}
	covered := func(e evidence, err error) bool {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return e.covered["c-1"] != ""
	}

	approving := &approval{}
	first := covered(approving.coverage(g, tip, checks, io.Discard))
	publish("integrator")
	revoked := covered(approving.coverage(g, tip, checks, io.Discard))
	publish("integrator", "worker")
	restored := covered(approving.coverage(g, tip, checks, io.Discard))
	if !first || revoked || !restored || reads() != 3 {
		t.Errorf("approve's assessments covered c-1: %v, with the worker's key removed %v, restored %v, in %d reads of the signers, not 3",
			first, revoked, restored, reads())
	}

	building := newAsking(g, io.Discard, io.Discard)
	building.head = tip
	first = covered(building.cover(checks))
	publish("integrator")
	revoked = covered(building.cover(checks))
	if !first || revoked || reads() != 5 {
		t.Errorf("checks --build's assessments covered c-1: %v, with the worker's key removed %v, in %d reads of the signers, not 5",
			first, revoked, reads())
	}

	publish("integrator", "worker")
	_, name, _ := verification.resolve() // transfer verification
	walk := hostKeys{use: listEvidence}  // main's walk
	defer walk.done()
	_, walked, why := walk.read(g)
	publish("integrator")
	keys, listedP, unsigned := hostSignersFrom(g, listEvidence.signers()) // a listing's evidence
	if unsigned == "" {
		defer os.Remove(keys)
	}
	if why != "" || unsigned != "" || walked == nil || walked != listedP || name != "integrator" || reads() != 6 {
		t.Errorf("a listing read the signers %d times, not once more: transfers %q, the walk %p (%s), the evidence %p (%s)",
			reads()-5, name, walked, why, listedP, unsigned)
	}
}
