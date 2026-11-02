package rebase

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A snapshot whose fetch fails because main moved after the listing reads
// origin again. Here origin rewrites main, and prunes its old tip, as the
// snapshot fetches. Once, and the read made again finds main at its new
// tip. On every fetch, and the read ends with the fetch's error after
// snapshotTries listings.
func TestASnapshotReadsAgainWhenMainMovesUnderIt(t *testing.T) {
	for _, c := range []struct {
		name  string
		races int
	}{{"once", 1}, {"every time", snapshotTries}} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.moveMain("main.txt", "main\n")
			listed := w.at("refs/heads/main")
			reader := filepath.Join(t.TempDir(), "reader")
			w.run(filepath.Dir(reader), "init", "--quiet", reader)
			w.run(reader, "remote", "add", "origin", w.origin)
			calls := filepath.Join(t.TempDir(), "calls")
			w.run(reader, "config", "remote.origin.uploadpack", racing(t, calls, c.races))

			s, err := ReadSnapshot(context.Background(), Git{Dir: reader, Env: w.env}, Verifier{})
			data, _ := os.ReadFile(calls)
			n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			if c.races == snapshotTries {
				if err == nil || n != 2*snapshotTries {
					t.Errorf("a read that raced main every time ended with %v, after %d calls to origin", err, n)
				}
				return
			}
			if err != nil {
				t.Fatalf("the read did not survive main moving under it: %v", err)
			}
			if now := w.at("refs/heads/main"); s.Tip != now || now == listed || n != 4 {
				t.Errorf("the snapshot has main at %s, origin at %s, after %d calls to origin", short(s.Tip), short(now), n)
			}
		})
	}
}

// racing is an upload-pack that rewrites origin's main, and prunes its old
// tip, before each of the first races fetches it serves. A snapshot lists
// origin and then fetches, so every second call is a fetch. It counts its
// calls in the file given.
func racing(t *testing.T, calls string, races int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "racing-upload-pack")
	script := fmt.Sprintf(`#!/bin/sh
n=$(( $(cat '%[1]s' 2>/dev/null || echo 0) + 1 ))
echo "$n" > '%[1]s'
if [ $((n %% 2)) -eq 0 ] && [ $((n / 2)) -le %[2]d ]; then
  tree=$(git -C "$1" rev-parse 'refs/heads/main^{tree}')
  new=$(git -C "$1" commit-tree "$tree" -m "main rewritten, call $n")
  git -C "$1" update-ref refs/heads/main "$new"
  git -C "$1" gc --quiet --prune=now
fi
exec git upload-pack "$@"
`, calls, races)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A snapshot answers whether one commit is an ancestor of another as git
// does, for every pair, from one read of main's history and each head's
// own commits, without asking git about any pair. Main here holds a
// merge. One branch forks below it, one from the side it merged, one from
// main's tip, and one merges main back in, so its history meets main's at
// two commits.
func TestAncestryIsAnsweredAsGitAnswersIt(t *testing.T) {
	w := newWorld(t)
	base := w.git("rev-parse", "HEAD")
	m1 := w.commit("m1", "main.txt", "1\n")
	w.git("checkout", "--quiet", "-b", "side", base)
	s1 := w.commit("s1", "side.txt", "s\n")
	w.git("checkout", "--quiet", "main")
	w.git("merge", "--quiet", "--no-ff", "-m", "merge side", "side")
	merge := w.git("rev-parse", "HEAD")
	m2 := w.commit("m2", "main.txt", "2\n")
	branch := func(name, from string) string {
		w.git("checkout", "--quiet", "-b", name, from)
		return w.commit(name, name+".txt", name+"\n")
	}
	x1 := branch("x", m1)
	x2 := w.commit("x2", "x.txt", "x2\n")
	y := branch("y", m2)
	s2 := branch("s", s1)
	w.git("checkout", "--quiet", "-b", "z", x2)
	w.git("merge", "--quiet", "--no-ff", "-m", "merge main", m2)
	z := w.git("rev-parse", "HEAD")
	w.git("push", "--quiet", "origin", "main", "x", "y", "s", "z")

	commits := []string{base, m1, s1, merge, m2, x1, x2, y, s2, z}
	want := map[[2]string]bool{}
	for _, a := range commits {
		for _, b := range commits {
			want[[2]string{a, b}] = w.ancestor(a, b)
		}
	}

	// Every git the snapshot starts goes through a shim that logs it.
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shim, calls := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> '%s'\nexec '%s' \"$@\"\n", calls, real)
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))

	s, err := ReadSnapshot(context.Background(), w.g, Verifier{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range commits {
		if got := s.Reaches(a); got != want[[2]string{a, m2}] {
			t.Errorf("Reaches(%s) is %v, and git says %v", short(a), got, !got)
		}
		for _, b := range commits {
			if got := s.StrictAncestor(a, b); got != (a != b && want[[2]string{a, b}]) {
				t.Errorf("StrictAncestor(%s, %s) is %v, and git says %v", short(a), short(b), got, !got)
			}
		}
	}
	log, _ := os.ReadFile(calls)
	if n := strings.Count(string(log), "merge-base --is-ancestor"); n != 0 {
		t.Errorf("the snapshot asked git about %d pairs:\n%s", n, log)
	}
}
