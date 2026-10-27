package rebase

import (
	"context"
	"fmt"
	"os"
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
