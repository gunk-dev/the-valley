// valley knot: the plan graph's API. A project's plan is a graph of
// knots at refs/the-valley/knots on origin (the knot module), and these
// verbs read it and write it without a checkout and without review: each
// write is one commit on the ref, built with git plumbing on a private
// index and pushed with a lease on the tip it was built on.
//
// When the lease fails, the operation is formed again on the new tip and
// pushed again, at most knot.Attempts times in all, after a random pause.
// set, edit, link --parent, claim and unclaim are compare-and-set: each
// remembers the value of every field it changes as it first read it, and
// when a retry finds another value there it reports a conflict instead of
// overwriting it. set's --force skips that comparison. --if adds a
// comparison of its own, on a field the knot module names, which holds on
// every try. Every operation carries a Valley-Op trailer naming an ID
// chosen once, so after a push whose outcome it cannot tell, the verb
// looks for the ID among the ref's latest commits before it tries again,
// and an operation is never recorded twice.

package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"the-valley/knot"
)

const knotUsage = `usage: valley knot <verb> [args]

  new <type> <title> [--parent ID] [--blocked-by ID]... [--owner P] [--target PROJECT] [--body -]
  show <id> [--json]
  list [--type T] [--status S] [--owner P] [--ready] [--json]
  set <id> <field>=<value>... [--if <field>=<value>]... [--force]
  link <id> (--blocked-by ID | --parent ID | --change LINK)...
  unlink <id> (--blocked-by ID | --parent ID | --change LINK)...
  edit <id> --body -
  comment <id> (-m TEXT | --body -)
  claim <id> --run RUN
  unclaim <id>
  schema [--json]

every verb takes --project NAME and --json. exit status 3 is a conflict.`

// conflict is a write refused because the graph no longer holds what the
// operation was formed on, or what --if requires. Exit status 3.
type conflict string

func (c conflict) Error() string { return string(c) }

// unchanged is a write that would change nothing: it is not pushed.
type unchanged string

func (u unchanged) Error() string { return string(u) }

// knotArgs are a knot verb's arguments.
type knotArgs struct {
	verb                      string
	pos                       []string
	project                   string
	json, ready, force, stdin bool
	parent, owner, target     string
	typ, status, run, message string
	blockedBy, changes        []string
	ifs                       []string
	hasParent, hasMessage     bool
}

// knotFlags are the flags each verb takes, beyond --project and --json.
var knotFlags = map[string][]string{
	"new":     {"--parent", "--blocked-by", "--owner", "--target", "--body"},
	"show":    {},
	"list":    {"--type", "--status", "--owner", "--ready"},
	"set":     {"--if", "--force"},
	"link":    {"--blocked-by", "--parent", "--change", "--if"},
	"unlink":  {"--blocked-by", "--parent", "--change", "--if"},
	"edit":    {"--body", "--if"},
	"comment": {"-m", "--body"},
	"claim":   {"--run", "--if"},
	"unclaim": {"--if"},
	"schema":  {},
}

// knotPositionals is how many positional arguments each verb takes: at
// least the first, at most the second (-1 for any number).
var knotPositionals = map[string][2]int{
	"new": {2, 2}, "show": {1, 1}, "list": {0, 0}, "set": {2, -1}, "link": {1, 1}, "unlink": {1, 1},
	"edit": {1, 1}, "comment": {1, 1}, "claim": {1, 1}, "unclaim": {1, 1}, "schema": {0, 0},
}

func parseKnotArgs(args []string) (knotArgs, error) {
	var a knotArgs
	if len(args) == 0 {
		return a, failure(knotUsage)
	}
	a.verb, args = args[0], args[1:]
	allowed, ok := knotFlags[a.verb]
	if !ok {
		return a, failure(fmt.Sprintf("knot has no verb %q; its verbs are new, show, list, set, link, unlink, edit, comment, claim, unclaim and schema\n%s", a.verb, knotUsage))
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flag, inline, hasInline := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			a.pos = append(a.pos, arg)
			continue
		}
		if arg == "--" {
			a.pos = append(a.pos, args[i+1:]...)
			break
		}
		if !hasInline {
			flag = arg
		}
		if flag != "--project" && flag != "--json" && !slices.Contains(allowed, flag) {
			return a, failure(fmt.Sprintf("knot %s takes no %s; it takes %s", a.verb, flag, strings.Join(append([]string{"--project", "--json"}, allowed...), ", ")))
		}
		value := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			if i+1 >= len(args) {
				return "", failure(flag + " needs a value")
			}
			i++
			return args[i], nil
		}
		var v string
		var err error
		switch flag {
		case "--json", "--ready", "--force":
			if hasInline {
				return a, failure(flag + " takes no value")
			}
			switch flag {
			case "--json":
				a.json = true
			case "--ready":
				a.ready = true
			default:
				a.force = true
			}
			continue
		}
		if v, err = value(); err != nil {
			return a, err
		}
		switch flag {
		case "--project":
			a.project = v
		case "--parent":
			a.parent, a.hasParent = v, true
		case "--blocked-by":
			a.blockedBy = append(a.blockedBy, v)
		case "--change":
			a.changes = append(a.changes, v)
		case "--owner":
			a.owner = v
		case "--target":
			a.target = v
		case "--type":
			a.typ = v
		case "--status":
			a.status = v
		case "--run":
			a.run = v
		case "--if":
			a.ifs = append(a.ifs, v)
		case "-m":
			a.message, a.hasMessage = v, true
		case "--body":
			if v != "-" {
				return a, failure("--body takes -, and reads the body from stdin")
			}
			a.stdin = true
		}
	}
	n := knotPositionals[a.verb]
	if len(a.pos) < n[0] || (n[1] >= 0 && len(a.pos) > n[1]) {
		return a, failure(fmt.Sprintf("knot %s takes %s\n%s", a.verb, positionalsOf(a.verb), knotUsage))
	}
	return a, nil
}

func positionalsOf(verb string) string {
	switch verb {
	case "new":
		return "a type and a title"
	case "set":
		return "a knot's id and one or more <field>=<value>"
	case "list", "schema":
		return "no argument but its flags"
	}
	return "a knot's id"
}

// runKnot runs one knot verb and returns its exit status.
func runKnot(g git, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a, err := parseKnotArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", err)
		return 2
	}
	err = knotVerb(g, a, stdin, stdout)
	var c conflict
	var u unchanged
	switch {
	case err == nil:
		return 0
	case errors.As(err, &u):
		fmt.Fprintf(stderr, "valley: %s\n", u)
		return 0
	case errors.As(err, &c):
		fmt.Fprintf(stderr, "valley: conflict: %s\n", c)
		return 3
	}
	fmt.Fprintf(stderr, "valley: %s\n", err)
	return 1
}

func knotVerb(g git, a knotArgs, stdin io.Reader, stdout io.Writer) error {
	if a.verb == "schema" {
		if a.json {
			return writeKnotJSON(stdout, knot.TheSchema())
		}
		_, err := io.WriteString(stdout, knot.Describe())
		return err
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	if a.verb == "show" && knot.IsQualifiedID(a.pos[0]) {
		project, id, _ := strings.Cut(a.pos[0], ":")
		if a.project != "" && a.project != project {
			return failure(fmt.Sprintf("%s names a knot in %s, and --project names %s", a.pos[0], project, a.project))
		}
		a.project, a.pos[0] = project, id
	}
	r, err := knotRemote(g, a.project)
	if err != nil {
		return err
	}
	switch a.verb {
	case "show":
		return knotShow(r, a, stdout)
	case "list":
		return knotList(r, a, stdout)
	}
	var body string
	if a.stdin {
		data, err := io.ReadAll(io.LimitReader(stdin, knot.MaxKnotSize+1))
		if err != nil {
			return failure("could not read the body from stdin")
		}
		body = knot.NormalBody(string(data))
	}
	principal, err := g.output(true, "config", "valley.principal")
	if err != nil || !knot.IsName(principal) {
		return failure("a knot's writes name the principal your key pushes as, and git config valley.principal names none; set it with git config valley.principal <name>")
	}
	o, err := formOp(a, body, principal)
	if err != nil {
		return err
	}
	return r.write(g, o, principal, a.json, stdout)
}

// knotRemote is where a project's graph is read and written: the current
// repository's origin, or, for another project, a clone kept under the
// cache directory whose origin is the project's URL. That URL is origin's
// with its last path segment replaced by the project's name, as changes
// --all derives a project's URL from the instance's.
func knotRemote(g git, name string) (remote, error) {
	here := project(g)
	if name == "" || name == here {
		return remote{g: g, name: here}, nil
	}
	if !knot.IsName(name) {
		return remote{}, failure(fmt.Sprintf("%q is not a project's name", name))
	}
	originURL, err := g.output(true, "remote", "get-url", "origin")
	if err != nil {
		return remote{}, failure("this repository has no origin, so no other project can be found beside it")
	}
	url, err := projectURL(originURL, name)
	if err != nil {
		return remote{}, failure(err.Error())
	}
	c, err := cacheRepo(g.restricted(), url, name)
	if err != nil {
		return remote{}, failure(err.Error())
	}
	return remote{g: c, name: name}, nil
}

// remote is one project's graph on its origin, and the repository its
// objects are read and written in.
type remote struct {
	g    git
	name string
}

// knotState is a graph as read at one tip of the ref.
type knotState struct {
	tip   string // "" before the ref's first commit
	graph *knot.Graph
	blobs map[string][]byte // every file's content, by object
}

func (s knotState) read(oid string, max int) ([]byte, error) {
	data, ok := s.blobs[oid]
	if !ok {
		return nil, errors.New("names an object that was not read")
	}
	if len(data) > max {
		return nil, fmt.Errorf("is %d bytes, more than the %d it may be", len(data), max)
	}
	return data, nil
}

// read reads the graph at origin's tip of the ref: one ls-remote, a fetch
// of the tip when it is not here, one listing of its tree, and one batched
// read of every file in it.
func (r remote) read() (knotState, error) {
	tip, err := remoteRef(r.g, "origin", knot.Ref)
	if err != nil {
		return knotState{}, failure("could not read origin")
	}
	if tip == "" {
		return knotState{graph: knot.Empty(), blobs: map[string][]byte{}}, nil
	}
	if err := fetch(r.g, tip); err != nil {
		return knotState{}, err
	}
	return r.readAt(tip)
}

func (r remote) readAt(commit string) (knotState, error) {
	entries, err := r.listing(commit)
	if err != nil {
		return knotState{}, err
	}
	idx, err := knot.Layout(entries)
	if err != nil {
		return knotState{}, failure(fmt.Sprintf("the graph at %s does not read: %v", short(commit), err))
	}
	var oids []string
	for _, oid := range idx.Files {
		oids = append(oids, oid)
	}
	blobs, err := r.blobs(oids)
	if err != nil {
		return knotState{}, err
	}
	s := knotState{tip: commit, blobs: blobs}
	if s.graph, err = knot.Load(idx, s.read); err != nil {
		return knotState{}, failure(fmt.Sprintf("the graph at %s does not read: %v", short(commit), err))
	}
	return s, nil
}

// listing lists every tree and file in the commit's tree.
func (r remote) listing(treeish string) ([]knot.Entry, error) {
	out, err := r.g.output(true, "ls-tree", "-r", "-t", "-z", "--full-tree", treeish)
	if err != nil {
		return nil, failure("could not list the graph's tree at " + short(treeish))
	}
	var entries []knot.Entry
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		meta, path, ok := strings.Cut(rec, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, failure("git listed the graph's tree in a form this does not read: " + rec)
		}
		entries = append(entries, knot.Entry{Path: path, Mode: strings.TrimPrefix(fields[0], "0"), OID: fields[2]})
	}
	return entries, nil
}

// blobs reads every object named, in one cat-file --batch.
func (r remote) blobs(oids []string) (map[string][]byte, error) {
	found := map[string][]byte{}
	if len(oids) == 0 {
		return found, nil
	}
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Env, cmd.Dir = r.g.env, r.g.dir
	cmd.Stdin = strings.NewReader(strings.Join(oids, "\n") + "\n")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, failure("could not read the graph's files")
	}
	in := bufio.NewReader(out)
	for range oids {
		header, err := in.ReadString('\n')
		fields := strings.Fields(header)
		if err != nil || len(fields) != 3 {
			_ = cmd.Wait()
			return nil, failure("could not read the graph's files: git answered " + strconv.Quote(header))
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size > knot.MaxKnotSize {
			_ = cmd.Wait()
			return nil, failure(fmt.Sprintf("the graph's file %s is larger than any knot", fields[0]))
		}
		data := make([]byte, size+1)
		if _, err := io.ReadFull(in, data); err != nil {
			_ = cmd.Wait()
			return nil, failure("could not read the graph's files")
		}
		found[fields[0]] = data[:size]
	}
	if err := cmd.Wait(); err != nil {
		return nil, failure("could not read the graph's files")
	}
	return found, nil
}

// op is one write: its ID, and how it is formed on a knotState of the
// graph. form returns the files it adds or changes, and the commit's
// subject, or a conflict, or unchanged.
type op struct {
	id   string
	form func(s knotState) (files map[string][]byte, subject, id string, err error)
}

func newOpID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// formOp turns a verb's arguments into the operation it writes.
func formOp(a knotArgs, body, principal string) (op, error) {
	o := op{id: newOpID()}
	now := clock()
	guards, err := parseAssignments(a.ifs, "--if")
	if err != nil {
		return o, err
	}
	for _, g := range guards {
		if _, ok := knot.FieldNamed(g.field); !ok {
			return o, failure(fmt.Sprintf("a knot has no field %s; --if compares %s", g.field, strings.Join(fieldNames(), ", ")))
		}
	}
	// first holds the values a compare-and-set read on its first try.
	var first map[string]string
	guarded := func(s knotState, id string, fields []string, compare bool) (*knot.Knot, error) {
		k, ok := s.graph.Knots[id]
		if !ok {
			return nil, failure(fmt.Sprintf("the graph holds no knot %s", id))
		}
		for _, g := range guards {
			if got := k.Get(g.field); got != g.value {
				return nil, conflict(fmt.Sprintf("%s's %s is %q, and --if requires %q", id, g.field, got, g.value))
			}
		}
		if first == nil {
			first = map[string]string{}
			for _, f := range fields {
				first[f] = k.Get(f)
			}
		} else if compare {
			for _, f := range fields {
				if got := k.Get(f); got != first[f] {
					return nil, conflict(fmt.Sprintf("%s's %s changed from %q to %q while this was written; read it again, or pass --force to set it anyway", id, f, first[f], got))
				}
			}
		}
		return k.Clone(), nil
	}
	put := func(k *knot.Knot) map[string][]byte {
		return map[string][]byte{k.Path(): knot.Render(k)}
	}

	switch a.verb {
	case "new":
		t, ok := knot.TypeNamed(a.pos[0])
		if !ok {
			return o, failure(fmt.Sprintf("%q is no type of knot; the types are %s", a.pos[0], strings.Join(knot.TypeNames(), ", ")))
		}
		if strings.TrimSpace(a.pos[1]) == "" {
			return o, failure("a knot needs a title")
		}
		o.form = func(s knotState) (map[string][]byte, string, string, error) {
			id, slug := knot.NewID(t, a.pos[1], func(id string) bool { _, ok := s.graph.Index.Knots[id]; return ok })
			k := &knot.Knot{Type: t.Name, ID: id, Slug: slug, Status: t.Initial(), Title: a.pos[1],
				Created: now.Format("2006-01-02"), CreatedBy: principal, Parent: a.parent, Owner: a.owner,
				Target: a.target, Body: body}
			if t.Name == "outcome" || t.Name == "bug" {
				k.BlockedBy = sortedSet(a.blockedBy)
			} else if len(a.blockedBy) > 0 || a.target != "" {
				return nil, "", "", failure(fmt.Sprintf("a%s %s carries no blocked_by or target; outcomes and bugs do", article(t.Name), t.Name))
			}
			return put(k), fmt.Sprintf("knot new %s: %s", id, a.pos[1]), id, nil
		}

	case "set":
		id := a.pos[0]
		sets, err := parseAssignments(a.pos[1:], "set")
		if err != nil {
			return o, err
		}
		var fields []string
		for _, s := range sets {
			f, ok := knot.FieldNamed(s.field)
			switch {
			case !ok:
				return o, failure(fmt.Sprintf("a knot has no field %s; set changes %s", s.field, strings.Join(settable(), ", ")))
			case f.Set == "link":
				return o, failure(fmt.Sprintf("%s is an edge; change it with valley knot link or unlink", s.field))
			case f.Set == "claim":
				return o, failure(fmt.Sprintf("%s is a claim's; change it with valley knot claim or unclaim", s.field))
			case f.Set != "set":
				return o, failure(fmt.Sprintf("%s never changes once a knot is made; set changes %s", s.field, strings.Join(settable(), ", ")))
			}
			fields = append(fields, s.field)
		}
		o.form = func(s knotState) (map[string][]byte, string, string, error) {
			k, err := guarded(s, id, fields, !a.force)
			if err != nil {
				return nil, "", "", err
			}
			before := string(knot.Render(k))
			for _, a := range sets {
				setField(k, a.field, a.value)
			}
			// Finishing a knot releases its claim in the same commit.
			if !k.Active() {
				k.ClaimedBy, k.ClaimedAt = "", ""
			}
			if string(knot.Render(k)) == before {
				return nil, "", "", unchanged(fmt.Sprintf("%s already holds that; nothing to change", id))
			}
			return put(k), fmt.Sprintf("knot set %s: %s", id, strings.Join(a.pos[1:], " ")), id, nil
		}

	case "link", "unlink":
		id := a.pos[0]
		if len(a.blockedBy) == 0 && !a.hasParent && len(a.changes) == 0 {
			return o, failure(fmt.Sprintf("knot %s takes --blocked-by, --parent or --change", a.verb))
		}
		o.form = func(s knotState) (map[string][]byte, string, string, error) {
			var fields []string
			if a.hasParent {
				fields = []string{"parent"}
			}
			k, err := guarded(s, id, fields, true)
			if err != nil {
				return nil, "", "", err
			}
			before := string(knot.Render(k))
			var did []string
			if a.verb == "link" {
				if a.hasParent {
					if k.Parent != "" && k.Parent != a.parent {
						return nil, "", "", conflict(fmt.Sprintf("%s's parent is %s; unlink it first to give it another", id, k.Parent))
					}
					k.Parent = a.parent
					did = append(did, "parent "+a.parent)
				}
				if len(a.blockedBy) > 0 {
					k.BlockedBy = sortedSet(append(slices.Clone(k.BlockedBy), a.blockedBy...))
					did = append(did, "blocked by "+strings.Join(a.blockedBy, ", "))
				}
				if len(a.changes) > 0 {
					k.Changes = sortedSet(append(slices.Clone(k.Changes), a.changes...))
					did = append(did, "change "+strings.Join(a.changes, ", "))
				}
			} else {
				if a.hasParent {
					if k.Parent != a.parent {
						return nil, "", "", conflict(fmt.Sprintf("%s's parent is %q, not %s", id, k.Parent, a.parent))
					}
					k.Parent = ""
					did = append(did, "parent "+a.parent)
				}
				if len(a.blockedBy) > 0 {
					k.BlockedBy = without(k.BlockedBy, a.blockedBy)
					if k.BlockedBy == nil && (k.Type == "outcome" || k.Type == "bug") {
						k.BlockedBy = []string{}
					}
					did = append(did, "blocked by "+strings.Join(a.blockedBy, ", "))
				}
				if len(a.changes) > 0 {
					k.Changes = without(k.Changes, a.changes)
					did = append(did, "change "+strings.Join(a.changes, ", "))
				}
			}
			if string(knot.Render(k)) == before {
				return nil, "", "", unchanged(fmt.Sprintf("%s already holds that; nothing to change", id))
			}
			return put(k), fmt.Sprintf("knot %s %s: %s", a.verb, id, strings.Join(did, "; ")), id, nil
		}

	case "edit":
		id := a.pos[0]
		if !a.stdin {
			return o, failure("knot edit takes --body -, and reads the new body from stdin")
		}
		o.form = func(s knotState) (map[string][]byte, string, string, error) {
			k, err := guarded(s, id, nil, true)
			if err != nil {
				return nil, "", "", err
			}
			if first["body"] == "" {
				first["body"] = "read:" + k.Body
			} else if first["body"] != "read:"+k.Body {
				return nil, "", "", conflict(fmt.Sprintf("%s's body changed while this was written; read it again", id))
			}
			if k.Body == body {
				return nil, "", "", unchanged(fmt.Sprintf("%s's body is that already; nothing to change", id))
			}
			k.Body = body
			return put(k), fmt.Sprintf("knot edit %s", id), id, nil
		}

	case "comment":
		id := a.pos[0]
		text := body
		if a.hasMessage {
			if a.stdin {
				return o, failure("knot comment takes -m or --body -, not both")
			}
			text = knot.NormalBody(a.message)
		}
		if text == "" {
			return o, failure("knot comment takes the comment, with -m TEXT or --body -")
		}
		stamp := now.Format(knot.CommentStamp)
		o.form = func(s knotState) (map[string][]byte, string, string, error) {
			if _, err := guarded(s, id, nil, false); err != nil {
				return nil, "", "", err
			}
			path := fmt.Sprintf("%s/%s/%s-%s.md", knot.CommentsDirectory, id, stamp, o.id)
			return map[string][]byte{path: knot.RenderComment(&knot.Comment{Author: principal, Body: text})},
				fmt.Sprintf("knot comment %s", id), id, nil
		}

	case "claim":
		id := a.pos[0]
		if !knot.IsName(a.run) {
			return o, failure("knot claim takes --run RUN, the ID of the run working on the knot: letters, digits, '.', '_' and '-'")
		}
		o.form = func(s knotState) (map[string][]byte, string, string, error) {
			k, err := guarded(s, id, nil, false)
			if err != nil {
				return nil, "", "", err
			}
			switch {
			case k.Type != "outcome" && k.Type != "bug":
				return nil, "", "", failure(fmt.Sprintf("a%s %s is not claimed; outcomes and bugs are", article(k.Type), k.Type))
			case k.ClaimedBy == a.run:
				return nil, "", "", unchanged(fmt.Sprintf("%s is claimed by %s already", id, a.run))
			case k.ClaimedBy != "":
				return nil, "", "", conflict(fmt.Sprintf("%s is claimed by %s since %s; unclaim it first", id, k.ClaimedBy, k.ClaimedAt))
			case !k.Active():
				return nil, "", "", failure(fmt.Sprintf("%s is %s, and only an active knot is claimed", id, k.Status))
			}
			k.ClaimedBy, k.ClaimedAt = a.run, now.Format(time.RFC3339)
			if k.Type == "outcome" && k.Status == "open" {
				k.Status = "in-progress"
			}
			return put(k), fmt.Sprintf("knot claim %s: %s", id, a.run), id, nil
		}

	case "unclaim":
		id := a.pos[0]
		o.form = func(s knotState) (map[string][]byte, string, string, error) {
			// The claim released is the one first read. A retry that finds
			// another claim, or none, reports a conflict: the claim changed
			// meanwhile, and what the knot holds now is not this
			// operation's to release.
			k, err := guarded(s, id, []string{"claimed_by", "claimed_at"}, false)
			if err != nil {
				return nil, "", "", err
			}
			if k.ClaimedBy != first["claimed_by"] || k.ClaimedAt != first["claimed_at"] {
				return nil, "", "", conflict(fmt.Sprintf("%s's claim changed from %s to %s while this was written; read it again", id, claimOf(first["claimed_by"], first["claimed_at"]), claimOf(k.ClaimedBy, k.ClaimedAt)))
			}
			if k.ClaimedBy == "" {
				return nil, "", "", unchanged(fmt.Sprintf("%s is not claimed", id))
			}
			run := k.ClaimedBy
			k.ClaimedBy, k.ClaimedAt = "", ""
			return put(k), fmt.Sprintf("knot unclaim %s: %s", id, run), id, nil
		}
	}
	return o, nil
}

// assignment is one <field>=<value>.
type assignment struct{ field, value string }

func parseAssignments(list []string, what string) ([]assignment, error) {
	var out []assignment
	for _, s := range list {
		field, value, ok := strings.Cut(s, "=")
		if !ok || field == "" {
			return nil, failure(fmt.Sprintf("%s takes <field>=<value>, and %q is not one", what, s))
		}
		out = append(out, assignment{field, value})
	}
	return out, nil
}

// claimOf says who holds a claim and since when, or that there is none.
func claimOf(by, at string) string {
	if by == "" {
		return "none"
	}
	return by + " since " + at
}

func fieldNames() []string {
	var names []string
	for _, f := range knot.Fields {
		names = append(names, f.Name)
	}
	return names
}

func settable() []string {
	var names []string
	for _, f := range knot.Fields {
		if f.Set == "set" {
			names = append(names, f.Name)
		}
	}
	return names
}

func setField(k *knot.Knot, field, value string) {
	switch field {
	case "status":
		k.Status = value
	case "title":
		k.Title = value
	case "source":
		k.Source = value
	case "owner":
		k.Owner = value
	case "target":
		k.Target = value
	case "graduated_into":
		k.GraduatedInto = value
	}
}

func sortedSet(list []string) []string {
	out := slices.Clone(list)
	if out == nil {
		out = []string{}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func without(list, drop []string) []string {
	var out []string
	for _, x := range list {
		if !slices.Contains(drop, x) {
			out = append(out, x)
		}
	}
	return out
}

func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "n"
	}
	return ""
}

// write forms the operation on origin's tip, validates what it would
// leave by the rules the hook applies, and pushes it with a lease on that
// tip, until it lands or knot.Attempts tries have failed.
//
// A push that fails may have landed all the same: the connection can drop
// after origin took it. So after every failure but a refusal by origin's
// hook, the operation is looked for on the ref before it is formed again,
// and it is formed again only once a search has found it absent. A search
// that fails uses up a try.
func (r remote) write(g git, o op, principal string, asJSON bool, w io.Writer) error {
	search := false
	for attempt := 1; attempt <= knot.Attempts; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Duration(attempt*(50+mrand.IntN(200))) * time.Millisecond)
		}
		if search {
			found, err := r.find(o.id)
			if err != nil {
				continue
			}
			if found != "" {
				return r.reportAt(found, o, asJSON, w)
			}
			search = false
		}
		s, err := r.read()
		if err != nil {
			return err
		}
		files, subject, id, err := o.form(s)
		if err != nil {
			return err
		}
		commit, after, err := r.commit(g, s, files, knot.Message(subject, o.id), principal)
		if err != nil {
			return err
		}
		refused, err := r.push(commit, s.tip)
		if err == nil {
			return r.report(after, id, o.id, commit, subject, asJSON, w)
		}
		if refused != "" {
			return failure("origin refused the write:\n" + refused)
		}
		search = true
	}
	if found, err := r.find(o.id); err == nil && found != "" {
		return r.reportAt(found, o, asJSON, w)
	}
	return failure(fmt.Sprintf("origin did not take the write after %d tries", knot.Attempts))
}

// reportAt reports an operation found on the ref at commit, as write
// reports one it pushed. The commit's subject names the knot:
// knot <verb> <id>, and what the operation did after a colon.
func (r remote) reportAt(commit string, o op, asJSON bool, w io.Writer) error {
	s, err := r.readAt(commit)
	if err != nil {
		return err
	}
	subject, _ := r.g.output(true, "log", "-1", "--format=%s", commit)
	id := ""
	if fields := strings.Fields(subject); len(fields) >= 3 {
		id = strings.TrimSuffix(fields[2], ":")
	}
	return r.report(s, id, o.id, commit, subject, asJSON, w)
}

// commit builds the operation's commit on a private index: the tip's tree
// with the files written over it. It holds the result to every rule the
// hook applies, as the principal pushing it, and returns the commit and
// the graph it leaves.
func (r remote) commit(g git, s knotState, files map[string][]byte, message, principal string) (string, knotState, error) {
	tmp, err := os.MkdirTemp("", "valley-knot-")
	if err != nil {
		return "", knotState{}, failure("could not make a private index")
	}
	defer os.RemoveAll(tmp)
	email, err := g.output(true, "config", "user.email")
	if err != nil || email == "" {
		email = principal + "@valley.invalid"
	}
	c := r.g
	c.env = append(slices.Clone(c.env), "GIT_INDEX_FILE="+filepath.Join(tmp, "index"),
		"GIT_AUTHOR_NAME="+principal, "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+principal, "GIT_COMMITTER_EMAIL="+email)
	readTree := []string{"read-tree", "--empty"}
	if s.tip != "" {
		readTree = []string{"read-tree", s.tip}
	}
	if _, err := c.output(false, readTree...); err != nil {
		return "", knotState{}, failure("could not read the graph into a private index")
	}
	blobs := maps(s.blobs)
	var info strings.Builder
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		oid, err := c.input(false, bytes.NewReader(files[p]), "hash-object", "-w", "--stdin")
		if err != nil {
			return "", knotState{}, failure("could not write " + p)
		}
		blobs[oid] = files[p]
		fmt.Fprintf(&info, "100644 %s\t%s\n", oid, p)
	}
	if _, err := c.input(false, strings.NewReader(info.String()), "update-index", "--add", "--index-info"); err != nil {
		return "", knotState{}, failure("could not stage the write")
	}
	tree, err := c.output(false, "write-tree")
	if err != nil {
		return "", knotState{}, failure("could not write the graph's tree")
	}
	entries, err := r.listing(tree)
	if err != nil {
		return "", knotState{}, err
	}
	idx, err := knot.Layout(entries)
	if err == nil {
		after := knotState{blobs: blobs}
		err = knot.Push{Old: s.graph.Index, New: idx, Read: after.read, Pusher: principal}.Check()
	}
	if err != nil {
		return "", knotState{}, failure("the write would leave the graph invalid: " + err.Error())
	}
	args := []string{"commit-tree", tree}
	if s.tip != "" {
		args = append(args, "-p", s.tip)
	}
	commit, err := c.input(false, strings.NewReader(message), append(args, "-F", "-")...)
	if err != nil {
		return "", knotState{}, failure("could not write the commit")
	}
	after := knotState{tip: commit, blobs: blobs}
	if after.graph, err = knot.Load(idx, after.read); err != nil {
		return "", knotState{}, failure("the write would leave the graph invalid: " + err.Error())
	}
	return commit, after, nil
}

func maps(m map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// push moves the ref on origin from old to commit, leased on old: it is
// refused if the ref has moved since it was read, and a ref that did not
// exist must still not exist. refused is what origin's hook said when it
// refused the push. It is "" for a failure that may have been anything:
// a lease that failed, a connection that dropped, or a push that passed
// the hook and then lost the ref's lock to another push landing at once,
// which git reports as remote rejected too.
func (r remote) push(commit, old string) (refused string, err error) {
	cmd := exec.Command("git", "push", "--porcelain", "--quiet", "--force-with-lease="+knot.Ref+":"+old,
		"origin", commit+":"+knot.Ref)
	cmd.Env, cmd.Dir = r.g.env, r.g.dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err == nil {
		return "", nil
	} else if strings.Contains(stdout.String(), "hook declined") {
		var lines []string
		for _, line := range strings.Split(stderr.String(), "\n") {
			if said, ok := strings.CutPrefix(line, "remote: "); ok {
				lines = append(lines, said)
			}
		}
		if len(lines) == 0 {
			lines = []string{strings.TrimSpace(stdout.String())}
		}
		return strings.Join(lines, "\n"), err
	} else {
		return "", err
	}
}

// find looks for the operation among the latest knot.OpSearch commits of
// the ref on origin, and returns the commit that recorded it, or "".
func (r remote) find(op string) (string, error) {
	tip, err := remoteRef(r.g, "origin", knot.Ref)
	if err != nil || tip == "" {
		return "", err
	}
	if err := fetch(r.g, tip); err != nil {
		return "", err
	}
	out, err := r.g.output(true, "log", "--format=%x1e%H%x1f%B", "-n", strconv.Itoa(knot.OpSearch), tip)
	if err != nil {
		return "", err
	}
	for _, rec := range strings.Split(out, "\x1e") {
		commit, message, ok := strings.Cut(rec, "\x1f")
		if !ok {
			continue
		}
		if found, err := knot.OpOf(message); err == nil && found == op {
			return commit, nil
		}
	}
	return "", nil
}

// report says what a write did.
func (r remote) report(s knotState, id, op, commit, subject string, asJSON bool, w io.Writer) error {
	if asJSON {
		var j *jsonKnot
		if k, ok := s.graph.Knots[id]; ok {
			j = r.jsonKnot(s, k, true, nil)
		}
		doc := struct {
			Schema  string    `json:"schema"`
			Project string    `json:"project"`
			Op      string    `json:"op"`
			Commit  string    `json:"commit"`
			Knot    *jsonKnot `json:"knot"`
		}{"valley-knot-op/v1", r.name, op, commit, j}
		return writeKnotJSON(w, doc)
	}
	_, err := fmt.Fprintf(w, "%s (%s)\n", visible(subject), short(commit))
	return err
}

// jsonKnot is one knot as --json prints it: its fields, what the graph
// derives of it, and, for show, its body and comments.
type jsonKnot struct {
	ID            string        `json:"id"`
	Type          string        `json:"type"`
	Status        string        `json:"status"`
	Active        bool          `json:"active"`
	Title         string        `json:"title"`
	Created       string        `json:"created"`
	CreatedBy     string        `json:"created_by"`
	Source        string        `json:"source,omitempty"`
	Parent        string        `json:"parent,omitempty"`
	Owner         string        `json:"owner,omitempty"`
	BlockedBy     []string      `json:"blocked_by"`
	ClaimedBy     string        `json:"claimed_by,omitempty"`
	ClaimedAt     string        `json:"claimed_at,omitempty"`
	Changes       []string      `json:"changes"`
	Target        string        `json:"target,omitempty"`
	GraduatedInto string        `json:"graduated_into,omitempty"`
	Path          string        `json:"path"`
	Body          *string       `json:"body,omitempty"`
	Comments      []jsonComment `json:"comments,omitempty"`
	CommentCount  int           `json:"comment_count"`
	knot.View
}

type jsonComment struct {
	Author string `json:"author"`
	At     string `json:"at"`
	Op     string `json:"op"`
	Body   string `json:"body"`
}

func (r remote) jsonKnot(s knotState, k *knot.Knot, full bool, foreign knot.Foreign) *jsonKnot {
	j := &jsonKnot{ID: k.ID, Type: k.Type, Status: k.Status, Active: k.Active(), Title: k.Title,
		Created: k.Created, CreatedBy: k.CreatedBy, Source: k.Source, Parent: k.Parent, Owner: k.Owner,
		BlockedBy: nonNil(k.BlockedBy), ClaimedBy: k.ClaimedBy, ClaimedAt: k.ClaimedAt,
		Changes: nonNil(k.Changes), Target: k.Target, GraduatedInto: k.GraduatedInto, Path: k.Path(),
		CommentCount: len(s.graph.Comments[k.ID]), View: s.graph.View(k.ID, clock(), foreign)}
	if full {
		body := k.Body
		j.Body = &body
		j.Comments = []jsonComment{}
		for _, c := range s.graph.Comments[k.ID] {
			j.Comments = append(j.Comments, jsonComment{Author: c.Author, At: c.At.Format(time.RFC3339), Op: c.Op, Body: c.Body})
		}
	}
	return j
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// foreign resolves references to other projects' knots, reading each
// project's graph once.
func (r remote) foreign(g git) knot.Foreign {
	// Another project's git speaks only through its error, which the
	// reference reports as dangling; its stderr stays quiet.
	g.stderr = io.Discard
	f := &foreignGraphs{g: g, graphs: map[string]*knot.Graph{}, failed: map[string]error{}}
	return f.resolve
}

func knotShow(r remote, a knotArgs, w io.Writer) error {
	s, err := r.read()
	if err != nil {
		return err
	}
	id := a.pos[0]
	k, ok := s.graph.Knots[id]
	if !ok {
		return failure(fmt.Sprintf("the graph of %s holds no knot %s", r.name, id))
	}
	j := r.jsonKnot(s, k, true, r.foreign(r.g))
	if a.json {
		return writeKnotJSON(w, struct {
			Schema  string    `json:"schema"`
			Project string    `json:"project"`
			Tip     string    `json:"tip"`
			Knot    *jsonKnot `json:"knot"`
		}{"valley-knot/v1", r.name, s.tip, j})
	}
	var b strings.Builder
	title := func(id string) string {
		if o, ok := s.graph.Knots[id]; ok {
			return fmt.Sprintf("%s (%s) %s", id, o.Status, visible(o.Title))
		}
		return id
	}
	fmt.Fprintf(&b, "%s  %s  %s\n%s\n", k.ID, k.Type, k.Status, visible(k.Title))
	fmt.Fprintf(&b, "created %s by %s\n", k.Created, visible(k.CreatedBy))
	line := func(label, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%-11s %s\n", label+":", value)
		}
	}
	line("source", visible(k.Source))
	if k.Parent != "" {
		line("parent", title(k.Parent))
	}
	line("owner", visible(k.Owner))
	line("target", visible(k.Target))
	if j.Claim != nil {
		stale := ""
		if j.Claim.Stale {
			stale = ", stale"
		}
		line("claimed", fmt.Sprintf("by %s %s ago%s", visible(j.Claim.By), age(j.Claim.AgeSeconds), stale))
	}
	for _, ref := range k.BlockedBy {
		mark := ""
		switch {
		case slices.Contains(j.Dangling, ref):
			mark = " (dangling: its project holds no such knot)"
		case slices.Contains(j.Unresolved, ref):
			mark = " (dangling: its project could not be read)"
		case slices.Contains(j.ClearedByEnding, ref):
			mark = " (cleared by ending)"
		}
		line("blocked by", title(ref)+mark)
	}
	for _, other := range j.Blocks {
		line("blocks", title(other))
	}
	for _, other := range j.Children {
		line("child", title(other))
	}
	if j.Progress != nil {
		line("progress", fmt.Sprintf("%d of %d", j.Progress.Done, j.Progress.Of))
	}
	for _, c := range k.Changes {
		line("change", visible(c))
	}
	line("graduated", k.GraduatedInto)
	if j.Ready {
		b.WriteString("ready\n")
	}
	if k.Body != "" {
		b.WriteString("\n" + visible(k.Body))
	}
	for _, c := range s.graph.Comments[k.ID] {
		fmt.Fprintf(&b, "\n— %s, %s\n%s", visible(c.Author), c.At.Format("2006-01-02 15:04Z"), visible(c.Body))
	}
	_, err = io.WriteString(w, b.String())
	return err
}

func knotList(r remote, a knotArgs, w io.Writer) error {
	if a.typ != "" {
		if _, ok := knot.TypeNamed(a.typ); !ok {
			return failure(fmt.Sprintf("%q is no type of knot; the types are %s", a.typ, strings.Join(knot.TypeNames(), ", ")))
		}
	}
	if a.status != "" && a.status != "active" && a.status != "terminal" && !anyStatus(a.status) {
		return failure(fmt.Sprintf("%q is no status; --status takes active, terminal, or one of %s", a.status, strings.Join(allStatuses(), ", ")))
	}
	s, err := r.read()
	if err != nil {
		return err
	}
	foreign := r.foreign(r.g)
	var list []*jsonKnot
	for _, id := range s.graph.IDs() {
		k := s.graph.Knots[id]
		switch {
		case a.typ != "" && k.Type != a.typ,
			a.owner != "" && k.Owner != a.owner,
			a.status == "active" && !k.Active(),
			a.status == "terminal" && k.Active(),
			a.status != "" && a.status != "active" && a.status != "terminal" && k.Status != a.status:
			continue
		}
		j := r.jsonKnot(s, k, false, foreign)
		if a.ready && !j.Ready {
			continue
		}
		list = append(list, j)
	}
	if a.json {
		if list == nil {
			list = []*jsonKnot{}
		}
		return writeKnotJSON(w, struct {
			Schema  string      `json:"schema"`
			Project string      `json:"project"`
			Tip     string      `json:"tip"`
			Knots   []*jsonKnot `json:"knots"`
		}{"valley-knots/v1", r.name, s.tip, list})
	}
	var b strings.Builder
	for _, j := range list {
		var notes []string
		if j.Claim != nil {
			notes = append(notes, "claimed by "+visible(j.Claim.By))
		}
		if len(j.WaitingOn) > 0 {
			notes = append(notes, "waits on "+strings.Join(j.WaitingOn, ", "))
		}
		if j.Ready {
			notes = append(notes, "ready")
		}
		note := ""
		if len(notes) > 0 {
			note = "  [" + strings.Join(notes, "; ") + "]"
		}
		fmt.Fprintf(&b, "%-11s %-8s %-11s %s%s\n", j.ID, j.Type, j.Status, visible(j.Title), note)
	}
	_, err = io.WriteString(w, b.String())
	return err
}

func anyStatus(s string) bool { return slices.Contains(allStatuses(), s) }

func allStatuses() []string {
	var all []string
	for _, t := range knot.Types {
		for _, s := range t.Statuses() {
			if !slices.Contains(all, s) {
				all = append(all, s)
			}
		}
	}
	return all
}

func age(seconds int64) string {
	d := time.Duration(seconds) * time.Second
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// visible is text made safe to print to a terminal: every control
// character but newline and tab, and every character that reorders the
// text around it, is written as an escape instead. The graph refuses
// control characters, so this is a second guard rather than the first.
func visible(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)) {
			fmt.Fprintf(&b, "\\u{%04x}", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// writeKnotJSON prints one indented JSON document.
func writeKnotJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
