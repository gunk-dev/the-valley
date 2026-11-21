package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/change"
)

// A withdrawal given the head it was read at writes only while the
// change's active version is still at that head, as review's [r]eject
// withdraws the version whose branch it deleted: a version readied since
// is left as it is.
func TestSetStateWithdrawsOnlyAtTheHeadRead(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	origin, work := filepath.Join(dir, "origin.git"), filepath.Join(dir, "work")
	sh := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out))
	}
	valley := func(args ...string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr, os.Exit); code != 0 {
			t.Fatalf("valley %s: %s", strings.Join(args, " "), stderr.String())
		}
	}
	if out, err := exec.Command("git", "init", "--quiet", "--bare", "--initial-branch=main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "clone", "--quiet", origin, work).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	sh("config", "user.name", "valley-test")
	sh("config", "user.email", "valley-test@localhost")
	sh("config", "valley.principal", "alice")
	sh("checkout", "--quiet", "-b", "main")
	sh("commit", "--quiet", "--allow-empty", "-m", "base")
	sh("push", "--quiet", "origin", "main")
	t.Chdir(work)

	id := strings.Repeat("k", 32)
	sh("checkout", "--quiet", "-b", "topic")
	sh("commit", "--quiet", "--allow-empty", "-m", "one")
	first := sh("rev-parse", "HEAD")
	sh("push", "--quiet", "origin", "topic")
	valley("ready", "--change", id, "topic")
	sh("commit", "--quiet", "--allow-empty", "-m", "two")
	second := sh("rev-parse", "HEAD")
	sh("push", "--quiet", "origin", "topic")
	valley("ready", "--change", id, "topic")

	state := func() string {
		t.Helper()
		blob, err := exec.Command("git", "-C", origin, "cat-file", "blob", change.Ref(recordTarget, id)+":record.json").Output()
		if err != nil {
			t.Fatalf("origin holds no record of %s: %v", id, err)
		}
		var r change.Record
		if err := json.Unmarshal(blob, &r); err != nil {
			t.Fatalf("the record of %s: %v", id, err)
		}
		return r.State
	}
	g := git{env: gitEnvironment(os.Environ()), stderr: io.Discard}
	err := setState(g, []string{id}, io.Discard, io.Discard, "withdraw", change.Withdrawn, first)
	if err == nil || !strings.Contains(err.Error(), "is at v2 now") {
		t.Errorf("a withdrawal at v1's head, with v2 active, was not refused: %v", err)
	}
	if s := state(); s != change.Ready {
		t.Errorf("a refused withdrawal left the change %s", s)
	}
	if err := setState(g, []string{id}, io.Discard, io.Discard, "withdraw", change.Withdrawn, second); err != nil {
		t.Fatalf("a withdrawal at the active head: %v", err)
	}
	if s := state(); s != change.Withdrawn {
		t.Errorf("a withdrawal at the active head left the change %s", s)
	}
}

// readyFixture sets up a bare origin and a working clone configured for
// alice, with an initial commit on main containing the files named.
type readyFixture struct {
	t      *testing.T
	origin string
	work   string
}

func newReadyFixture(t *testing.T, files map[string]string) *readyFixture {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	f := &readyFixture{
		t:      t,
		origin: filepath.Join(dir, "origin.git"),
		work:   filepath.Join(dir, "work"),
	}
	if out, err := exec.Command("git", "init", "--quiet", "--bare", "--initial-branch=main", f.origin).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "clone", "--quiet", f.origin, f.work).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	f.git("config", "user.name", "valley-test")
	f.git("config", "user.email", "valley-test@localhost")
	f.git("config", "valley.principal", "alice")
	f.git("checkout", "--quiet", "-b", "main")
	for name, body := range files {
		f.write(name, body)
	}
	if len(files) == 0 {
		f.git("commit", "--quiet", "--allow-empty", "-m", "base")
	} else {
		f.git("add", "-A")
		f.git("commit", "--quiet", "-m", "base")
	}
	f.git("push", "--quiet", "origin", "main")
	t.Chdir(f.work)
	return f
}

func (f *readyFixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = f.work
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *readyFixture) write(name, body string) {
	f.t.Helper()
	path := filepath.Join(f.work, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		f.t.Fatalf("write %s: %v", path, err)
	}
}

func (f *readyFixture) valley(args ...string) (int, string, string) {
	f.t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, os.Exit)
	return code, stdout.String(), stderr.String()
}

func (f *readyFixture) mustValley(args ...string) (string, string) {
	f.t.Helper()
	code, stdout, stderr := f.valley(args...)
	if code != 0 {
		f.t.Fatalf("valley %s exited %d:\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, stdout, stderr)
	}
	return stdout, stderr
}

func (f *readyFixture) records() map[string]*change.Record {
	f.t.Helper()
	out, err := exec.Command("git", "-C", f.origin, "for-each-ref", "--format=%(refname)", change.RefPrefix+recordTarget+"/").Output()
	if err != nil {
		f.t.Fatalf("for-each-ref: %v", err)
	}
	recs := map[string]*change.Record{}
	for _, ref := range strings.Fields(string(out)) {
		_, id, ok := change.ParseRef(ref)
		if !ok {
			continue
		}
		blob, err := exec.Command("git", "-C", f.origin, "cat-file", "blob", ref+":record.json").Output()
		if err != nil {
			f.t.Fatalf("cat-file %s: %v", ref, err)
		}
		var r change.Record
		if err := json.Unmarshal(blob, &r); err != nil {
			f.t.Fatalf("unmarshal %s: %v", ref, err)
		}
		recs[id] = &r
	}
	return recs
}

// A commit made with plain git (no header, no trailer), readied on a branch,
// then rebased onto a moved main and readied again produces one change with
// two versions.
func TestReadyPlainGitRebaseKeepsChange(t *testing.T) {
	f := newReadyFixture(t, map[string]string{
		"a.txt": "line1\n",
		"b.txt": "line1\n",
	})
	f.git("checkout", "--quiet", "-b", "topic")
	f.write("a.txt", "line1\nfrom topic\n")
	f.git("commit", "--quiet", "-am", "topic work")
	first := f.git("rev-parse", "HEAD")
	f.git("push", "--quiet", "origin", "topic")

	out1, err1 := f.mustValley("ready")
	if err1 != "" {
		t.Errorf("unexpected warning on current base: %q", err1)
	}
	recs := f.records()
	if len(recs) != 1 {
		t.Fatalf("expected 1 record after first ready, got %d", len(recs))
	}
	var id string
	for k := range recs {
		id = k
	}
	handle := id[:4]
	if !strings.Contains(out1, "v1 ready") || !strings.Contains(out1, "The change ID "+id+" is new.") {
		t.Errorf("unexpected first ready output: %q", out1)
	}

	// Advance main on origin, then rebase topic with plain git and ready again.
	f.git("checkout", "--quiet", "main")
	f.write("b.txt", "line1\nfrom main\n")
	f.git("commit", "--quiet", "-am", "main advances")
	f.git("push", "--quiet", "origin", "main")

	f.git("checkout", "--quiet", "topic")
	f.git("rebase", "--quiet", "origin/main")
	second := f.git("rev-parse", "HEAD")
	f.git("push", "--quiet", "--force", "origin", "topic")

	out2, err2 := f.mustValley("ready")
	if err2 != "" {
		t.Errorf("unexpected warning after rebase onto current main: %q", err2)
	}
	recs = f.records()
	if len(recs) != 1 {
		t.Fatalf("expected 1 change after plain-git rebase and ready, got %d changes", len(recs))
	}
	r := recs[id]
	if r == nil || r.Active != 2 || len(r.Versions) != 2 {
		t.Fatalf("expected change %s to have 2 versions with active=2, got %+v", id, r)
	}
	if r.Versions[0].Head != change.HeadPrefix+first || r.Versions[1].Head != change.HeadPrefix+second {
		t.Errorf("unexpected version heads: %+v", r.Versions)
	}
	if !strings.Contains(out2, handle+" v2 ready") {
		t.Errorf("expected %s v2 ready in output, got %q", handle, out2)
	}
	if !strings.Contains(out2, "Using change "+handle+": branch topic already has it.") {
		t.Errorf("expected branch-match line naming %s, got %q", handle, out2)
	}
	if strings.Contains(out2, "is new") {
		t.Errorf("second ready claimed change ID is new: %q", out2)
	}
}

// Branch-matched change ID rules: drafts stay live, explicit IDs and trailers
// win, two live changes on one branch are refused, landed and withdrawn
// changes do not capture the branch, and --stamp and --if-active still guard.
func TestReadyBranchMatchRules(t *testing.T) {
	t.Run("draft stays live and revives or advances", func(t *testing.T) {
		f := newReadyFixture(t, map[string]string{"f.txt": "0\n"})
		f.git("checkout", "--quiet", "-b", "draft-branch")
		f.write("f.txt", "1\n")
		f.git("commit", "--quiet", "-am", "v1")
		f.git("push", "--quiet", "origin", "draft-branch")
		f.mustValley("ready")
		var id string
		for k := range f.records() {
			id = k
		}
		handle := id[:4]
		f.mustValley("unready", handle)

		// Readying the same head revives the draft at v1.
		out, _ := f.mustValley("ready")
		r := f.records()[id]
		if len(f.records()) != 1 || r.State != change.Ready || r.Active != 1 || len(r.Versions) != 1 {
			t.Fatalf("expected revived draft v1, got recs=%d r=%+v", len(f.records()), r)
		}
		if !strings.Contains(out, "Using change "+handle+": branch draft-branch already has it.") {
			t.Errorf("expected branch-match line, got %q", out)
		}

		// Unready again, push a new head, ready without --change -> v2.
		f.mustValley("unready", handle)
		f.write("f.txt", "2\n")
		f.git("commit", "--quiet", "-am", "v2")
		f.git("push", "--quiet", "origin", "draft-branch")
		f.mustValley("ready")
		r = f.records()[id]
		if len(f.records()) != 1 || r.State != change.Ready || r.Active != 2 || len(r.Versions) != 2 {
			t.Fatalf("expected draft advanced to v2, got recs=%d r=%+v", len(f.records()), r)
		}
	})

	t.Run("two live changes on the same branch are refused and both named", func(t *testing.T) {
		f := newReadyFixture(t, map[string]string{"f.txt": "0\n"})
		id1 := strings.Repeat("k", 32)
		id2 := strings.Repeat("m", 32)
		f.git("checkout", "--quiet", "-b", "shared")
		f.write("f.txt", "1\n")
		f.git("commit", "--quiet", "-am", "one")
		f.git("push", "--quiet", "origin", "shared")
		f.mustValley("ready", "--change", id1, "shared")
		f.mustValley("ready", "--change", id2, "shared")

		f.write("f.txt", "2\n")
		f.git("commit", "--quiet", "-am", "two")
		f.git("push", "--quiet", "origin", "shared")

		code, stdout, stderr := f.valley("ready", "shared")
		if code == 0 {
			t.Fatalf("expected refusal with two live changes on branch, got exit 0:\nstdout: %s\nstderr: %s", stdout, stderr)
		}
		for _, want := range []string{"kkkk", id1, "mmmm", id2, "--change"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("refusal stderr missing %q:\n%s", want, stderr)
			}
		}
		if len(f.records()) != 2 {
			t.Errorf("refused ready created a record: got %d records", len(f.records()))
		}
	})

	t.Run("withdrawn change does not capture branch, explicit id still revives", func(t *testing.T) {
		f := newReadyFixture(t, map[string]string{"f.txt": "0\n"})
		f.git("checkout", "--quiet", "-b", "reused-w")
		f.write("f.txt", "1\n")
		f.git("commit", "--quiet", "-am", "first")
		f.git("push", "--quiet", "origin", "reused-w")
		f.mustValley("ready")
		var firstID string
		for k := range f.records() {
			firstID = k
		}
		f.mustValley("withdraw", firstID)

		// A new plain-git commit readied without --change starts a new change.
		f.write("f.txt", "2\n")
		f.git("commit", "--quiet", "-am", "second")
		f.git("push", "--quiet", "origin", "reused-w")
		out, _ := f.mustValley("ready")
		recs := f.records()
		if len(recs) != 2 {
			t.Fatalf("expected 2 changes (withdrawn + new), got %d", len(recs))
		}
		if !strings.Contains(out, "is new.") {
			t.Errorf("expected new change ID message, got %q", out)
		}
		if recs[firstID].State != change.Withdrawn {
			t.Errorf("withdrawn change was altered: %+v", recs[firstID])
		}

		// Explicit --change still revives the withdrawn change.
		f.write("f.txt", "3\n")
		f.git("commit", "--quiet", "-am", "third")
		f.git("push", "--quiet", "origin", "reused-w")
		f.mustValley("ready", "--change", firstID)
		recs = f.records()
		if recs[firstID].State != change.Ready || recs[firstID].Active != 2 {
			t.Errorf("explicit --change did not revive withdrawn change: %+v", recs[firstID])
		}
	})

	t.Run("landed change does not capture branch", func(t *testing.T) {
		f := newReadyFixture(t, map[string]string{"f.txt": "0\n"})
		f.git("checkout", "--quiet", "-b", "reused-l")
		f.write("f.txt", "1\n")
		f.git("commit", "--quiet", "-am", "lands")
		landedHead := f.git("rev-parse", "HEAD")
		f.git("push", "--quiet", "origin", "reused-l")
		f.mustValley("ready")
		var firstID string
		for k := range f.records() {
			firstID = k
		}

		// Land the commit onto origin/main (first-parent history).
		f.git("push", "--quiet", "origin", landedHead+":refs/heads/main")
		f.git("checkout", "--quiet", "main")
		f.git("reset", "--quiet", "--hard", landedHead)

		// Reuse the same branch name for new work.
		f.git("checkout", "--quiet", "reused-l")
		f.write("f.txt", "2\n")
		f.git("commit", "--quiet", "-am", "after landing")
		f.git("push", "--quiet", "origin", "reused-l")
		out, _ := f.mustValley("ready")
		recs := f.records()
		if len(recs) != 2 {
			t.Fatalf("expected 2 changes (landed + new), got %d", len(recs))
		}
		if !strings.Contains(out, "is new.") {
			t.Errorf("expected new change ID message after landing, got %q", out)
		}
		if len(recs[firstID].Versions) != 1 {
			t.Errorf("landed change gained a version: %+v", recs[firstID])
		}
	})

	t.Run("trailer and --change win over live branch change", func(t *testing.T) {
		f := newReadyFixture(t, map[string]string{"f.txt": "0\n"})
		liveID := strings.Repeat("k", 32)
		trailerID := strings.Repeat("n", 32)
		flagID := strings.Repeat("p", 32)

		f.git("checkout", "--quiet", "-b", "topic")
		f.write("f.txt", "1\n")
		f.git("commit", "--quiet", "-am", "live")
		f.git("push", "--quiet", "origin", "topic")
		f.mustValley("ready", "--change", liveID)

		// A commit carrying a Change-Id trailer uses the trailer ID, not liveID.
		f.write("f.txt", "2\n")
		f.git("commit", "--quiet", "-a", "-m", "with trailer", "-m", "Change-Id: "+trailerID)
		f.git("push", "--quiet", "origin", "topic")
		out, _ := f.mustValley("ready")
		if strings.Contains(out, "Using change") {
			t.Errorf("trailer commit should not report branch match: %q", out)
		}
		if recs := f.records(); recs[trailerID] == nil || len(recs[liveID].Versions) != 1 {
			t.Fatalf("trailer did not win over live branch change: %+v", recs)
		}

		// --change wins over both trailer and live branch change.
		f.mustValley("ready", "--change", flagID)
		if recs := f.records(); recs[flagID] == nil {
			t.Fatalf("--change did not win: %+v", recs)
		}
	})

	t.Run("--stamp and --if-active still guard branch-matched change", func(t *testing.T) {
		f := newReadyFixture(t, map[string]string{"f.txt": "0\n"})
		f.git("checkout", "--quiet", "-b", "guarded")
		f.write("f.txt", "1\n")
		f.git("commit", "--quiet", "-am", "v1")
		h1 := f.git("rev-parse", "HEAD")
		f.git("push", "--quiet", "origin", "guarded")
		f.mustValley("ready")
		var id string
		for k := range f.records() {
			id = k
		}

		// Local unpushed commit with --stamp must be refused because branch already has a change.
		f.write("f.txt", "2\n")
		f.git("commit", "--quiet", "-am", "v2")
		h2 := f.git("rev-parse", "HEAD")
		code, _, stderr := f.valley("ready", "--stamp")
		if code == 0 || !strings.Contains(stderr, "has a record on origin already") {
			t.Errorf("expected --stamp refusal on branch with live change, got code=%d stderr=%q", code, stderr)
		}
		if got := f.git("rev-parse", "HEAD"); got != h2 {
			t.Errorf("--stamp rewrote local HEAD on refusal: %s != %s", got, h2)
		}

		// Push h2 and check --if-active guard without --change.
		f.git("push", "--quiet", "origin", "guarded")
		wrong := strings.Repeat("a", 40)
		code, _, stderr = f.valley("ready", "--if-active", wrong)
		if code == 0 || !strings.Contains(stderr, "no version is added") {
			t.Errorf("expected --if-active mismatch refusal, got code=%d stderr=%q", code, stderr)
		}
		f.mustValley("ready", "--if-active", h1)
		if r := f.records()[id]; r.Active != 2 || r.Versions[1].Head != change.HeadPrefix+h2 {
			t.Errorf("--if-active with active head did not advance branch-matched change: %+v", r)
		}
	})
}

// Stale-base warning: printed on stderr when base is behind origin/main and
// paths overlap, absent when paths are disjoint or base is current, and
// skipped without failing ready if computing the warning fails.
func TestReadyStaleBaseWarning(t *testing.T) {
	baseFiles := map[string]string{
		"a.txt":     "0\n",
		"b.txt":     "0\n",
		"c.txt":     "0\n",
		"d.txt":     "0\n",
		"e.txt":     "0\n",
		"other.txt": "0\n",
	}

	t.Run("no warning when base is current", func(t *testing.T) {
		f := newReadyFixture(t, baseFiles)
		f.git("checkout", "--quiet", "-b", "current")
		f.write("a.txt", "1\n")
		f.git("commit", "--quiet", "-am", "edit a")
		f.git("push", "--quiet", "origin", "current")
		_, stderr := f.mustValley("ready")
		if stderr != "" {
			t.Errorf("expected no warning on current base, got %q", stderr)
		}
		if len(f.records()) != 1 {
			t.Fatalf("expected 1 record, got %d", len(f.records()))
		}
	})

	t.Run("no warning when base is behind but paths do not overlap", func(t *testing.T) {
		f := newReadyFixture(t, baseFiles)
		oldMain := f.git("rev-parse", "main")
		f.write("other.txt", "main\n")
		f.git("commit", "--quiet", "-am", "main edits other")
		f.git("push", "--quiet", "origin", "main")

		f.git("checkout", "--quiet", "-b", "disjoint", oldMain)
		f.write("a.txt", "branch\n")
		f.git("commit", "--quiet", "-am", "branch edits a")
		f.git("push", "--quiet", "origin", "disjoint")

		_, stderr := f.mustValley("ready")
		if stderr != "" {
			t.Errorf("expected no warning for disjoint paths, got %q", stderr)
		}
		if len(f.records()) != 1 {
			t.Fatalf("expected 1 record, got %d", len(f.records()))
		}
	})

	t.Run("warns when base is behind and one path overlaps", func(t *testing.T) {
		f := newReadyFixture(t, baseFiles)
		oldMain := f.git("rev-parse", "main")
		f.write("a.txt", "main\n")
		f.git("commit", "--quiet", "-am", "main edits a")
		f.git("push", "--quiet", "origin", "main")

		f.git("checkout", "--quiet", "-b", "overlap-one", oldMain)
		f.write("a.txt", "branch\n")
		f.git("commit", "--quiet", "-am", "branch edits a")
		f.git("push", "--quiet", "origin", "overlap-one")

		stdout, stderr := f.mustValley("ready")
		recs := f.records()
		if len(recs) != 1 {
			t.Fatalf("expected ready to write record despite warning, got %d", len(recs))
		}
		var id string
		for k := range recs {
			id = k
		}
		handle := id[:4]
		if !strings.Contains(stdout, handle+" v1 ready") {
			t.Errorf("unexpected stdout: %q", stdout)
		}
		for _, want := range []string{
			"valley: warning:",
			"a.txt",
			"restack is likely to conflict",
			"valley rebase " + handle,
			"git rebase origin/main",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stale-base warning missing %q:\n%s", want, stderr)
			}
		}
	})

	t.Run("warns with first three paths and a count when many overlap", func(t *testing.T) {
		f := newReadyFixture(t, baseFiles)
		oldMain := f.git("rev-parse", "main")
		for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"} {
			f.write(name, "main\n")
		}
		f.git("commit", "--quiet", "-am", "main edits five")
		f.git("push", "--quiet", "origin", "main")

		f.git("checkout", "--quiet", "-b", "overlap-many", oldMain)
		for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"} {
			f.write(name, "branch\n")
		}
		f.git("commit", "--quiet", "-am", "branch edits five")
		f.git("push", "--quiet", "origin", "overlap-many")

		_, stderr := f.mustValley("ready")
		if len(f.records()) != 1 {
			t.Fatalf("expected 1 record, got %d", len(f.records()))
		}
		if !strings.Contains(stderr, "a.txt, b.txt, c.txt and 2 more") {
			t.Errorf("expected 3 paths + count in warning, got:\n%s", stderr)
		}
		if strings.Contains(stderr, "d.txt") || strings.Contains(stderr, "e.txt") {
			t.Errorf("warning should not list paths past the preview limit:\n%s", stderr)
		}
	})

	t.Run("failure to compute warning does not fail ready", func(t *testing.T) {
		f := newReadyFixture(t, baseFiles)
		oldMain := f.git("rev-parse", "main")
		f.write("a.txt", "main\n")
		f.git("commit", "--quiet", "-am", "main edits a")
		f.git("push", "--quiet", "origin", "main")

		f.git("checkout", "--quiet", "-b", "diff-fails", oldMain)
		f.write("a.txt", "branch\n")
		f.git("commit", "--quiet", "-am", "branch edits a")
		f.git("push", "--quiet", "origin", "diff-fails")

		realGit, err := exec.LookPath("git")
		if err != nil {
			t.Fatal(err)
		}
		bin := t.TempDir()
		shim := "#!/bin/sh\nfor a in \"$@\"; do\n  case \"$a\" in\n    diff) exit 1 ;;\n    -*) ;;\n    *) break ;;\n  esac\ndone\nexec '" + realGit + "' \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

		_, stderr := f.mustValley("ready")
		if stderr != "" {
			t.Errorf("expected warning skipped when diff fails, got %q", stderr)
		}
		if len(f.records()) != 1 {
			t.Fatalf("expected ready to succeed when diff fails, got %d records", len(f.records()))
		}
	})
}
