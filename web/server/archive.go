package server

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"the-valley/web/export"
)

// archiveSuffix ends the last segment of an archive's route.
const archiveSuffix = ".tar.gz"

// tipArchive is the last segment of the route that redirects to the
// archive of main's tip. main is the one name an archive's route takes in
// place of a full commit ID.
const tipArchive = "main" + archiveSuffix

// archiveLevel is the level the server gzips an archive at. It is fixed, so
// one commit's archive is the same bytes on every request to one build.
const archiveLevel = gzip.DefaultCompression

// archiveType is an archive's Content-Type.
const archiveType = "application/gzip"

// maxLinkTarget is the longest target of a symbolic link an archive holds:
// the longest path Linux takes, PATH_MAX. A link with a longer target fails
// its archive.
const maxLinkTarget = 4096

// archiveURL is the gzipped tar archive of commit, under its project's base.
func archiveURL(base, commit string) string {
	return base + "/source/archive/" + commit + archiveSuffix
}

// archive answers with commit's tree as a gzipped tar archive, every entry
// under one directory named for the project's web name and the commit's
// first 12 hex digits. It holds the commit's tree and nothing else: no
// history, and every file byte for byte as the tree holds it.
//
// The server writes the tar itself, from git ls-tree's listing of the tree
// and the blobs one git cat-file --batch prints. Neither reads an
// attribute, so export-ignore and export-subst are not applied, and no
// configuration or filter touches a file. Every entry carries the commit's
// committer time, so the archive's bytes depend on the commit and the
// build alone.
//
// The listing is read first, at most MaxRead bytes of it. A tree whose
// files add up to more than MaxArchiveInput, or that holds more than
// MaxArchiveEntries entries, is refused before any file is read. The
// files are then streamed through the tar and gzip writers into a body
// like any page's: from the response budget, at most the server's
// maxResponse bytes, and under the request's git slot. So the server
// holds no file whole, only the gzipped archive. An archive refused, or
// one whose gzipped bytes would pass maxResponse, answers with the short
// page that says the page is too large, never with part of the archive.
func (q *request) archive(ctx context.Context, p *export.Project, repo, commit string) error {
	tree, ok, err := commitTree(ctx, p, repo, commit)
	if err != nil || !ok {
		q.missing(err)
		return nil
	}
	when, err := commitTime(ctx, repo, commit)
	if err != nil {
		return err
	}
	entries, refused, err := listArchive(ctx, repo, tree)
	if err != nil {
		return err
	}
	if refused != "" {
		q.s.log(fmt.Errorf("serving %q: the archive is larger than the server sends: %s", q.r.URL.EscapedPath(), refused))
		q.static(http.StatusInternalServerError, htmlType, tooLargePage)
		return nil
	}
	name := p.WebName + "-" + commit[:12]
	q.drop()
	b := &body{s: q.s, ctx: q.ctx}
	if err := writeArchive(ctx, repo, b, name+"/", when, entries); err != nil {
		b.free()
		switch {
		case errors.Is(err, errNoBudget):
			q.busy()
			return nil
		case errors.Is(err, errTooLarge):
			q.tooLarge()
			return nil
		}
		return err
	}
	q.giveSlot()
	q.status, q.ctype, q.body = http.StatusOK, archiveType, b
	q.disposition = `attachment; filename="` + name + archiveSuffix + `"`
	return nil
}

// commitTime is commit's committer time, which every entry of its archive
// carries as its modification time.
func commitTime(ctx context.Context, repo, commit string) (time.Time, error) {
	out, err := export.Git(ctx, repo, maxLine, "log", "-1", "--format=%ct", commit)
	if err != nil {
		return time.Time{}, err
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("git log gave %q for the time of %s", out, commit)
	}
	return time.Unix(sec, 0), nil
}

// listArchive is every entry of tree, at every depth, in git's order,
// which puts each directory before what it holds. Each entry's Name is its
// path in the tree. refused says why an archive of tree passes the bounds
// on one: its listing is longer than MaxRead, it holds more than
// MaxArchiveEntries entries, or its files add up to more than
// MaxArchiveInput bytes. The listing stops there, and entries is then
// incomplete.
func listArchive(ctx context.Context, repo, tree string) (entries []entry, refused string, err error) {
	var total int64
	err = export.GitStream(ctx, repo, MaxRead, func(r io.Reader) error {
		recs := newRecords(r, 0, maxEntryRecord)
		for {
			rec, cut, err := recs.next()
			switch {
			case err == io.EOF:
				return nil
			case errors.Is(err, export.ErrOutputLimit):
				refused = fmt.Sprintf("the listing of its tree is longer than %d bytes", MaxRead)
				return nil
			case err != nil:
				return err
			case cut:
				return fmt.Errorf("the tree %s holds a path longer than %d bytes", tree, maxEntryRecord)
			}
			e, err := parseEntry(rec, false, true)
			if err != nil {
				return err
			}
			if err := archivable(e); err != nil {
				return fmt.Errorf("the tree %s holds %q: %w", tree, e.Name, err)
			}
			if len(entries) == MaxArchiveEntries {
				refused = fmt.Sprintf("its tree holds more than %d entries", MaxArchiveEntries)
				return nil
			}
			if e.Type == "blob" {
				if e.Size > MaxArchiveInput-total {
					refused = fmt.Sprintf("its files are larger than %d bytes", MaxArchiveInput)
					return nil
				}
				total += e.Size
			}
			entries = append(entries, e)
		}
	}, "ls-tree", "-r", "-t", "-l", "-z", tree)
	return entries, refused, err
}

// archivable fails for an entry an archive cannot hold as it is: one of a
// mode git does not give, a path with an empty, "." or ".." segment, or
// a symbolic link whose target is longer than maxLinkTarget.
func archivable(e entry) error {
	switch {
	case slices.ContainsFunc(strings.Split(e.Name, "/"), func(s string) bool { return s == "" || s == "." || s == ".." }):
		return errors.New("the path has an empty, \".\" or \"..\" segment")
	case e.Type == "tree" && e.Mode == "040000", e.Type == "commit" && e.Mode == modeSubmodule:
		return nil
	case e.Type != "blob" || e.Size < 0:
		return fmt.Errorf("git ls-tree gave it the type %s and the size %d", e.Type, e.Size)
	case e.Mode == modeSymlink && e.Size > maxLinkTarget:
		return fmt.Errorf("a symbolic link to a target of %d bytes, longer than %d", e.Size, maxLinkTarget)
	case e.Mode == modeSymlink, e.Mode == "100644", e.Mode == "100755":
		return nil
	}
	return fmt.Errorf("git ls-tree gave it the mode %s", e.Mode)
}

// writeArchive writes entries to w as a gzipped tar: the directory dir,
// then each entry under it, in order. A blob's content comes from one git
// cat-file --batch, given every blob's ID in order, and is copied into the
// tar as git prints it. Every entry carries when as its time, uid and gid
// 0, and no user or group name. A file is 0644, or 0755 when git gives it
// mode 100755, and a symbolic link is a link to its blob's content. A
// directory is a directory entry, 0755, and a submodule is one too, with
// nothing under it. Each header is the USTAR format when its name and
// target fit, and PAX when they do not.
func writeArchive(ctx context.Context, repo string, w io.Writer, dir string, when time.Time, entries []entry) error {
	zw, err := gzip.NewWriterLevel(w, archiveLevel)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(zw)
	var ids bytes.Buffer
	var printed int64 // what git cat-file prints for every blob
	for _, e := range entries {
		if e.Type == "blob" {
			ids.WriteString(e.OID + "\n")
			printed += int64(len(blobHeader(e))) + e.Size + 1
		}
	}
	write := func(blobs *bufio.Reader) error {
		if err := tw.WriteHeader(archiveDir(dir, when)); err != nil {
			return err
		}
		for _, e := range entries {
			var err error
			if e.Type == "blob" {
				err = writeBlob(tw, blobs, dir, when, e)
			} else {
				err = tw.WriteHeader(archiveDir(dir+e.Name+"/", when))
			}
			if err != nil {
				return err
			}
		}
		if blobs != nil {
			if _, err := blobs.ReadByte(); err != io.EOF {
				return fmt.Errorf("git cat-file printed more than the blobs asked for: %v", err)
			}
		}
		if err := tw.Close(); err != nil {
			return err
		}
		return zw.Close()
	}
	if ids.Len() == 0 {
		return write(nil)
	}
	return export.GitStreamInput(ctx, repo, ids.Bytes(), printed, func(r io.Reader) error {
		return write(bufio.NewReaderSize(r, pieceSize))
	}, "cat-file", "--batch")
}

// archiveDir is the header of a directory at name, which ends in "/".
func archiveDir(name string, when time.Time) *tar.Header {
	return &tar.Header{Typeflag: tar.TypeDir, Name: name, Mode: 0o755, ModTime: when}
}

// blobHeader is the line git cat-file --batch prints before e's content.
func blobHeader(e entry) string {
	return e.OID + " blob " + strconv.FormatInt(e.Size, 10) + "\n"
}

// writeBlob reads e's blob from blobs, where git cat-file --batch prints
// it next, and writes it to tw under dir. The blob must be e's ID and
// size, as the listing gave them.
func writeBlob(tw *tar.Writer, blobs *bufio.Reader, dir string, when time.Time, e entry) error {
	line, err := blobs.ReadSlice('\n')
	if err != nil {
		return fmt.Errorf("git cat-file ended before %s: %w", e.OID, err)
	}
	if string(line) != blobHeader(e) {
		return fmt.Errorf("git cat-file gave %q for the blob %s of %d bytes", line, e.OID, e.Size)
	}
	hdr := &tar.Header{Typeflag: tar.TypeReg, Name: dir + e.Name, Mode: 0o644, Size: e.Size, ModTime: when}
	switch e.Mode {
	case "100755":
		hdr.Mode = 0o755
	case modeSymlink:
		target := make([]byte, e.Size)
		if _, err := io.ReadFull(blobs, target); err != nil {
			return fmt.Errorf("git cat-file ended within %s: %w", e.OID, err)
		}
		if bytes.IndexByte(target, 0) >= 0 {
			return fmt.Errorf("the symbolic link %q has a target with a NUL byte, which tar cannot hold", e.Name)
		}
		hdr.Typeflag, hdr.Mode, hdr.Size, hdr.Linkname = tar.TypeSymlink, 0o777, 0, string(target)
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if hdr.Typeflag == tar.TypeReg {
		if _, err := io.CopyN(tw, blobs, e.Size); err != nil {
			return fmt.Errorf("copying %s into the archive: %w", e.OID, err)
		}
	}
	if end, err := blobs.ReadByte(); err != nil || end != '\n' {
		return fmt.Errorf("git cat-file did not end %s with a newline: %v", e.OID, err)
	}
	return nil
}
