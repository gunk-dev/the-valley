package main

// The content-addressing scheme the subject of an attestation is named by.
//
// valley-tree-v1 is SHA-256 over a canonical manifest of a tree's entries.
// The manifest is
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
//
// valley-inputs-v1 is SHA-256 over the same shape of manifest for a list
// of opaque names — a pure check's input paths:
//
//	"valley-inputs-v1\n"
//	for each name, sorted and deduplicated:
//	  "<byte length of name> <name>\n"

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

const (
	treeScheme   = "valley-tree-v1"
	inputsScheme = "valley-inputs-v1"

	modeFile    = "100644"
	modeExec    = "100755"
	modeSymlink = "120000"
)

// entry is one member of a tree: its git mode, its path relative to the
// tree root, and the SHA-256 of its content.
type entry struct {
	mode string
	path string
	sum  [32]byte
}

// treeManifest renders the canonical bytes digested by valley-tree-v1.
// Exposed separately from treeDigest so a test can read what is hashed.
func treeManifest(entries []entry) []byte {
	sorted := append([]entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].path < sorted[j].path })

	var b bytes.Buffer
	b.WriteString(treeScheme + "\n")
	for _, e := range sorted {
		b.WriteString(e.mode)
		b.WriteByte(' ')
		b.WriteString(hex.EncodeToString(e.sum[:]))
		b.WriteByte(' ')
		b.WriteString(strconv.Itoa(len(e.path)))
		b.WriteByte(' ')
		b.WriteString(e.path)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func treeDigest(entries []entry) string {
	return hex.EncodeToString(sha256sum(treeManifest(entries)))
}

func inputsManifest(names []string) []byte {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)

	var b bytes.Buffer
	b.WriteString(inputsScheme + "\n")
	var last string
	for i, n := range sorted {
		if i > 0 && n == last {
			continue
		}
		last = n
		b.WriteString(strconv.Itoa(len(n)))
		b.WriteByte(' ')
		b.WriteString(n)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func inputsDigest(names []string) string {
	return hex.EncodeToString(sha256sum(inputsManifest(names)))
}

func sha256sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// gitTreeEntries reads the entries of a revision's tree. The revision's
// tree, never the working tree: the subject of an attestation must be a
// function of committed content alone.
func gitTreeEntries(repo, rev string) ([]entry, error) {
	out, err := git(repo, "ls-tree", "-r", "-z", "--full-tree", rev)
	if err != nil {
		return nil, err
	}
	var entries []entry
	var oids []string
	for _, rec := range bytes.Split([]byte(out), []byte{0}) {
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
		case modeFile, modeExec, modeSymlink:
		case "160000":
			return nil, fmt.Errorf("%s is a submodule; valley-tree-v1 does not name trees that reach outside themselves", path)
		default:
			return nil, fmt.Errorf("%s has unsupported mode %s", path, mode)
		}
		entries = append(entries, entry{mode: mode, path: path})
		oids = append(oids, string(fields[2]))
	}
	sums, err := gitBlobSums(repo, oids)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].sum = sums[i]
	}
	return entries, nil
}

// fsEntries reads the entries of a directory on disk — a check's build
// output. A root that is not a directory is one entry at the empty path,
// which is what a check that writes a single file to $out produces.
func fsEntries(root string) ([]entry, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		e, err := fsEntry(root, "", info)
		if err != nil {
			return nil, err
		}
		return []entry{e}, nil
	}
	var entries []entry
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e, err := fsEntry(p, filepath.ToSlash(rel), info)
		if err != nil {
			return err
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func fsEntry(p, rel string, info fs.FileInfo) (entry, error) {
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		if err != nil {
			return entry{}, err
		}
		return entry{mode: modeSymlink, path: rel, sum: sha256.Sum256([]byte(target))}, nil
	case info.Mode().IsRegular():
		content, err := os.ReadFile(p)
		if err != nil {
			return entry{}, err
		}
		mode := modeFile
		if info.Mode().Perm()&0o111 != 0 {
			mode = modeExec
		}
		return entry{mode: mode, path: rel, sum: sha256.Sum256(content)}, nil
	default:
		return entry{}, fmt.Errorf("%s is neither a regular file nor a symlink", p)
	}
}
