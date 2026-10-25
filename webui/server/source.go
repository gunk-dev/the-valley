package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"the-valley/webui/export"
)

// treeURL is the directory at path in commit. It ends in a slash.
func treeURL(project, commit string, path []string) string {
	u := "/source/" + url.PathEscape(project) + "/tree/" + commit + "/"
	if len(path) > 0 {
		u += escapePath(path) + "/"
	}
	return u
}

// blobURL is the file at path in commit.
func blobURL(project, commit string, path []string) string {
	return "/source/" + url.PathEscape(project) + "/blob/" + commit + "/" + escapePath(path)
}

// logURL is the history that ends at commit.
func logURL(project, commit string) string {
	return "/source/" + url.PathEscape(project) + "/log/" + commit
}

func escapePath(path []string) string {
	parts := make([]string, len(path))
	for i, s := range path {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// source serves the routes under /source/<project>/. The project is
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
		http.Redirect(q.w, q.r, treeURL(p.Name, p.SourceTip, nil), http.StatusFound)
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
	ctx, done, err := q.gitContext()
	if err != nil {
		return // the reader went away
	}
	defer done()
	if err := view(ctx); err != nil {
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
	out, err := export.Git(ctx, repo, "rev-parse", "--verify", commit+"^{tree}")
	if err != nil {
		return "", false, err
	}
	tree = strings.TrimSpace(string(out))
	if !export.ValidObjectID(tree) {
		return "", false, fmt.Errorf("the tree of %s is %q", commit, tree)
	}
	return tree, true, nil
}

// entry is one entry of a tree, as git ls-tree --long gives it. Size is
// -1 for an entry that is not a blob.
type entry struct {
	Mode string
	Type string
	OID  string
	Size int64
	Name string
}

func listTree(ctx context.Context, repo, tree string) ([]entry, error) {
	out, err := export.Git(ctx, repo, "ls-tree", "-z", "--long", tree)
	if err != nil {
		return nil, err
	}
	var entries []entry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, name, ok := bytes.Cut(rec, []byte{'\t'})
		f := strings.Fields(string(meta))
		if !ok || len(f) != 4 || !export.ValidObjectID(f[2]) {
			return nil, fmt.Errorf("git ls-tree gave %q", rec)
		}
		e := entry{Mode: f[0], Type: f[1], OID: f[2], Size: -1, Name: string(name)}
		if f[3] != "-" {
			n, err := strconv.ParseInt(f[3], 10, 64)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("git ls-tree gave %q", rec)
			}
			e.Size = n
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// resolve walks path down from the tree root, one tree entry at a time,
// and gives the entry it ends at. ok is false when a segment names no
// entry, or a segment other than the last names something that is not a
// tree.
func resolve(ctx context.Context, repo, root string, path []string) (e entry, ok bool, err error) {
	e = entry{Mode: "040000", Type: "tree", OID: root, Size: -1}
	for _, name := range path {
		if e.Type != "tree" {
			return entry{}, false, nil
		}
		entries, err := listTree(ctx, repo, e.OID)
		if err != nil {
			return entry{}, false, err
		}
		found := false
		for _, c := range entries {
			if c.Name == name {
				e, found = c, true
				break
			}
		}
		if !found {
			return entry{}, false, nil
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
	data, err := export.Git(ctx, repo, "cat-file", "blob", e.OID)
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
	entries, err := listTree(ctx, repo, e.OID)
	if err != nil {
		return err
	}
	page := dirPage{Project: p.Name, Commit: commit, Short: commit[:12], AtTip: commit == p.SourceTip,
		TipURL: treeURL(p.Name, p.SourceTip, nil), LogURL: logURL(p.Name, commit),
		Crumbs: crumbs(p.Name, commit, path)}
	// Directories first, then everything else, each in git's order.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Type == "tree" && entries[j].Type != "tree" })
	for i, c := range entries {
		if i == MaxEntries {
			page.Omitted = len(entries) - MaxEntries
			break
		}
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
		if !validPath(at) {
			d.URL = "" // a name no route takes is listed and not linked
		}
		page.Entries = append(page.Entries, d)
	}
	if r, ok := readmeOf(entries); ok {
		text, notice, err := shown(ctx, repo, r)
		if err != nil {
			return err
		}
		at := append(append([]string{}, path...), r.Name)
		page.Readme = &readme{Name: oneLine(r.Name), URL: blobURL(p.Name, commit, at), Text: trimEnd(text), Notice: notice}
	}
	title := p.Name + " at " + commit[:12]
	if len(path) > 0 {
		title = strings.Join(path, "/") + " · " + title
	}
	q.page(http.StatusOK, "tree", title, page)
	return nil
}

// readmeOf is the directory's README: README.md, README or README.txt
// when there is one, and otherwise the first file whose name is README
// in any case, with any extension.
func readmeOf(entries []entry) (entry, bool) {
	for _, name := range []string{"README.md", "README", "README.txt"} {
		for _, e := range entries {
			if e.Name == name && e.Type == "blob" && e.Mode != modeSymlink {
				return e, true
			}
		}
	}
	for _, e := range entries {
		base, _, _ := strings.Cut(strings.ToLower(e.Name), ".")
		if base == "readme" && e.Type == "blob" && e.Mode != modeSymlink {
			return e, true
		}
	}
	return entry{}, false
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
		page.Lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
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

// logPage is one page of the history that ends at a commit.
type logPage struct {
	Project string
	Commit  string
	Short   string
	AtTip   bool
	Commits []logEntry
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
	out, err := export.Git(ctx, repo, "log", "--no-mailmap", "--no-notes", "--no-show-signature",
		"--no-decorate", "--no-color", "-z", "--format=%H%n%an%n%aI%n%s",
		"--max-count="+strconv.Itoa(LogPage+1), "--skip="+strconv.Itoa(skip), commit, "--")
	if err != nil {
		return err
	}
	page := logPage{Project: p.Name, Commit: commit, Short: commit[:12], AtTip: commit == p.SourceTip}
	for _, rec := range strings.Split(string(out), "\x00") {
		f := strings.SplitN(strings.TrimPrefix(rec, "\n"), "\n", 4)
		if len(f) != 4 || !export.ValidObjectID(f[0]) {
			continue
		}
		if len(page.Commits) == LogPage {
			page.Older = logURL(p.Name, commit) + "?skip=" + strconv.Itoa(skip+LogPage)
			break
		}
		page.Commits = append(page.Commits, logEntry{Short: f[0][:12], URL: treeURL(p.Name, f[0], nil),
			Author: oneLine(f[1]), Date: stampOf(f[2]), Subject: oneLine(f[3])})
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
