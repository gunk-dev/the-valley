package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"the-valley/web/export"
	"the-valley/web/markdown"
)

// treeURL is the directory at path in commit, under the base of its
// project, /<group>/<project>, escaped. It ends in a slash.
func treeURL(base, commit string, path []string) string {
	u := base + "/source/tree/" + commit + "/"
	if len(path) > 0 {
		u += escapePath(path) + "/"
	}
	return u
}

// blobURL is the file at path in commit, under its project's base.
func blobURL(base, commit string, path []string) string {
	return base + "/source/blob/" + commit + "/" + escapePath(path)
}

// logURL is the history that ends at commit, under its project's base.
func logURL(base, commit string) string {
	return base + "/source/log/" + commit
}

func escapePath(path []string) string {
	parts := make([]string, len(path))
	for i, s := range path {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// source serves the routes under /<group>/<project>/source/. The project is
// checked first, so everything under a project the reader may not see
// answers as a missing project does. A route that reads the repository is
// returned, for runGit to run once the request holds a git slot: its view
// runs git under the request's git context, and gives back its git slot
// once it renders. Every other route is answered from the index alone.
func (q *request) source(project string, rest []string) *gitOp {
	p, ok := q.project(project)
	if !ok {
		q.notFound()
		return nil
	}
	var view func(ctx context.Context, repo string) error
	switch {
	case len(rest) == 0 || len(rest) == 1 && rest[0] == "":
		q.redirect(treeURL(q.idx.bases[p.Name], p.SourceTip, nil))
		return nil
	case len(rest) == 2 && rest[0] == "archive" && rest[1] == tipArchive:
		q.redirect(archiveURL(q.idx.bases[p.Name], p.SourceTip))
		return nil
	case len(rest) >= 2 && rest[0] == "tree":
		path := rest[2:]
		if n := len(path); n > 0 && path[n-1] == "" {
			path = path[:n-1]
		}
		if !validPath(path) {
			q.notFound()
			return nil
		}
		view = func(ctx context.Context, repo string) error { return q.dir(ctx, p, repo, rest[1], path) }
	case len(rest) >= 3 && rest[0] == "blob":
		path := rest[2:]
		if !validPath(path) {
			q.notFound()
			return nil
		}
		view = func(ctx context.Context, repo string) error { return q.file(ctx, p, repo, rest[1], path) }
	case len(rest) == 2 && rest[0] == "log":
		view = func(ctx context.Context, repo string) error { return q.log(ctx, p, repo, rest[1]) }
	case len(rest) == 2 && rest[0] == "archive":
		commit, ok := strings.CutSuffix(rest[1], archiveSuffix)
		if !ok {
			q.notFound()
			return nil
		}
		view = func(ctx context.Context, repo string) error { return q.archive(ctx, p, repo, commit) }
	default:
		q.notFound()
		return nil
	}
	return &gitOp{project: p, run: view}
}

// validPath is a path a source route may name: at most MaxDepth
// segments, none of them empty, a dot segment, or holding a slash or a
// NUL.
func validPath(path []string) bool {
	if len(path) > MaxDepth {
		return false
	}
	for _, s := range path {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\x00") {
			return false
		}
	}
	return true
}

// commitTree is the tree of commit, when commit is a commit the
// project's published source tip reaches. ok is false for anything else:
// an unreachable commit, another kind of object, or text that is no full
// object ID.
func commitTree(ctx context.Context, p *export.Project, repo, commit string) (tree string, ok bool, err error) {
	reachable, err := export.Reachable(ctx, repo, p.SourceTip, commit)
	if err != nil || !reachable {
		return "", false, err
	}
	out, err := export.Git(ctx, repo, maxLine, "rev-parse", "--verify", commit+"^{tree}")
	if err != nil {
		return "", false, err
	}
	tree = strings.TrimSpace(string(out))
	if !export.ValidObjectID(tree) {
		return "", false, fmt.Errorf("the tree of %s is %q", commit, tree)
	}
	return tree, true, nil
}

// The bounds on what a page keeps of one record of git's output.
const (
	// maxLine bounds what git prints for one object ID.
	maxLine = 1 << 10
	// maxEntryRecord bounds one entry of git ls-tree: its mode, type,
	// object ID and size, and its name. git fsck refuses a name longer
	// than 4,096 bytes, so the receiver accepts no entry this cuts.
	maxEntryRecord = 8 << 10
	// maxLogRecord bounds each of the two records git log gives for one
	// commit: its ID and parents, then its date, author and subject. It
	// holds a commit's ID and its first MaxParents parents in either
	// object format. A page shows maxLogText of the author and subject.
	maxLogRecord = 16 << 10
	maxLogText   = 1 << 10
)

// The ID and first MaxParents parents of a SHA-256 commit, each followed
// by one byte, fit in maxLogRecord.
const _ = uint(maxLogRecord - 65*(1+MaxParents))

// records reads the records of git's output that end in sep, and keeps
// at most max bytes of each.
type records struct {
	r   *bufio.Reader
	sep byte
	buf []byte
}

func newRecords(r io.Reader, sep byte, max int) *records {
	return &records{r: bufio.NewReaderSize(r, max), sep: sep}
}

// next is the next record, without its sep. A record longer than the
// bound is cut to it, with cut set, and the rest of it is read and
// dropped. At the end of the output next fails with io.EOF. On any other
// error, rec is the start of the record the error cut short.
func (rs *records) next() (rec []byte, cut bool, err error) {
	line, err := rs.r.ReadSlice(rs.sep)
	rs.buf = append(rs.buf[:0], line...)
	for err == bufio.ErrBufferFull {
		cut = true
		_, err = rs.r.ReadSlice(rs.sep)
	}
	switch {
	case err == io.EOF && len(rs.buf) > 0:
		return rs.buf, cut, nil // a last record git did not end
	case err != nil:
		return rs.buf, cut, err
	case !cut:
		return rs.buf[:len(rs.buf)-1], false, nil
	}
	return rs.buf, true, nil
}

// entry is one entry of a tree, as git ls-tree gives it. Size is -1 for
// an entry that is not a blob, and for a blob read without --long. Cut is
// set when the entry's name is longer than a page keeps, and Name is then
// its start alone.
type entry struct {
	Mode string
	Type string
	OID  string
	Size int64
	Name string
	Cut  bool
}

// readTree calls each with the entries of tree, in git's order, until
// each returns false. It reads at most limit bytes of git ls-tree's
// output: complete is false when the listing is longer and was cut there.
// read is how many bytes it read. long gives each blob's size, which git
// looks up for every blob listed, so only a page that shows sizes asks.
func readTree(ctx context.Context, repo, tree string, limit int64, long bool,
	each func(entry) bool) (complete bool, read int64, err error) {
	complete = true
	args := []string{"ls-tree", "-z", tree}
	if long {
		args = []string{"ls-tree", "-z", "--long", tree}
	}
	err = export.GitStream(ctx, repo, limit, func(r io.Reader) error {
		counted := &counter{r: r}
		defer func() { read = counted.n }()
		recs := newRecords(counted, 0, maxEntryRecord)
		for {
			rec, cut, err := recs.next()
			switch {
			case err == io.EOF:
				return nil
			case errors.Is(err, export.ErrOutputLimit):
				complete = false
				return nil
			case err != nil:
				return err
			}
			e, err := parseEntry(rec, cut, long)
			if err != nil {
				return err
			}
			if !each(e) {
				return nil
			}
		}
	}, args...)
	return complete, read, err
}

// counter reads r, and counts the bytes it read.
type counter struct {
	r io.Reader
	n int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// parseEntry is one record of git ls-tree: mode, type, object ID, and,
// when long, size, then a tab and the name.
func parseEntry(rec []byte, cut, long bool) (entry, error) {
	meta, name, ok := bytes.Cut(rec, []byte{'\t'})
	f := strings.Fields(string(meta))
	fields := 3
	if long {
		fields = 4
	}
	if !ok || len(f) != fields || !export.ValidObjectID(f[2]) {
		return entry{}, fmt.Errorf("git ls-tree gave %q", rec)
	}
	e := entry{Mode: f[0], Type: f[1], OID: f[2], Size: -1, Name: string(name), Cut: cut}
	if long && f[3] != "-" {
		n, err := strconv.ParseInt(f[3], 10, 64)
		if err != nil || n < 0 {
			return entry{}, fmt.Errorf("git ls-tree gave %q", rec)
		}
		e.Size = n
	}
	return e, nil
}

// resolve walks path down from the tree root, one tree entry at a time,
// and gives the entry it ends at, with its size when it is a blob. ok is
// false when a segment names no entry, or a segment other than the last
// names something that is not a tree. Each directory is read only until
// the segment's entry, without sizes, and all of them together no further
// than MaxRead bytes of git's output. So an entry past MaxRead along the
// path is not found, however the path's directories share it.
func resolve(ctx context.Context, repo, root string, path []string) (e entry, ok bool, err error) {
	e = entry{Mode: "040000", Type: "tree", OID: root, Size: -1}
	left := int64(MaxRead)
	for _, name := range path {
		if e.Type != "tree" {
			return entry{}, false, nil
		}
		found := false
		_, read, err := readTree(ctx, repo, e.OID, left, false, func(c entry) bool {
			if !c.Cut && c.Name == name {
				e, found = c, true
			}
			return !found
		})
		if left -= read; err != nil || !found {
			return entry{}, false, err
		}
	}
	if e.Type == "blob" {
		out, err := export.Git(ctx, repo, maxLine, "cat-file", "-s", e.OID)
		if err != nil {
			return entry{}, false, err
		}
		n, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if err != nil || n < 0 {
			return entry{}, false, fmt.Errorf("git cat-file -s %s gave %q", e.OID, out)
		}
		e.Size = n
	}
	return e, true, nil
}

// Entry modes git gives for a symbolic link and a submodule.
const (
	modeSymlink   = "120000"
	modeSubmodule = "160000"
)

// shown is how a page shows a blob: its text, or a notice that says why
// it is not shown. A symbolic link is shown as its target and is never
// followed.
func shown(ctx context.Context, repo string, e entry) (text, notice string, err error) {
	if e.Size > MaxFile {
		return "", fmt.Sprintf("This file is %s, larger than the %s a page shows.", size(e.Size), size(MaxFile)), nil
	}
	data, err := export.Git(ctx, repo, MaxFile, "cat-file", "blob", e.OID)
	if err != nil {
		return "", "", err
	}
	if e.Mode == modeSymlink {
		return "", "A symbolic link to " + oneLine(string(data)) + ". It is not followed.", nil
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", fmt.Sprintf("This file is binary, %s, and is not shown.", size(e.Size)), nil
	}
	return string(data), "", nil
}

// dirPage is one directory of a commit, under its project header.
type dirPage struct {
	Head    projectHead
	Project string
	Commit  string
	Short   string
	AtTip   bool
	TipURL  string
	LogURL  string
	// ArchiveURL is the commit's archive, which the page links to.
	ArchiveURL string
	Crumbs     []link
	Entries    []dirEntry
	Omitted    int
	// Partial is set when the directory is larger than a page reads,
	// and only its start is listed.
	Partial bool
	Readme  *readme
}

type dirEntry struct {
	Name string
	URL  string
	Kind string
	Size string
}

type readme struct {
	Name   string
	URL    string
	Text   string
	Notice string
	// Partial is set when the README is longer than a page shows.
	Partial bool
	// Doc is the README rendered, when it is Markdown.
	Doc *document
}

// document is a Markdown file rendered, and the contents list of a long
// one.
type document struct {
	HTML     template.HTML
	Contents []contentsEntry
}

type contentsEntry struct {
	Text  string
	URL   string
	Level int
}

// isMarkdown reports whether a file's name says it is Markdown: it ends
// in .md or .markdown, in any case.
func isMarkdown(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, ".md") || strings.HasSuffix(n, ".markdown")
}

// markdown renders text, a file of dir in commit, with its relative links
// resolved within the project's source at that commit. A document with
// at least four headings at levels 2 to 4 gets a contents list of them.
// The rendered document is at most MaxShown bytes, the most a page spends
// on a file's text, and cut is set when it holds only the start of text.
//
// It waits for a rendering slot while it holds its git slot, and gives
// the git slot back once it has one: rendering runs no git. It renders
// under the request's context.
func (q *request) markdown(text, project, commit string, dir []string) (d *document, cut bool, err error) {
	wait := q.ctx
	if q.git != nil {
		wait = q.git
	}
	select {
	case q.s.renders <- struct{}{}:
	case <-wait.Done():
		return nil, false, fmt.Errorf("waiting to render Markdown: %w", wait.Err())
	}
	defer func() { <-q.s.renders }()
	q.giveSlot()
	doc, err := markdown.Render(q.ctx, []byte(text), markdown.Options{
		Resolve: resolver(q.idx.bases[project], commit, dir), MaxOutput: MaxShown})
	if err != nil {
		return nil, false, fmt.Errorf("rendering Markdown: %w", err)
	}
	d = &document{HTML: doc.HTML}
	for _, h := range doc.Headings {
		if h.Level >= 2 && h.Level <= 4 && h.ID != "" {
			d.Contents = append(d.Contents, contentsEntry{oneLine(h.Text), "#" + (&url.URL{Fragment: h.ID}).EscapedFragment(), h.Level})
		}
	}
	if len(d.Contents) < 4 {
		d.Contents = nil
	}
	return d, doc.Truncated, nil
}

// resolver resolves a relative link of a document in dir, in commit, to
// the page of its target at the same commit. A path that starts with /
// starts at the repository's root. A fragment is kept, and a link that is
// a fragment alone stays one. A path that climbs above the root, or that
// is no path a source route takes, is refused. The resolver runs no git:
// a link to a directory goes to the blob route, which redirects it to
// the tree route, and one that ends in / goes to the tree route.
func resolver(base, commit string, dir []string) func(string) (string, bool) {
	return func(dest string) (string, bool) {
		rest, frag, hasFrag := strings.Cut(dest, "#")
		if hasFrag {
			f, err := url.PathUnescape(frag)
			if err != nil {
				return "", false
			}
			frag = "#" + (&url.URL{Fragment: f}).EscapedFragment()
		}
		rest, _, _ = strings.Cut(rest, "?")
		if rest == "" {
			return frag, hasFrag
		}
		path := slices.Clone(dir)
		if strings.HasPrefix(rest, "/") {
			path, rest = nil, rest[1:]
		}
		segs := strings.Split(rest, "/")
		for i, seg := range segs {
			seg, err := url.PathUnescape(seg)
			switch {
			case err != nil:
				return "", false
			case seg == "" && i == len(segs)-1, seg == ".":
			case seg == "..":
				if len(path) == 0 {
					return "", false
				}
				path = path[:len(path)-1]
			default:
				path = append(path, seg)
			}
		}
		if !validPath(path) {
			return "", false
		}
		if len(path) == 0 || strings.HasSuffix(rest, "/") {
			return treeURL(base, commit, path) + frag, true
		}
		return blobURL(base, commit, path) + frag, true
	}
}

func (q *request) dir(ctx context.Context, p *export.Project, repo, commit string, path []string) error {
	root, ok, err := commitTree(ctx, p, repo, commit)
	if err != nil || !ok {
		q.missing(err)
		return nil
	}
	e, ok, err := resolve(ctx, repo, root, path)
	if err == nil && ok && e.Type == "blob" && len(path) > 0 {
		q.redirect(blobURL(q.idx.bases[p.Name], commit, path)) // a file, named as a directory
		return nil
	}
	if err != nil || !ok || e.Type != "tree" {
		q.missing(err)
		return nil
	}
	// Directories come first, then everything else, each in git's
	// order. Keeping MaxEntries of each keeps the first MaxEntries of
	// that order, however many entries the tree holds.
	var dirs, others []entry
	read, rank := 0, -1
	var rm entry
	complete, _, err := readTree(ctx, repo, e.OID, MaxRead, true, func(c entry) bool {
		read++
		switch {
		case c.Type == "tree" && len(dirs) < MaxEntries:
			dirs = append(dirs, c)
		case c.Type != "tree" && len(others) < MaxEntries:
			others = append(others, c)
		}
		if r := readmeRank(c); r >= 0 && (rank < 0 || r < rank) {
			rm, rank = c, r
		}
		return true
	})
	if err != nil {
		return err
	}
	entries := append(dirs, others...)
	if len(entries) > MaxEntries {
		entries = entries[:MaxEntries]
	}
	page := dirPage{Head: q.idx.head(p.Name, "source"), Project: p.Name, Commit: commit, Short: commit[:12], AtTip: commit == p.SourceTip,
		TipURL: treeURL(q.idx.bases[p.Name], p.SourceTip, nil), LogURL: logURL(q.idx.bases[p.Name], commit),
		ArchiveURL: archiveURL(q.idx.bases[p.Name], commit), Crumbs: crumbs(p.Name, q.idx.bases[p.Name], commit, path),
		Omitted: read - len(entries), Partial: !complete}
	for _, c := range entries {
		at := append(append([]string{}, path...), c.Name)
		d := dirEntry{Name: oneLine(c.Name)}
		switch {
		case c.Type == "tree":
			d.Kind, d.URL = "directory", treeURL(q.idx.bases[p.Name], commit, at)
		case c.Mode == modeSubmodule || c.Type == "commit":
			d.Kind = "submodule at " + c.OID[:12]
		case c.Mode == modeSymlink:
			d.Kind, d.URL = "symbolic link", blobURL(q.idx.bases[p.Name], commit, at)
		default:
			d.Kind, d.URL, d.Size = "file", blobURL(q.idx.bases[p.Name], commit, at), size(c.Size)
		}
		if c.Cut {
			d.Name += "…"
		}
		if c.Cut || !validPath(at) {
			d.URL = "" // a name no route takes is listed and not linked
		}
		page.Entries = append(page.Entries, d)
	}
	if rank >= 0 {
		text, notice, err := shown(ctx, repo, rm)
		if err != nil {
			return err
		}
		lines, _, partial := excerpt(trimEnd(text), 1)
		at := append(append([]string{}, path...), rm.Name)
		page.Readme = &readme{Name: oneLine(rm.Name), URL: blobURL(q.idx.bases[p.Name], commit, at),
			Text: strings.Join(lines, "\n"), Notice: notice, Partial: partial}
		if notice == "" && isMarkdown(rm.Name) {
			doc, cut, err := q.markdown(page.Readme.Text, p.Name, commit, path)
			if err != nil {
				return err
			}
			page.Readme.Doc = doc
			page.Readme.Partial = page.Readme.Partial || cut
		}
	}
	title := p.Name + " at " + commit[:12]
	if len(path) > 0 {
		title = strings.Join(path, "/") + " · " + title
	}
	q.page(http.StatusOK, "tree", title, page)
	return nil
}

// readmeRank is how a directory's README is chosen from its entries:
// README.md, README or README.txt, in that order, and otherwise the first
// file whose name is README in any case, with any extension. A lower rank
// is chosen first. It is -1 for an entry that is no README.
func readmeRank(e entry) int {
	if e.Type != "blob" || e.Mode == modeSymlink || e.Cut {
		return -1
	}
	switch e.Name {
	case "README.md":
		return 0
	case "README":
		return 1
	case "README.txt":
		return 2
	}
	if base, _, _ := strings.Cut(strings.ToLower(e.Name), "."); base == "readme" {
		return 3
	}
	return -1
}

// filePage is one file of a commit, under its project header.
type filePage struct {
	Head    projectHead
	Project string
	Commit  string
	Short   string
	AtTip   bool
	TipURL  string
	LogURL  string
	Crumbs  []link
	Name    string
	Size    string
	Lines   []string
	// Partial says where the page stops, when the file is longer than a
	// page shows.
	Partial string
	Notice  string
	// Doc is the file rendered, when it is Markdown and shown so. Plain
	// and Rendered link the file's two views, when it has two.
	Doc      *document
	Plain    string
	Rendered string
}

func (q *request) file(ctx context.Context, p *export.Project, repo, commit string, path []string) error {
	root, ok, err := commitTree(ctx, p, repo, commit)
	if err != nil || !ok {
		q.missing(err)
		return nil
	}
	e, ok, err := resolve(ctx, repo, root, path)
	if err == nil && ok && e.Type == "tree" {
		q.redirect(treeURL(q.idx.bases[p.Name], commit, path)) // a directory, named as a file
		return nil
	}
	if err != nil || !ok || e.Type != "blob" {
		q.missing(err)
		return nil
	}
	text, notice, err := shown(ctx, repo, e)
	if err != nil {
		return err
	}
	page := filePage{Head: q.idx.head(p.Name, "source"), Project: p.Name, Commit: commit, Short: commit[:12], AtTip: commit == p.SourceTip,
		TipURL: treeURL(q.idx.bases[p.Name], p.SourceTip, nil), LogURL: logURL(q.idx.bases[p.Name], commit),
		Crumbs: crumbs(p.Name, q.idx.bases[p.Name], commit, path), Name: oneLine(path[len(path)-1]),
		Size: size(e.Size), Notice: notice}
	if notice == "" {
		md := isMarkdown(path[len(path)-1])
		plain := !md || q.r.URL.Query().Get("plain") == "1"
		cost := rowCost
		if !plain {
			cost = 1
		}
		lines, total, partial := excerpt(text, cost)
		if partial {
			page.Partial = fmt.Sprintf("This file is longer than a page shows. The page stops at line %d of %d.", len(lines), total)
		}
		here := blobURL(q.idx.bases[p.Name], commit, path)
		switch {
		case plain && md:
			page.Lines, page.Rendered = lines, here
		case plain:
			page.Lines = lines
		default:
			doc, cut, err := q.markdown(strings.Join(lines, "\n"), p.Name, commit, path[:len(path)-1])
			if err != nil {
				return err
			}
			page.Doc, page.Plain = doc, here+"?plain=1"
			if cut {
				page.Partial = "This file is longer than a page shows, and only its start is shown."
			}
		}
	}
	q.page(http.StatusOK, "blob", strings.Join(path, "/")+" · "+p.Name+" at "+commit[:12], page)
	return nil
}

// crumbs are the links from the project's root down to path. The last is
// the page itself and has no link.
func crumbs(project, base, commit string, path []string) []link {
	out := []link{{project, treeURL(base, commit, nil)}}
	for i, name := range path {
		out = append(out, link{oneLine(name), treeURL(base, commit, path[:i+1])})
	}
	out[len(out)-1].URL = ""
	return out
}

// rowCost is what a file page's markup takes for each line it shows,
// besides the line's text: a table row, an anchor and the line number.
// It is an upper bound for line numbers of up to seven digits.
const rowCost = 100

// excerpt is the start of text that a page shows: whole lines, at most
// MaxLines of them, while the bytes they take once escaped, with cost
// more for each line, stay within MaxShown. A line that would pass
// MaxShown is cut to fit, and ends the excerpt. total is how many lines
// text holds, and partial is set when the excerpt holds less than all of
// it.
func excerpt(text string, cost int) (lines []string, total int, partial bool) {
	text = strings.TrimSuffix(text, "\n")
	total = strings.Count(text, "\n") + 1
	budget := MaxShown
	for rest := text; len(lines) < MaxLines; {
		line, more, found := strings.Cut(rest, "\n")
		n := cost + escapedSize(line)
		if n > budget {
			if room := budget - cost; room > 0 {
				lines = append(lines, cutEscaped(line, room))
			}
			return lines, total, true
		}
		budget -= n
		lines = append(lines, line)
		if !found {
			return lines, total, false
		}
		rest = more
	}
	return lines, total, true
}

// escapedSize is an upper bound on the bytes html/template writes for s
// as text: five for each character it escapes, and one for any other
// byte.
func escapedSize(s string) int {
	n := len(s)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\'', '&', '+', '<', '>', 0:
			n += 4
		}
	}
	return n
}

// cutEscaped is the longest start of s that ends on a whole character
// and that escapedSize counts at no more than room bytes.
func cutEscaped(s string, room int) string {
	n := 0
	for i := 0; i < len(s); i++ {
		if n += escapedSize(s[i : i+1]); n > room {
			for i > 0 && !utf8.RuneStart(s[i]) {
				i--
			}
			return s[:i]
		}
	}
	return s
}

// clip is s cut to at most n bytes, on a whole character, and marked as
// cut when it is.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// logPage is one page of the history that ends at a commit, under its
// project header.
type logPage struct {
	Head    projectHead
	Project string
	Commit  string
	Short   string
	AtTip   bool
	Commits []logEntry
	// Partial is set when the page's commits are larger than a page
	// reads, and the page ends early.
	Partial bool
	// Crowded is set when the page ends early, before a merge that would
	// leave more than MaxLeftToShow commits to show.
	Crowded bool
	Newest  string
	Older   string
	// Others link the history from each commit the page leaves to show
	// past the MaxFrontier its Older link starts from. Lines is how many
	// it leaves to show in all, when there are others.
	Others []link
	Lines  int
}

type logEntry struct {
	Commit  string
	Short   string
	URL     string
	Subject string
	Author  string
	Date    string
	// Parents is set when the commit has more than MaxParents parents,
	// and says that only the first are followed.
	Parents string
}

// log serves one page of the history that ends at commit: LogPage
// commits, newest first, and none before any of its children, as git log
// --date-order gives them. The first page starts from commit, and an
// older page from the commits its ?from= names, each of which commit must
// reach.
//
// A page's older pages start from what the page leaves to show: the
// commits it started from and the parents of each commit it shows, less
// the commits it shows. Since no page shows a commit before its children,
// those commits reach every commit of the history that no page before
// showed, and none that one did. Each page asks git for LogPage commits
// however old it is.
//
// The Older link starts from at most MaxFrontier of those commits, and
// each of the rest has a link of its own. A page leaves at most
// MaxLeftToShow, and ends early before a merge that would leave more. So
// the links from a page reach every older commit of the history, merges
// and all, along the first MaxParents parents of each commit. A page
// marks a merge that has more. The Older links alone show each commit
// once while no page leaves more than MaxFrontier; the history from each
// other link may show some again.
func (q *request) log(ctx context.Context, p *export.Project, repo, commit string) error {
	if _, ok, err := commitTree(ctx, p, repo, commit); err != nil || !ok {
		q.missing(err)
		return nil
	}
	page := logPage{Head: q.idx.head(p.Name, "source"), Project: p.Name, Commit: commit, Short: commit[:12],
		AtTip: commit == p.SourceTip}
	from := []string{commit}
	if query := q.r.URL.Query(); query.Has("from") {
		ids, ok := frontier(query["from"])
		if !ok {
			q.notFound()
			return nil
		}
		if ok, err := export.Reachable(ctx, repo, commit, ids...); err != nil || !ok {
			q.missing(err)
			return nil
		}
		from, page.Newest = ids, logURL(q.idx.bases[p.Name], commit)
	}
	// older is what the older page starts from. git gives no commit
	// before its children, so no parent added is a commit shown already.
	older := map[string]bool{}
	for _, id := range from {
		older[id] = true
	}
	// git gives two records for each commit, so that a long line of
	// parents cuts neither the commit's ID nor its subject.
	args := []string{"log", "--date-order", "--no-mailmap", "--no-notes", "--no-show-signature",
		"--no-decorate", "--no-color", "-z", "--format=%H%n%P%x00%aI%n%an%n%s",
		"--max-count=" + strconv.Itoa(LogPage)}
	args = append(append(args, from...), "--")
	// A listing cut at MaxRead ends the page at the commit it cut, shown
	// as far as it was read once its ID and the parents a page follows
	// were read whole.
	err := export.GitStream(ctx, repo, MaxRead, func(r io.Reader) error {
		recs := newRecords(r, 0, maxLogRecord)
		for len(page.Commits) < LogPage {
			rec, cut, err := recs.next()
			if err == io.EOF {
				return nil
			}
			limited := errors.Is(err, export.ErrOutputLimit)
			if err != nil && !limited {
				return err
			}
			id, parents, more, ok := parentsOf(string(rec), !cut && !limited)
			if !ok && len(page.Commits) == 0 {
				return fmt.Errorf("git log gave a first commit whose ID and parents were not read: %q", clip(string(rec), maxLogText))
			}
			if !ok {
				page.Partial = true // the older page starts from the commit
				return nil
			}
			if len(page.Commits) > 0 && leftAfter(older, id, parents) > MaxLeftToShow {
				page.Crowded = true // the older pages start from the commit
				return nil
			}
			var meta []byte
			if !limited {
				meta, _, err = recs.next()
				if err == io.EOF {
					return fmt.Errorf("git log gave no date, author or subject for %s", id)
				}
				if limited = errors.Is(err, export.ErrOutputLimit); err != nil && !limited {
					return err
				}
			}
			e := logEntryOf(q.idx.bases[p.Name], id, string(meta))
			if more {
				e.Parents = fmt.Sprintf("This merge has more than %d parents. Older pages follow its first %d.", MaxParents, MaxParents)
			}
			page.Commits = append(page.Commits, e)
			delete(older, id)
			for _, parent := range parents {
				older[parent] = true
			}
			if limited {
				page.Partial = true
				return nil
			}
		}
		return nil
	}, args...)
	if err != nil {
		return err
	}
	ids := slices.Sorted(maps.Keys(older))
	if len(ids) > MaxFrontier {
		for _, id := range ids[MaxFrontier:] {
			page.Others = append(page.Others, link{id[:12], logURL(q.idx.bases[p.Name], commit) + "?from=" + id})
		}
		ids, page.Lines = ids[:MaxFrontier], len(ids)
	}
	if len(ids) > 0 {
		page.Older = logURL(q.idx.bases[p.Name], commit) + "?from=" + strings.Join(ids, ",")
	}
	q.page(http.StatusOK, "log", "History of "+p.Name+" at "+commit[:12], page)
	return nil
}

// A page always shows its first commit. That leaves at most the other
// MaxFrontier-1 commits it started from and the commit's MaxParents
// parents, which must be within MaxLeftToShow.
const _ = uint(MaxLeftToShow - (MaxFrontier - 1 + MaxParents))

// leftAfter is how many commits a page leaves to show once it shows
// commit, when it leaves left before: commit is no longer left, and its
// parents are.
func leftAfter(left map[string]bool, commit string, parents []string) int {
	n := len(left)
	if left[commit] {
		n--
	}
	for _, parent := range parents {
		if !left[parent] {
			n++
		}
	}
	return n
}

// frontier is the commits a ?from= names: one value, of 1 to MaxFrontier
// object IDs, separated by commas, in ascending order and none twice, so
// each page has one address. ok is false for anything else. Whether the
// page's commit reaches each of them is Reachable's to say.
func frontier(values []string) (ids []string, ok bool) {
	if len(values) != 1 {
		return nil, false
	}
	ids = strings.Split(values[0], ",")
	if len(ids) > MaxFrontier {
		return nil, false
	}
	for i, id := range ids {
		if !export.ValidObjectID(id) || i > 0 && ids[i-1] >= id {
			return nil, false
		}
	}
	return ids, true
}

// parentsOf is the first record git log gives for a commit: its ID, then
// its parents on one line. whole is false when the record was cut, and
// its last parent may then be cut too. parents is the commit's first
// MaxParents parents, and more is set when it has others. ok is false
// when the record holds no whole ID, or a parent that is not an ID of
// the commit's length, or when it was cut before the parents a page
// follows.
func parentsOf(rec string, whole bool) (id string, parents []string, more, ok bool) {
	id, line, found := strings.Cut(rec, "\n")
	if !found || !export.ValidObjectID(id) {
		return "", nil, false, false
	}
	f := strings.Fields(line)
	if !whole && len(f) <= MaxParents {
		return "", nil, false, false
	}
	parents, more = f[:min(len(f), MaxParents)], len(f) > MaxParents
	for _, parent := range parents {
		if len(parent) != len(id) || !export.ValidObjectID(parent) {
			return "", nil, false, false
		}
	}
	return id, parents, more, true
}

// logEntryOf is a commit's entry on a page: its ID, and the second record
// git log gives for it, its author date, author and subject, each on a
// line of its own. A record cut short is shown as far as it goes.
func logEntryOf(base, id, rec string) logEntry {
	f := strings.SplitN(rec, "\n", 3)
	for len(f) < 3 {
		f = append(f, "")
	}
	return logEntry{Commit: id, Short: id[:12], URL: treeURL(base, id, nil), Date: stampOf(f[0]),
		Author: clip(oneLine(f[1]), maxLogText), Subject: clip(oneLine(f[2]), maxLogText)}
}

// missing answers a source route whose commit or path names nothing it
// serves: the one 404, or the one 500 when git failed.
func (q *request) missing(err error) {
	if err != nil {
		q.fail(err)
		return
	}
	q.notFound()
}

// oneLine is text from a repository as a page shows it on one line:
// valid UTF-8, with control and bidirectional formatting characters
// replaced, so a name cannot reorder or hide the text around it.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return '�'
		}
		return r
	}, strings.ToValidUTF8(s, "�"))
}

// size is a byte count in words.
func size(n int64) string {
	switch {
	case n < 0:
		return ""
	case n < 1<<10:
		return count(int(n), "byte")
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
}
