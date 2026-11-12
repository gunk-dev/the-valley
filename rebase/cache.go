// What a snapshot remembers between commands: facts about objects that
// never change, kept under the user's cache directory, outside every
// checkout.
//
//	<cache>/trees/<tree ID>        the valley-tree-v1 digest of a git tree
//	<cache>/landings/<SHA-256>     a transfer statement that passed every
//	                               content check (transfer.go)
//
// A tree entry is the line "valley-tree-v1 <tree ID> <digest>", then a
// line holding the SHA-256 of the first. A landing entry is the text that
// names what was checked, under the SHA-256 of that text: it is found only
// when the file holds exactly that text. An entry that does not read as
// one, such as one cut short or damaged, or one that is not a regular
// file, is not found, and the work it would have saved is done again.
// Each entry is written to a file of its own and renamed into place, so
// commands running at once never see one half written. Nothing here fails
// a command or prints anything: a cache that cannot be read or written
// leaves the work to be done each time.
//
// A landing entry is never a version's landing evidence. It records that
// a statement's content checks passed for one head, landing commit,
// request name and signing key; the snapshot still checks, on every read,
// that the key is accepted, that the integrator's records bind the head to
// the statement, and that the landing commit is on main.

package rebase

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CacheDir is the directory a snapshot remembers verified transfer
// statements in: valley/transfers under the user's cache directory
// (os.UserCacheDir, which honours XDG_CACHE_HOME), beside the clones
// valley keeps there, or "" when there is none.
func CacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "valley", "transfers")
}

// cache is a snapshot's cache directory, or none when dir is "".
type cache struct{ dir string }

// entryLimit is the most an entry is read to: every entry is far smaller.
const entryLimit = 4096

// tree is the digest remembered for a tree, if one is.
func (c cache) tree(oid string) (string, bool) {
	data, ok := c.read("trees", oid)
	if !ok {
		return "", false
	}
	line, sum, ok := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if !ok || sum != hexSum(line)+"\n" || len(fields) != 3 || fields[0] != "valley-tree-v1" || fields[1] != oid || !isHex(fields[2], 64) {
		return "", false
	}
	return fields[2], true
}

// putTree remembers a tree's digest.
func (c cache) putTree(oid, digest string) {
	line := "valley-tree-v1 " + oid + " " + digest
	c.write("trees", oid, line+"\n"+hexSum(line)+"\n")
}

// landing reports whether a landing entry holding text is remembered.
func (c cache) landing(text string) bool {
	data, ok := c.read("landings", hexSum(text))
	return ok && string(data) == text
}

// putLanding remembers a landing entry holding text.
func (c cache) putLanding(text string) {
	c.write("landings", hexSum(text), text)
}

func hexSum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// read is the content of an entry, if it reads. Only a regular file is
// an entry: the name is opened without following a symlink or waiting
// (entryFlags), so a FIFO, a device or a symlink in an entry's place is a
// miss, and never a read that blocks.
func (c cache) read(kind, name string) ([]byte, bool) {
	if c.dir == "" || !isHex(name, len(name)) || name == "" {
		return nil, false
	}
	f, err := os.OpenFile(filepath.Join(c.dir, kind, name), os.O_RDONLY|entryFlags, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, entryLimit))
	return data, err == nil
}

// write puts an entry in place whole: written to a file of its own in the
// same directory, then renamed over its name.
func (c cache) write(kind, name, content string) {
	if c.dir == "" || !isHex(name, len(name)) || name == "" {
		return
	}
	dir := filepath.Join(c.dir, kind)
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	f, err := os.CreateTemp(dir, ".new-*")
	if err != nil {
		return
	}
	_, err = f.WriteString(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(dir, name))
	}
	if err != nil {
		os.Remove(f.Name())
	}
}
