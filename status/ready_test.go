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
	sh("config", "user.name", "valley-go-test")
	sh("config", "user.email", "valley-go-test@localhost")
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
