package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"the-valley/webui/export"
)

// treeURL is the directory at path in commit. It ends in a slash.
func treeURL(project, commit string, path []string) string {
	u := "/" + url.PathEscape(project) + "/source/tree/" + commit + "/"
	if len(path) > 0 {
		u += escapePath(path) + "/"
	}
	return u
}

// blobURL is the file at path in commit.
func blobURL(project, commit string, path []string) string {
	return "/" + url.PathEscape(project) + "/source/blob/" + commit + "/" + escapePath(path)
}

// logURL is the history that ends at commit.
func logURL(project, commit string) string {
	return "/" + url.PathEscape(project) + "/source/log/" + commit
}

func escapePath(path []string) string {
	parts := make([]string, len(path))
	for i, s := range path {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// source serves the routes under /<project>/source/. The project is
// checked first, so everything under a project the export does not hold
// as public answers as a missing project does.
func (q *request) source(project string, rest []string) {
	p, ok := q.project(project)
	if !ok {
		q.notFound()
		return
	}
	repo, ok := q.g.Repo(p.Name)
	if !ok {
		q.notFound()
		return
	}
	var view func(ctx context.Context) error
	switch {
	case len(rest) == 0 || len(rest) == 1 && rest[0] == "":
		q.redirect(treeURL(p.Name, p.SourceTip, nil))
		return
	case len(rest) >= 2 && rest[0] == "tree":
		path := rest[2:]
		if n := len(path); n > 0 && path[n-1] == "" {
			path = path[:n-1]
		}
		if !validPath(path) {
			q.notFound()
			return
		}
		view = func(ctx context.Context) error { return q.dir(ctx, p, repo, rest[1], path) }
	case len(rest) >= 3 && rest[0] == "blob":
		path := rest[2:]
		if !validPath(path) {
			q.notFound()
			return
		}
		view = func(ctx context.Context) error { return q.file(ctx, p, repo, rest[1], path) }
	case len(rest) == 2 && rest[0] == "log":
		view = func(ctx context.Context) error { return q.log(ctx, p, repo, rest[1]) }
	default:
		q.notFound()
		return
	}
	if err := view(q.ctx); err != nil {
		q.fail(err)
	}
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
	// maxLogRecord bounds one commit of git log: its ID, date, author
	// and subject. A page shows maxLogText of the author and subject.
	maxLogRecord = 16 << 10
	maxLogText   = 1 << 10
)

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

// entry is one entry of a tree, as git ls-tree --long gives it. Size is
// -1 for an entry that is not a blob. Cut is set when the entry's name is
// longer than a page keeps, and Name is then its start alone.
type entry struct {
	Mode string
	Type string
	OID  string
	Size int64
	Name string
	Cut  bool
}

// readTree calls each with the entries of tree, in git's order, until
// each returns false. It reads at most MaxRead bytes of git ls-tree's
// output: complete is false when the listing is longer and was cut there.
func readTree(ctx context.Context, repo, tree string, each func(entry) bool) (complete bool, err error) {
	complete = true
	err = export.GitStream(ctx, repo, MaxRead, func(r io.Reader) error {
		recs := newRecords(r, 0, maxEntryRecord)
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
			e, err := parseEntry(rec, cut)
			if err != nil {
				return err
			}
			if !each(e) {
				return nil
			}
		}
	}, "ls-tree", "-z", "--long", tree)
	return complete, err
}

func parseEntry(rec []byte, cut bool) (entry, error) {
	meta, name, ok := bytes.Cut(rec, []byte{'\t'})
	f := strings.Fields(string(meta))
	if !ok || len(f) != 4 || !export.ValidObjectID(f[2]) {
		return entry{}, fmt.Errorf("git ls-tree gave %q", rec)
	}
	e := entry{Mode: f[0], Type: f[1], OID: f[2], Size: -1, Name: string(name), Cut: cut}
	if f[3] != "-" {
		n, err := strconv.ParseInt(f[3], 10, 64)
		if err != nil || n < 0 {
			return entry{}, fmt.Errorf("git ls-tree gave %q", rec)
		}
		e.Size = n
	}
	return e, nil
}

// resolve walks path down from the tree root, one tree entry at a time,
// and gives the entry it ends at. ok is false when a segment names no
// entry, or a segment other than the last names something that is not a
// tree. Each directory is read only until the segment's entry, and no
// further than readTree reads, so an entry past MaxRead is not found.
func resolve(ctx context.Context, repo, root string, path []string) (e entry, ok bool, err error) {
	e = entry{Mode: "040000", Type: "tree", OID: root, Size: -1}
	for _, name := range path {
		if e.Type != "tree" {
			return entry{}, false, nil
		}
		found := false
		_, err := readTree(ctx, repo, e.OID, func(c entry) bool {
			if !c.Cut && c.Name == name {
				e, found = c, true
			}
			return !found
		})
		if err != nil || !found {
			return entry{}, false, err
		}
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

// dirPage is one directory of a commit.
type dirPage struct {
	Project string
	Commit  string
	Short   string
	AtTip   bool
	TipURL  string
	LogURL  string
	Crumbs  []link
	Entries []dirEntry
	Omitted int
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
}

func (q *request) dir(ctx context.Context, p *export.Project, repo, commit string, path []string) error {
	root, ok, err := commitTree(ctx, p, repo, commit)
	if err != nil || !ok {
		q.missing(err)
		return nil
	}
	e, ok, err := resolve(ctx, repo, root, path)
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
	complete, err := readTree(ctx, repo, e.OID, func(c entry) bool {
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
	page := dirPage{Project: p.Name, Commit: commit, Short: commit[:12], AtTip: commit == p.SourceTip,
		TipURL: treeURL(p.Name, p.SourceTip, nil), LogURL: logURL(p.Name, commit),
		Crumbs: crumbs(p.Name, commit, path), Omitted: read - len(entries), Partial: !complete}
	for _, c := range entries {
		at := append(append([]string{}, path...), c.Name)
		d := dirEntry{Name: oneLine(c.Name)}
		switch {
		case c.Type == "tree":
			d.Kind, d.URL = "directory", treeURL(p.Name, commit, at)
		case c.Mode == modeSubmodule || c.Type == "commit":
			d.Kind = "submodule at " + c.OID[:12]
		case c.Mode == modeSymlink:
			d.Kind, d.URL = "symbolic link", blobURL(p.Name, commit, at)
		default:
			d.Kind, d.URL, d.Size = "file", blobURL(p.Name, commit, at), size(c.Size)
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
		page.Readme = &readme{Name: oneLine(rm.Name), URL: blobURL(p.Name, commit, at),
			Text: strings.Join(lines, "\n"), Notice: notice, Partial: partial}
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

// filePage is one file of a commit.
type filePage struct {
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
}

func (q *request) file(ctx context.Context, p *export.Project, repo, commit string, path []string) error {
	root, ok, err := commitTree(ctx, p, repo, commit)
	if err != nil || !ok {
		q.missing(err)
		return nil
	}
	e, ok, err := resolve(ctx, repo, root, path)
	if err != nil || !ok || e.Type != "blob" {
		q.missing(err)
		return nil
	}
	text, notice, err := shown(ctx, repo, e)
	if err != nil {
		return err
	}
	page := filePage{Project: p.Name, Commit: commit, Short: commit[:12], AtTip: commit == p.SourceTip,
		TipURL: treeURL(p.Name, p.SourceTip, nil), LogURL: logURL(p.Name, commit),
		Crumbs: crumbs(p.Name, commit, path), Name: oneLine(path[len(path)-1]),
		Size: size(e.Size), Notice: notice}
	if notice == "" {
		lines, total, partial := excerpt(text, rowCost)
		page.Lines = lines
		if partial {
			page.Partial = fmt.Sprintf("This file is longer than a page shows. The page stops at line %d of %d.", len(lines), total)
		}
	}
	q.page(http.StatusOK, "blob", strings.Join(path, "/")+" · "+p.Name+" at "+commit[:12], page)
	return nil
}

// crumbs are the links from the project's root down to path. The last is
// the page itself and has no link.
func crumbs(project, commit string, path []string) []link {
	out := []link{{project, treeURL(project, commit, nil)}}
	for i, name := range path {
		out = append(out, link{oneLine(name), treeURL(project, commit, path[:i+1])})
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

// logPage is one page of the history that ends at a commit.
type logPage struct {
	Project string
	Commit  string
	Short   string
	AtTip   bool
	Commits []logEntry
	// Partial is set when the page's commits are larger than a page
	// reads, and the page ends early.
	Partial bool
	Newer   string
	Older   string
}

type logEntry struct {
	Short   string
	URL     string
	Subject string
	Author  string
	Date    string
}

func (q *request) log(ctx context.Context, p *export.Project, repo, commit string) error {
	skip := 0
	if s := q.r.URL.Query().Get("skip"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 1_000_000 || strconv.Itoa(n) != s {
			q.notFound()
			return nil
		}
		skip = n
	}
	if _, ok, err := commitTree(ctx, p, repo, commit); err != nil || !ok {
		q.missing(err)
		return nil
	}
	// One commit past the page says whether there is an older page. A
	// listing cut at MaxRead ends the page at the commit it cut, shown
	// as far as it was read, and the older page starts after it.
	page := logPage{Project: p.Name, Commit: commit, Short: commit[:12], AtTip: commit == p.SourceTip}
	err := export.GitStream(ctx, repo, MaxRead, func(r io.Reader) error {
		recs := newRecords(r, 0, maxLogRecord)
		for len(page.Commits) <= LogPage {
			rec, _, err := recs.next()
			cut := errors.Is(err, export.ErrOutputLimit)
			if err == io.EOF {
				return nil
			}
			if err != nil && !cut {
				return err
			}
			if e, ok := logEntryOf(p.Name, string(rec)); ok {
				page.Commits = append(page.Commits, e)
			}
			if cut {
				page.Partial = true
				return nil
			}
		}
		return nil
	}, "log", "--no-mailmap", "--no-notes", "--no-show-signature",
		"--no-decorate", "--no-color", "-z", "--format=%H%n%aI%n%an%n%s",
		"--max-count="+strconv.Itoa(LogPage+1), "--skip="+strconv.Itoa(skip), commit, "--")
	if err != nil {
		return err
	}
	switch {
	case len(page.Commits) > LogPage:
		page.Commits = page.Commits[:LogPage]
		page.Older = logURL(p.Name, commit) + "?skip=" + strconv.Itoa(skip+LogPage)
	case page.Partial && len(page.Commits) > 0:
		page.Older = logURL(p.Name, commit) + "?skip=" + strconv.Itoa(skip+len(page.Commits))
	}
	if skip > 0 {
		page.Newer = logURL(p.Name, commit)
		if skip > LogPage {
			page.Newer += "?skip=" + strconv.Itoa(skip-LogPage)
		}
	}
	q.page(http.StatusOK, "log", "History of "+p.Name+" at "+commit[:12], page)
	return nil
}

// logEntryOf is one record of git log: a commit's ID, author date,
// author and subject, each on a line of its own. A record cut short is
// shown as far as it goes. ok is false for a record that names no commit.
func logEntryOf(project, rec string) (e logEntry, ok bool) {
	f := strings.SplitN(rec, "\n", 4)
	if !export.ValidObjectID(f[0]) {
		return logEntry{}, false
	}
	for len(f) < 4 {
		f = append(f, "")
	}
	return logEntry{Short: f[0][:12], URL: treeURL(project, f[0], nil), Date: stampOf(f[1]),
		Author: clip(oneLine(f[2]), maxLogText), Subject: clip(oneLine(f[3]), maxLogText)}, true
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
