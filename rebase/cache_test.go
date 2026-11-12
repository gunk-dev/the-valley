//go:build unix

package rebase

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Only a regular file is a cache entry. A FIFO, which an open to read
// would wait on for a writer, a symlink to one, and a symlink to an entry
// that reads, each in place of a landing entry and of a tree entry, is a
// miss, and the read returns at once.
func TestACacheEntryThatIsNotARegularFileIsAMiss(t *testing.T) {
	c := cache{dir: t.TempDir()}
	text, oid, digest := "the-valley transfer landing\n", strings.Repeat("ab", 20), strings.Repeat("cd", 32)
	c.putLanding(text)
	c.putTree(oid, digest)
	reads := map[string]func() bool{
		filepath.Join(c.dir, "landings", hexSum(text)): func() bool { return c.landing(text) },
		filepath.Join(c.dir, "trees", oid):             func() bool { _, ok := c.tree(oid); return ok },
	}
	for path, read := range reads {
		if !read() {
			t.Fatalf("the entry at %s does not read", path)
		}
		kept, fifo := filepath.Join(t.TempDir(), "kept"), filepath.Join(t.TempDir(), "fifo")
		if err := os.Rename(path, kept); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		for what, plant := range map[string]func() error{
			"a FIFO":                           func() error { return syscall.Mkfifo(path, 0o600) },
			"a symlink to a FIFO":              func() error { return os.Symlink(fifo, path) },
			"a symlink to an entry that reads": func() error { return os.Symlink(kept, path) },
		} {
			os.Remove(path)
			if err := plant(); err != nil {
				t.Fatal(err)
			}
			found := make(chan bool, 1)
			go func() { found <- read() }()
			select {
			case ok := <-found:
				if ok {
					t.Errorf("%s at %s is found", what, path)
				}
			case <-time.After(time.Minute):
				t.Fatalf("reading %s at %s did not return", what, path)
			}
		}
	}
}
