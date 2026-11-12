package statement

// valley-tree-v1, the content-addressing scheme a statement's subject is
// named by.
//
// It is SHA-256 over a canonical manifest of a tree's entries. The
// manifest is
//
//	"valley-tree-v1\n"
//	for each entry, sorted by path bytes ascending:
//	  "<mode> <sha256 of content, hex> <byte length of path> <path>\n"
//
// The path's byte length is written out, so the trailing newline is a
// delimiter no path can forge. Nothing outside the tree enters the
// manifest: no commit, no author, no time, no store path, no directory
// entry (a directory is implied by the paths under it, and an empty one is
// not a thing a tree can hold). Mode is git's: 100644, 100755, or 120000
// for a symlink, whose content is its target.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const (
	// TreeScheme names the scheme in a digest set.
	TreeScheme = "valley-tree-v1"

	ModeFile    = "100644"
	ModeExec    = "100755"
	ModeSymlink = "120000"
)

// Entry is one member of a tree: its git mode, its path relative to the
// tree root, and the SHA-256 of its content.
type Entry struct {
	Mode string
	Path string
	Sum  [32]byte
}

// TreeManifest renders the canonical bytes valley-tree-v1 digests.
func TreeManifest(entries []Entry) []byte {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	var b bytes.Buffer
	b.WriteString(TreeScheme + "\n")
	for _, e := range sorted {
		b.WriteString(e.Mode)
		b.WriteByte(' ')
		b.WriteString(hex.EncodeToString(e.Sum[:]))
		b.WriteByte(' ')
		b.WriteString(strconv.Itoa(len(e.Path)))
		b.WriteByte(' ')
		b.WriteString(e.Path)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// TreeDigest is the valley-tree-v1 digest of a tree's entries, in hex.
func TreeDigest(entries []Entry) string {
	sum := sha256.Sum256(TreeManifest(entries))
	return hex.EncodeToString(sum[:])
}

// GitTree reads the entries of a revision's tree with two git commands,
// `ls-tree -r -z --full-tree <rev>` and one `cat-file --batch` for every
// blob, each made by git, which sets the repository and environment it
// runs in. It reads the revision's tree, never a working tree: the
// subject of an attestation is a function of committed content alone. A
// submodule, or any mode but the three the scheme names, is an error.
func GitTree(git func(args ...string) *exec.Cmd, rev string) ([]Entry, error) {
	out, err := output(git("ls-tree", "-r", "-z", "--full-tree", rev))
	if err != nil {
		return nil, fmt.Errorf("ls-tree %s: %w", rev, err)
	}
	var entries []Entry
	var oids []string
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		// "<mode> <type> <oid>\t<path>"
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("ls-tree: no path in %q", rec)
		}
		fields := bytes.Fields(rec[:tab])
		if len(fields) != 3 {
			return nil, fmt.Errorf("ls-tree: malformed record %q", rec[:tab])
		}
		mode, path := string(fields[0]), string(rec[tab+1:])
		switch mode {
		case ModeFile, ModeExec, ModeSymlink:
		case "160000":
			return nil, fmt.Errorf("%s is a submodule; valley-tree-v1 does not name trees that reach outside themselves", path)
		default:
			return nil, fmt.Errorf("%s has unsupported mode %s", path, mode)
		}
		entries = append(entries, Entry{Mode: mode, Path: path})
		oids = append(oids, string(fields[2]))
	}
	sums, err := blobSums(git("cat-file", "--batch"), oids)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Sum = sums[i]
	}
	return entries, nil
}

// output runs cmd and returns its stdout, with its stderr in the error. A
// command that exits zero has succeeded even when the wait for its output
// was cut short, as by exec.Cmd.WaitDelay: only something it started can
// still hold the output then.
func output(cmd *exec.Cmd) ([]byte, error) {
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(errb.String()))
	}
	return out, nil
}

// blobSums returns the SHA-256 of each named blob's content, in the order
// asked for, read through cmd, a `git cat-file --batch`.
func blobSums(cmd *exec.Cmd, oids []string) ([][32]byte, error) {
	sums := make([][32]byte, len(oids))
	if len(oids) == 0 {
		return sums, nil
	}
	cmd.Stdin = strings.NewReader(strings.Join(oids, "\n") + "\n")
	var errb bytes.Buffer
	cmd.Stderr = &errb
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r := bufio.NewReader(stdout)
	read := func() error {
		for i := range oids {
			header, err := r.ReadString('\n')
			if err != nil {
				return fmt.Errorf("cat-file: %w: %s", err, strings.TrimSpace(errb.String()))
			}
			fields := strings.Fields(strings.TrimSpace(header))
			if len(fields) != 3 || fields[1] != "blob" {
				return fmt.Errorf("cat-file: unexpected header %q for %s", strings.TrimSpace(header), oids[i])
			}
			size, err := strconv.Atoi(fields[2])
			if err != nil {
				return fmt.Errorf("cat-file: bad size in %q", strings.TrimSpace(header))
			}
			h := sha256.New()
			if _, err := io.CopyN(h, r, int64(size)); err != nil {
				return fmt.Errorf("cat-file: reading %s: %w", oids[i], err)
			}
			if _, err := r.Discard(1); err != nil { // the record's trailing newline
				return err
			}
			copy(sums[i][:], h.Sum(nil))
		}
		return nil
	}
	if err := read(); err != nil {
		// What is left unread is discarded, so the command can end.
		io.Copy(io.Discard, r)
		cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return nil, fmt.Errorf("cat-file: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return sums, nil
}
