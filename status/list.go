// valley changes: every change, across every repository a host serves,
// as valley-changes/v1. docs/architecture.md documents the object, and
// klaus reads it, so a change to any field's name or meaning is a new
// schema version.
//
// Without --all it lists the current repository. With --all it lists every
// project the serving host publishes at refs/the-valley/projects in its
// instance repository. The instance is found at policy/valley on main's
// integrated tip, never in a checked-out or pushed tree, and each
// project's URL is the instance's with its last path segment replaced, so
// the transport, user and host are kept. A repository other than the
// current one is read through a clone this command keeps under the user's
// cache directory.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"the-valley/change"
)

const (
	changesSchema = "valley-changes/v1"
	pendingSchema = "valley-pending/v1"

	projectsRef   = "refs/the-valley/projects"
	valleyPointer = "policy/valley"
	instanceLayer = "policy/instance"
)

// The valley-changes/v1 object.
type jsonChanges struct {
	Schema string     `json:"schema"`
	Host   string     `json:"host"`
	Repos  []jsonRepo `json:"repos"`
}

type jsonRepo struct {
	Repo    string       `json:"repo"`
	URL     string       `json:"url"`
	Error   *string      `json:"error"`
	Changes []jsonChange `json:"changes"`
	// Drafts is valley-pending/v1's alone: valley-changes/v1 lists a draft
	// among the changes, with its state.
	Drafts   *[]jsonChange `json:"drafts,omitempty"`
	Branches []jsonBranch  `json:"branches"`
}

type jsonChange struct {
	ChangeID string        `json:"change_id"`
	Handle   string        `json:"handle"`
	Target   string        `json:"target"`
	Owner    string        `json:"owner"`
	State    string        `json:"state"`
	Record   string        `json:"record"`
	Seq      int           `json:"seq"`
	Active   int           `json:"active"`
	Versions []jsonVersion `json:"versions"`
	Includes []string      `json:"includes"`
	Restack  *jsonRestack  `json:"restack"`
	// Knots lists the knots in the project's plan graph that link the
	// change, active knots first (does.go).
	Knots []knotRef `json:"knots"`
}

type jsonVersion struct {
	N            int             `json:"n"`
	Head         string          `json:"head"`
	Title        string          `json:"title"`
	Branch       string          `json:"branch"`
	Uploader     string          `json:"uploader"`
	Landing      string          `json:"landing"`
	LandedCommit *string         `json:"landed_commit"`
	Request      *jsonRequest    `json:"request"`
	Outcome      *versionOutcome `json:"outcome"`
}

// versionOutcome is an outcome record about a version. Its head is the
// version's head; its tip is given only when it is an object ID.
type versionOutcome struct {
	Decision string  `json:"decision"`
	Head     string  `json:"head"`
	Tip      *string `json:"tip"`
}

type jsonBranch struct {
	Branch string `json:"branch"`
	Head   string `json:"head"`
}

// listing holds the flags parsed for changes or pending. verbose warns on
// stderr when a repository's plan graph is missing or unreadable.
type listing struct {
	asJSON, all, checks, verbose bool
	repo                         string
}

func parseListing(verb string, args []string, checks bool) (listing, error) {
	var l listing
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--json":
			l.asJSON = true
		case a == "--all":
			l.all = true
		case a == "--checks" && checks:
			l.checks = true
		case a == "-v":
			l.verbose = true
		case a == "--repo" && i+1 < len(args):
			i++
			l.repo = args[i]
		case strings.HasPrefix(a, "--repo="):
			l.repo = strings.TrimPrefix(a, "--repo=")
		default:
			return l, failure(fmt.Sprintf("%s does not take %s", verb, a))
		}
	}
	if l.repo != "" && !change.ValidPrincipal(l.repo) {
		return l, failure(fmt.Sprintf("--repo %s: not a project name", l.repo))
	}
	return l, nil
}

// source is one repository to list: its name, its URL, and where it is
// read, or why it could not be.
type source struct {
	name, url string
	g         git
	err       error
}

// sources are the repositories a listing covers, in name order, and the
// host they are served from.
func sources(g git, l listing, stderr io.Writer) ([]source, string, error) {
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return nil, "", failure("not a git repository")
	}
	originURL, err := g.output(true, "remote", "get-url", "origin")
	if err != nil {
		return nil, "", failure("this repository has no origin to read")
	}
	here := source{name: project(g), url: originURL, g: g}
	if (!l.all && l.repo == "") || l.repo == here.name {
		return []source{here}, hostOf(originURL), nil
	}
	instance, names, err := discover(g)
	if err != nil && (l.repo == "" || instance == "") {
		if l.repo != "" {
			return nil, "", failure(fmt.Sprintf("cannot find %s: discovery is unavailable: %v", l.repo, err))
		}
		fmt.Fprintf(stderr, "valley: discovery is unavailable: %v; listing %s only\n", err, here.name)
		return []source{here}, hostOf(originURL), nil
	}
	if l.repo != "" {
		if names != nil && !contains(names, l.repo) {
			return nil, "", failure(fmt.Sprintf("the host lists no project %s at %s", l.repo, projectsRef))
		}
		names = []string{l.repo}
	}
	var list []source
	for _, name := range names {
		if name == here.name {
			list = append(list, here)
			continue
		}
		url, err := projectURL(instance, name)
		s := source{name: name, url: url, err: err}
		if err == nil {
			s.g, s.err = cacheRepo(g, url, name)
		}
		list = append(list, s)
	}
	return list, hostOf(instance), nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// discover finds the serving host's instance repository and the projects
// it publishes. On an error the instance is still returned when it was
// found, with names nil.
func discover(g git) (instance string, names []string, err error) {
	instance, err = instanceOf(g)
	if err != nil {
		return "", nil, err
	}
	g = g.restricted()
	oid, err := remoteRef(g, instance, projectsRef)
	if err != nil {
		return instance, nil, fmt.Errorf("could not read the instance repository %s", instance)
	}
	if oid == "" {
		return instance, nil, fmt.Errorf("the instance repository %s publishes no %s", instance, projectsRef)
	}
	if err := fetchFrom(g, instance, projectsRef, oid); err != nil {
		return instance, nil, err
	}
	list, err := g.output(true, "cat-file", "blob", oid)
	if err != nil {
		return instance, nil, fmt.Errorf("%s in %s is not a list of projects", projectsRef, instance)
	}
	for _, name := range strings.Split(list, "\n") {
		if name == "" {
			continue
		}
		if !change.ValidPrincipal(name) {
			return instance, nil, fmt.Errorf("%s in %s lists %q, which is not a project name", projectsRef, instance, name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return instance, names, nil
}

// noInstance is the error of a repository whose main names no instance
// repository at all, as opposed to one that names an instance that cannot
// be read.
type noInstance string

func (e noInstance) Error() string { return string(e) }

// instanceOf is the serving host's instance repository. It is named at
// policy/valley on main's tip on origin, which is read fresh; a repository
// whose main carries policy/instance is its own instance.
func instanceOf(g git) (string, error) {
	tip, err := remoteRef(g, "origin", "refs/heads/"+recordTarget)
	if err != nil {
		return "", errors.New("could not read origin")
	}
	if tip == "" {
		return "", noInstance("origin has no main branch to read policy/valley from")
	}
	if err := fetch(g, tip); err != nil {
		return "", err
	}
	var instance string
	if pointer, err := g.output(true, "cat-file", "blob", tip+":"+valleyPointer); err == nil {
		instance = pointerURL(pointer)
		if instance == "" {
			return "", fmt.Errorf("%s on main names no repository", valleyPointer)
		}
	} else if g.succeeds(true, "cat-file", "-e", tip+":"+instanceLayer) {
		instance, _ = g.output(true, "remote", "get-url", "origin")
	} else {
		return "", noInstance(fmt.Sprintf("main on origin names no owning valley at %s", valleyPointer))
	}
	if err := checkURL(instance); err != nil {
		return "", err
	}
	return instance, nil
}

// fetchFrom fetches an object the instance repository's ref names, unless
// it is here already. g must be restricted.
func fetchFrom(g git, instance, ref, oid string) error {
	if g.succeeds(true, "cat-file", "-e", oid) {
		return nil
	}
	// The project and the instance share no history, so git warns "no
	// common commits". The warning is kept for the error alone.
	var diag bytes.Buffer
	quiet := g
	quiet.stderr = &diag
	if _, err := quiet.output(false, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--end-of-options", instance, oid); err != nil {
		msg := fmt.Sprintf("could not fetch %s from %s", ref, instance)
		if d := strings.TrimSpace(diag.String()); d != "" {
			msg += ": " + d
		}
		return errors.New(msg)
	}
	return nil
}

// remoteRef is the object a remote's ref names, or "" when it has no such
// ref. ls-remote matches a pattern against the tail of a ref name, so a
// pushed refs/heads/x/refs/heads/main would match refs/heads/main too.
// Each ref listed is compared whole.
func remoteRef(g git, remote, ref string) (string, error) {
	out, err := g.output(false, "ls-remote", "--end-of-options", remote, ref)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if oid, name, ok := strings.Cut(line, "\t"); ok && name == ref {
			return oid, nil
		}
	}
	return "", nil
}

// allowedProtocols are the transports git may use for a URL this command
// derives from data: ssh, git, https, and file, which covers local paths.
// GIT_ALLOW_PROTOCOL overrides any protocol.*.allow the caller's
// configuration sets.
const allowedProtocols = "ssh:git:https:file"

// restricted is g with git held to allowedProtocols.
func (g git) restricted() git {
	g.env = append(append([]string(nil), g.env...), "GIT_ALLOW_PROTOCOL="+allowedProtocols)
	return g
}

// checkURL refuses a URL that is unsafe to hand git when it comes from data
// (policy/valley, or a name the host lists): one that opens with a dash,
// which git would read as an option; a <transport>::<address> form, such
// as ext::, which runs a remote helper or a command; and a URL with a
// scheme other than ssh, git, https or file. A scp-like [user@]host:path
// and a local path are allowed, but not with a host that opens with a
// dash, which ssh would read as an option.
func checkURL(u string) error {
	switch {
	case u == "":
		return errors.New("no URL")
	case strings.HasPrefix(u, "-"):
		return fmt.Errorf("the URL %q opens with a dash, which git would read as an option", u)
	case strings.ContainsFunc(u, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return fmt.Errorf("the URL %q holds a control character", u)
	case strings.HasPrefix(hostOf(u), "-"):
		return fmt.Errorf("the URL %q names a host that opens with a dash", u)
	}
	if helper, _, ok := strings.Cut(u, "::"); ok && transportName.MatchString(helper) {
		return fmt.Errorf("the URL %q names the transport %s::, and only ssh, git, https, file, scp-like and local URLs are read", u, helper)
	}
	if scheme, _, ok := strings.Cut(u, "://"); ok {
		switch scheme {
		case "ssh", "git", "https", "file":
		default:
			return fmt.Errorf("the URL %q has the scheme %s, and only ssh, git, https, file, scp-like and local URLs are read", u, scheme)
		}
	}
	return nil
}

// transportName is the shape of the transport in git's
// <transport>::<address> form.
var transportName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+.-]*$`)

// pointerURL is the first line of policy/valley that is neither blank nor
// a comment, as bin/valley's pointer_url reads it.
func pointerURL(pointer string) string {
	for _, line := range strings.Split(pointer, "\n") {
		line, _, _ = strings.Cut(line, "#")
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// projectURL is the URL of a project served beside the instance
// repository: the instance's URL with its last path segment replaced by
// the project's name. A segment ending in .git gives a name ending in .git.
// It reads every form git fetch takes: a URL with a scheme, the scp-like
// [user@]host:path, and a local path.
func projectURL(instance, name string) (string, error) {
	u := strings.TrimRight(instance, "/")
	start := 0 // where the path begins
	if scheme := strings.Index(u, "://"); scheme >= 0 {
		slash := strings.Index(u[scheme+3:], "/")
		if slash < 0 {
			return "", fmt.Errorf("the instance URL %s has no path to replace", instance)
		}
		start = scheme + 3 + slash
	} else if colon := strings.Index(u, ":"); colon >= 0 && !strings.Contains(u[:colon], "/") {
		start = colon + 1
	}
	cut := start + strings.LastIndex(u[start:], "/") + 1
	segment := u[cut:]
	if segment == "" || segment == ".git" {
		return "", fmt.Errorf("the instance URL %s has no path to replace", instance)
	}
	if strings.HasSuffix(segment, ".git") {
		name += ".git"
	}
	url := u[:cut] + name
	return url, checkURL(url)
}

// hostOf is the host a URL reaches, with its port when one is given and
// without its user. A local path reaches localhost.
func hostOf(url string) string {
	if scheme := strings.Index(url, "://"); scheme >= 0 {
		rest := url[scheme+3:]
		rest, _, _ = strings.Cut(rest, "/")
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		if rest == "" {
			return "localhost"
		}
		return rest
	}
	if colon := strings.Index(url, ":"); colon >= 0 && !strings.Contains(url[:colon], "/") {
		host := url[:colon]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		return host
	}
	return "localhost"
}

// cacheRepo is the clone a repository other than the current one is read
// through: a repository under $XDG_CACHE_HOME/valley/repos, keyed by its
// URL, with origin set to it. It holds no checkout. Only fetches by object
// ID write to it, and main's tip is kept at refs/remotes/origin/main so
// valley checks can be run inside it.
func cacheRepo(g git, url, name string) (git, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return git{}, errors.New("no cache directory to read other repositories through")
		}
		base = filepath.Join(home, ".cache")
	}
	sum := sha256.Sum256([]byte(url))
	dir := filepath.Join(base, "valley", "repos", hex.EncodeToString(sum[:8])+"-"+name)
	c := git{env: cacheEnvironment(g.env), stderr: g.stderr, dir: dir}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return git{}, fmt.Errorf("could not make the cache repository %s", dir)
		}
		if _, err := c.output(false, "init", "--quiet"); err != nil {
			return git{}, fmt.Errorf("could not make the cache repository %s", dir)
		}
		if _, err := c.output(false, "remote", "add", "--end-of-options", "origin", url); err != nil {
			return git{}, fmt.Errorf("could not set origin in %s", dir)
		}
	}
	return c, nil
}

// cacheEnvironment is env without the variables that would point git at
// another repository than the cache's, and with git held to
// allowedProtocols.
func cacheEnvironment(env []string) []string {
	var kept []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_PREFIX", "GIT_NAMESPACE":
			continue
		}
		kept = append(kept, kv)
	}
	return append(kept, "GIT_ALLOW_PROTOCOL="+allowedProtocols)
}

// listed is one repository as read: its snapshot (or the error from reading
// it) and the knots its plan graph links to its changes.
type listed struct {
	source
	snap  *snapshot
	knots doers
}

// readSources reads every source. A repository that cannot be read is
// listed with its error, and the rest are listed all the same.
func readSources(list []source, v verifier) []listed {
	var out []listed
	for _, s := range list {
		l := listed{source: s}
		if s.err == nil {
			l.snap, l.err = readSnapshot(s.g, v)
			if l.err != nil && s.g.dir != "" {
				l.err = fmt.Errorf("could not read %s", s.url)
			}
		}
		if l.err == nil && l.snap.tip != "" && l.g.dir != "" {
			// The clone's main, for valley checks to read the policy at.
			_, _ = l.g.output(true, "update-ref", "refs/remotes/origin/"+recordTarget, l.snap.tip)
		}
		out = append(out, l)
	}
	return out
}

// readKnots reads the plan graph once for each readable repository in repos.
func readKnots(repos []listed, verbose bool, stderr io.Writer) {
	for i := range repos {
		if repos[i].err == nil {
			repos[i].knots = readDoers(repos[i].g, repos[i].name, verbose, stderr)
		}
	}
}

// handleKey names one change listed: changes are keyed by repository,
// target and change ID.
type handleKey struct{ repo, target, id string }

// handleMap is the handle of each change listed.
type handleMap map[handleKey]string

// handles gives each change listed its handle among every change listed,
// in every repository, so no two share one. Every record of a repository
// is counted, listed or not, so a handle also names its change in that
// repository alone. A change ID recorded in more than one repository
// listed gets the handle <repo>:<prefix> in each, which every verb that
// takes a handle reads in that repository. git refuses a colon in a ref
// name, so a qualified handle is never also a branch's name.
func handles(repos []listed) handleMap {
	var ids []string
	in := map[string]map[string]bool{} // change ID → the repositories recording it
	for _, r := range repos {
		if r.snap == nil {
			continue
		}
		for _, n := range r.snap.all {
			ids = append(ids, n.id)
			if in[n.id] == nil {
				in[n.id] = map[string]bool{}
			}
			in[n.id][r.name] = true
		}
	}
	sort.Strings(ids)
	h := handleMap{}
	for _, r := range repos {
		if r.snap == nil {
			continue
		}
		for _, n := range r.snap.all {
			handle := change.Handle(n.id, ids)
			if len(in[n.id]) > 1 {
				handle = r.name + ":" + handle
			}
			h[handleKey{r.name, n.record.Target, n.id}] = handle
		}
	}
	return h
}

// changeView is a change as valley-changes/v1 writes it, with includes
// among the candidates given.
func (s *snapshot) changeView(n recorded, handle string, candidates []recorded) jsonChange {
	r := n.record
	c := jsonChange{
		ChangeID: n.id, Handle: handle, Target: r.Target, Owner: r.Owner, State: r.State,
		Record: n.commit, Seq: r.Seq, Active: r.Active, Includes: s.includes(n, candidates),
		Restack: restackView(s.report(n.id)), Knots: []knotRef{},
	}
	for _, v := range r.Versions {
		kind, commit := s.landing(n.id, v)
		jv := jsonVersion{
			N: v.N, Head: headOf(v), Title: v.Title, Branch: v.Branch, Uploader: v.Uploader,
			Landing: kind, Request: s.request(n.id, v),
		}
		if commit != "" {
			jv.LandedCommit = &commit
		}
		if _, m, ok := s.outcome(n.id, v); ok {
			jv.Outcome = &versionOutcome{Decision: m.decision, Head: m.head}
			if isObjectID(m.tip) {
				tip := m.tip
				jv.Outcome.Tip = &tip
			}
		}
		c.Versions = append(c.Versions, jv)
	}
	return c
}

// repoView is a repository as valley-changes/v1 writes it, holding the
// changes keep chooses, each with includes among those include chooses.
func (l listed) repoView(h handleMap, keep, include func(*snapshot, recorded) bool) jsonRepo {
	r := jsonRepo{Repo: l.name, URL: l.url, Changes: []jsonChange{}, Branches: []jsonBranch{}}
	if l.err != nil {
		msg := l.err.Error()
		r.Error = &msg
		return r
	}
	var candidates []recorded
	for _, n := range l.snap.all {
		if include(l.snap, n) {
			candidates = append(candidates, n)
		}
	}
	for _, n := range l.snap.all {
		if keep(l.snap, n) {
			c := l.snap.changeView(n, h[handleKey{l.name, n.record.Target, n.id}], candidates)
			c.Knots = l.knots.of(n.id)
			r.Changes = append(r.Changes, c)
		}
	}
	r.Branches = l.snap.unrecorded()
	return r
}

func everyChange(*snapshot, recorded) bool { return true }

// writeDocument writes a listing document as one line of JSON. Every
// string in it must be UTF-8: encoding/json would write any other bytes
// as U+FFFD, which is another string.
func writeDocument(w io.Writer, doc jsonChanges) error {
	bad := false
	walkStrings(doc, func(s string) { bad = bad || !utf8.ValidString(s) })
	if bad {
		return failure("a name in the listing is not UTF-8, so JSON cannot carry it")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// walkStrings calls f with every string a listing document holds.
func walkStrings(doc any, f func(string)) {
	switch d := doc.(type) {
	case jsonChanges:
		f(d.Schema)
		f(d.Host)
		for _, r := range d.Repos {
			walkStrings(r, f)
		}
	case jsonRepo:
		f(d.Repo)
		f(d.URL)
		if d.Error != nil {
			f(*d.Error)
		}
		all := d.Changes
		if d.Drafts != nil {
			all = append(append([]jsonChange(nil), all...), *d.Drafts...)
		}
		for _, c := range all {
			f(c.ChangeID)
			f(c.Handle)
			f(c.Target)
			f(c.Owner)
			f(c.State)
			for _, k := range c.Knots {
				f(k.ID)
				f(k.Type)
				f(k.Status)
				f(k.Title)
			}
			for _, v := range c.Versions {
				f(v.Title)
				f(v.Branch)
				f(v.Uploader)
			}
		}
		for _, b := range d.Branches {
			f(b.Branch)
		}
	}
}

// utf8Branches drops every branch whose name is not UTF-8 from a listing,
// and says so on stderr: JSON cannot carry such a name exactly.
func utf8Branches(r *jsonRepo, stderr io.Writer) {
	kept := r.Branches[:0]
	for _, b := range r.Branches {
		if !utf8.ValidString(b.Branch) {
			fmt.Fprintf(stderr, "valley: %s: left out the branch %s, whose name is not UTF-8\n", r.Repo, shellQuote(b.Branch))
			continue
		}
		kept = append(kept, b)
	}
	r.Branches = kept
}

func runChanges(g git, args []string, stdout, stderr io.Writer) error {
	l, err := parseListing("changes", args, false)
	if err != nil {
		return err
	}
	if !l.asJSON {
		return failure("changes prints valley-changes/v1 alone; pass --json. valley pending is the view to read")
	}
	list, host, err := sources(g, l, stderr)
	if err != nil {
		return err
	}
	repos := readSources(list, newVerifier())
	readKnots(repos, l.verbose, stderr)
	h := handles(repos)
	writeHandleCache(g, repos, h)
	doc := jsonChanges{Schema: changesSchema, Host: host, Repos: []jsonRepo{}}
	for _, r := range repos {
		view := r.repoView(h, everyChange, everyChange)
		utf8Branches(&view, stderr)
		doc.Repos = append(doc.Repos, view)
	}
	warnUnverified(repos, stderr)
	return writeDocument(stdout, doc)
}

// warnUnverified says which repositories held a transfer statement that
// could not be checked, so a landing there may be stronger than listed,
// and which records did not read, so are not listed.
func warnUnverified(repos []listed, stderr io.Writer) {
	for _, r := range repos {
		if r.snap != nil {
			for _, u := range r.snap.unread {
				fmt.Fprintf(stderr, "valley: warning: %s: the change record %s does not read, so it is not listed: %s. The hook refuses such a record, so origin's storage is corrupt\n", r.name, u.ref, u.why)
			}
		}
		if r.snap != nil && r.snap.unverified() {
			fmt.Fprintf(stderr, "valley: %s: a transfer statement could not be verified, so a change may have landed that is not listed so. %s\n", r.name, verifierHint)
		}
	}
}
