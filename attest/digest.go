package main

// valley-inputs-v1, the digest of a pure check's input paths, and the
// entries of a check's build output, which valley-tree-v1 digests as it
// digests a git tree (note/statement/tree.go).
//
// valley-inputs-v1 is SHA-256 over the same shape of manifest
// valley-tree-v1 uses, for a list of opaque names:
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

	"the-valley/note/statement"
)

const inputsScheme = "valley-inputs-v1"

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

// fsEntries reads the entries of a directory on disk — a check's build
// output. A root that is not a directory is one entry at the empty path,
// which is what a check that writes a single file to $out produces.
func fsEntries(root string) ([]statement.Entry, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		e, err := fsEntry(root, "", info)
		if err != nil {
			return nil, err
		}
		return []statement.Entry{e}, nil
	}
	var entries []statement.Entry
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

func fsEntry(p, rel string, info fs.FileInfo) (statement.Entry, error) {
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		if err != nil {
			return statement.Entry{}, err
		}
		return statement.Entry{Mode: statement.ModeSymlink, Path: rel, Sum: sha256.Sum256([]byte(target))}, nil
	case info.Mode().IsRegular():
		content, err := os.ReadFile(p)
		if err != nil {
			return statement.Entry{}, err
		}
		mode := statement.ModeFile
		if info.Mode().Perm()&0o111 != 0 {
			mode = statement.ModeExec
		}
		return statement.Entry{Mode: mode, Path: rel, Sum: sha256.Sum256(content)}, nil
	default:
		return statement.Entry{}, fmt.Errorf("%s is neither a regular file nor a symlink", p)
	}
}
