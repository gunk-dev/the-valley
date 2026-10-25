package knot

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"testing"
	"time"
)

// tree is a graph's files in memory, each named by the SHA-1 of its
// content, as git would name it.
type tree map[string]string

func oid(content string) string {
	sum := sha1.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}

func (t tree) entries() []Entry {
	var es []Entry
	dirs := map[string]bool{}
	for p, content := range t {
		es = append(es, Entry{Path: p, Mode: "100644", OID: oid(content)})
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	for d := range dirs {
		es = append(es, Entry{Path: d, Mode: "40000"})
	}
	sort.Slice(es, func(i, j int) bool { return es[i].Path < es[j].Path })
	return es
}

func (t tree) index(tb testing.TB) *Index {
	tb.Helper()
	idx, err := Layout(t.entries())
	if err != nil {
		tb.Fatalf("layout: %v", err)
	}
	return idx
}

func (t tree) reader() Reader {
	byOID := map[string]string{}
	for _, content := range t {
		byOID[oid(content)] = content
	}
	return func(id string, max int) ([]byte, error) {
		content, ok := byOID[id]
		if !ok {
			return nil, errors.New("names an object the push did not bring")
		}
		if len(content) > max {
			return nil, fmt.Errorf("is %d bytes, more than %d", len(content), max)
		}
		return []byte(content), nil
	}
}

func (t tree) with(knots ...*Knot) tree {
	out := tree{}
	for p, c := range t {
		out[p] = c
	}
	for _, k := range knots {
		out[k.Path()] = string(Render(k))
	}
	return out
}

func newKnot(t, title string) *Knot {
	ty, _ := TypeNamed(t)
	slug := Slug(title)
	k := &Knot{Type: t, ID: DeriveID(ty, slug), Slug: slug, Status: ty.Initial(), Title: title,
		Created: "2026-10-15", CreatedBy: "operator"}
	if t == "outcome" || t == "bug" {
		k.BlockedBy = []string{}
	}
	return k
}

// ruleOf is the rule err refuses under, or "" for no refusal.
func ruleOf(err error) Rule {
	var e *Error
	if errors.As(err, &e) {
		return e.Rule
	}
	if err != nil {
		return "unruled: " + Rule(err.Error())
	}
	return ""
}

func push(old, new tree, pusher string) error {
	p := Push{New: new.indexOrNil(), Read: new.mergedReader(old), Pusher: pusher}
	if p.New == nil {
		_, err := Layout(new.entries())
		return err
	}
	if old != nil {
		p.Old = old.indexOrNil()
	}
	return p.Check()
}

func (t tree) indexOrNil() *Index {
	idx, err := Layout(t.entries())
	if err != nil {
		return nil
	}
	return idx
}

// mergedReader reads objects of both trees, as a repository holding both
// would.
func (t tree) mergedReader(old tree) Reader {
	all := tree{}
	for p, c := range old {
		all["old/"+p] = c
	}
	for p, c := range t {
		all[p] = c
	}
	return all.reader()
}

func TestTheCanonicalFormReadsBackExactly(t *testing.T) {
	k := newKnot("outcome", "Ship the \"plan\" graph <now> & then")
	k.Status, k.Parent, k.Owner, k.Source = "in-progress", "oc-1234567", "alice", "2026-10-15, a design"
	k.BlockedBy = []string{"oc-89abcde", "bd-0123456", "app:oc-7654321"}
	k.ClaimedBy, k.ClaimedAt = "run-1", "2026-10-15T00:58:00Z"
	k.Changes = []string{"https://github.com/a/b/pull/3", "valley:x/kkkkllllmmmmnnnnooooppppqqqqrrrr"}
	k.Target = "app:bd-0123456"
	k.Body = "A body.\n\n\twith a tab, and ünïcode.\n"
	data := Render(k)
	want := `---
type: outcome
id: ` + k.ID + `
status: in-progress
title: "Ship the \"plan\" graph <now> & then"
created: 2026-10-15
created_by: "operator"
source: "2026-10-15, a design"
parent: oc-1234567
owner: "alice"
blocked_by: ["app:oc-7654321", bd-0123456, oc-89abcde]
claimed_by: "run-1"
claimed_at: "2026-10-15T00:58:00Z"
changes: ["https://github.com/a/b/pull/3", "valley:x/kkkkllllmmmmnnnnooooppppqqqqrrrr"]
target: "app:bd-0123456"
---

A body.

	with a tab, and ünïcode.
`
	if string(data) != want {
		t.Fatalf("Render =\n%s\nwant\n%s", data, want)
	}
	got, err := ParseKnot(k.Path(), k.Slug, data)
	if err != nil {
		t.Fatal(err)
	}
	if string(Render(got)) != want {
		t.Errorf("a parsed knot renders as\n%s", Render(got))
	}

	// Each of these says the same thing in YAML, or nearly, and each is
	// refused, so there is one way to write the knot.
	canonical := string(data)
	for _, c := range []struct{ what, from, to string }{
		{"keys out of order", "status: in-progress\ntitle: ", "title: \"x\"\nstatus: in-progress\ntitle: "},
		{"a string written bare", `created_by: "operator"`, "created_by: operator"},
		{"a token written as a string", "status: in-progress", `status: "in-progress"`},
		{"a list out of order", `["app:oc-7654321", bd-0123456, oc-89abcde]`, `[oc-89abcde, bd-0123456, "app:oc-7654321"]`},
		{"an entry twice", `["app:oc-7654321", bd-0123456, oc-89abcde]`, `["app:oc-7654321", bd-0123456, bd-0123456, oc-89abcde]`},
		{"a local id quoted", `, bd-0123456,`, `, "bd-0123456",`},
		{"a list without spaces", `["app:oc-7654321", bd-0123456, oc-89abcde]`, `["app:oc-7654321",bd-0123456,oc-89abcde]`},
		{"a block list", "changes: [\"https://github.com/a/b/pull/3\", \"valley:x/kkkkllllmmmmnnnnooooppppqqqqrrrr\"]", "changes:\n  - \"https://github.com/a/b/pull/3\""},
		{"an escape where a character would do", `"Ship the`, `"` + "\\" + `u0053hip the`},
		{"two spaces after a key", "owner: ", "owner:  "},
		{"a space at the end of a line", "status: in-progress\n", "status: in-progress \n"},
		{"a YAML comment", "status: in-progress\n", "status: in-progress\n# a comment\n"},
		{"no blank line before the body", "---\n\nA body.", "---\nA body."},
		{"two blank lines before the body", "---\n\nA body.", "---\n\n\nA body."},
		{"a blank line at the end", "ünïcode.\n", "ünïcode.\n\n"},
		{"no newline at the end", "ünïcode.\n", "ünïcode."},
		{"a key named twice", "owner: \"alice\"\n", "owner: \"alice\"\nowner: \"alice\"\n"},
		{"an empty optional key", "owner: \"alice\"\n", "owner: \"\"\n"},
		{"an empty optional list", "target:", "changes: []\ntarget:"},
	} {
		variant := strings.Replace(canonical, c.from, c.to, 1)
		if variant == canonical {
			t.Fatalf("%s: the edit changed nothing", c.what)
		}
		_, err := ParseKnot(k.Path(), k.Slug, []byte(variant))
		if r := ruleOf(err); r != RuleCanonical && r != RuleFieldShape {
			t.Errorf("%s: rule %q, want canonical: %v", c.what, r, err)
		}
	}
}

func TestTextHoldsNoControlCharacters(t *testing.T) {
	k := newKnot("bug", "A bug")
	for _, bad := range []string{"\x1b[31m", "\r", "\x00", "\u0085", "\x7f", "￾", "\xff"} {
		k.Body = "text " + bad + " more\n"
		if _, err := ParseKnot(k.Path(), k.Slug, Render(k)); ruleOf(err) != RuleText {
			t.Errorf("a body holding %q: %v, want a refusal under %s", bad, err, RuleText)
		}
	}
	for _, good := range []string{"\t", "​", "日本"} {
		k.Body = "text " + good + " more\n"
		if _, err := ParseKnot(k.Path(), k.Slug, Render(k)); err != nil {
			t.Errorf("a body holding %q: %v", good, err)
		}
	}
	// A title is a JSON string, which can write a control character as
	// an escape: the file holds none, and the title it reads as does.
	for _, title := range []string{"two\nlines", "a \x1b[2J clear", "a \u0007 bell", "a\ttab"} {
		k.Body, k.Title = "", title
		if _, err := ParseKnot(k.Path(), k.Slug, Render(k)); ruleOf(err) != RuleText {
			t.Errorf("the title %q: %v", title, err)
		}
	}
}

func TestEdgesFormNoCycle(t *testing.T) {
	a, b, c := newKnot("outcome", "A"), newKnot("outcome", "B"), newKnot("outcome", "C")
	acyclic := tree{}.with(a, b, c)
	if err := Validate(acyclic.index(t), acyclic.reader()); err != nil {
		t.Fatal(err)
	}
	link := func(f func(a, b, c *Knot)) tree {
		a, b, c := a.Clone(), b.Clone(), c.Clone()
		f(a, b, c)
		return tree{}.with(a, b, c)
	}
	for _, x := range []struct {
		what  string
		graph tree
		rule  Rule
	}{
		{"a parent loop", link(func(a, b, c *Knot) { a.Parent, b.Parent, c.Parent = b.ID, c.ID, a.ID }), RuleCycle},
		{"a knot its own parent", link(func(a, b, c *Knot) { a.Parent = a.ID }), RuleCycle},
		{"a blocker loop", link(func(a, b, c *Knot) {
			a.BlockedBy, b.BlockedBy = []string{b.ID}, []string{c.ID, "x:oc-1234567"}
			c.BlockedBy = []string{a.ID}
		}), RuleCycle},
		{"a knot blocking itself", link(func(a, b, c *Knot) { b.BlockedBy = []string{b.ID} }), RuleCycle},
		// A milestone waiting on its own work item is no cycle: the two
		// kinds of edge are checked apart.
		{"a parent waiting on its child", link(func(a, b, c *Knot) { b.Parent = a.ID; a.BlockedBy = []string{b.ID} }), ""},
		{"a diamond", link(func(a, b, c *Knot) { a.BlockedBy = []string{b.ID, c.ID}; b.BlockedBy = []string{c.ID} }), ""},
	} {
		err := Validate(x.graph.index(t), x.graph.reader())
		if ruleOf(err) != x.rule {
			t.Errorf("%s: %v, want rule %q", x.what, err, x.rule)
		}
	}

	// A push is checked from what it changes, and that finds a cycle the
	// push closes even when the knot it changes is not on the loop's
	// first edge.
	before := link(func(a, b, c *Knot) { a.BlockedBy = []string{b.ID}; b.BlockedBy = []string{c.ID} })
	after := link(func(a, b, c *Knot) {
		a.BlockedBy = []string{b.ID}
		b.BlockedBy = []string{c.ID}
		c.BlockedBy = []string{a.ID}
	})
	if err := push(before, after, "operator"); ruleOf(err) != RuleCycle {
		t.Errorf("a push closing a loop: %v", err)
	}
}

func TestWhatAPushMayChange(t *testing.T) {
	a, b := newKnot("outcome", "A"), newKnot("idea", "B")
	comment := CommentsDirectory + "/" + a.ID + "/20261015T005800Z-0123456789abcdef.md"
	before := tree{}.with(a, b)
	before[comment] = string(RenderComment(&Comment{Author: "operator", Body: "Noted.\n"}))
	if err := push(nil, before, "operator"); err != nil {
		t.Fatalf("the create: %v", err)
	}
	if err := push(nil, before, "alice"); ruleOf(err) != RuleProvenance {
		t.Errorf("a create naming another creator: %v", err)
	}
	change := func(f func(t tree)) tree {
		next := tree{}
		for p, c := range before {
			next[p] = c
		}
		f(next)
		return next
	}
	edited := func(f func(k *Knot)) func(t tree) {
		return func(t tree) {
			k := a.Clone()
			f(k)
			delete(t, a.Path())
			t[k.Path()] = string(Render(k))
		}
	}
	for _, x := range []struct {
		what   string
		after  tree
		pusher string
		rule   Rule
	}{
		{"a status change by anyone", change(edited(func(k *Knot) { k.Status = "done" })), "alice", ""},
		{"a new knot by its creator", change(func(t tree) { k := newKnot("bug", "New"); k.CreatedBy = "alice"; t[k.Path()] = string(Render(k)) }), "alice", ""},
		{"a new knot naming another creator", change(func(t tree) { k := newKnot("bug", "New"); t[k.Path()] = string(Render(k)) }), "alice", RuleProvenance},
		{"a comment by its author", change(func(t tree) {
			t[CommentsDirectory+"/"+b.ID+"/20261016T000000Z-0123456789abcdef.md"] = string(RenderComment(&Comment{Author: "alice", Body: "Yes.\n"}))
		}), "alice", ""},
		{"a comment naming another author", change(func(t tree) {
			t[CommentsDirectory+"/"+b.ID+"/20261016T000000Z-0123456789abcdef.md"] = string(RenderComment(&Comment{Author: "operator", Body: "Yes.\n"}))
		}), "alice", RuleProvenance},
		{"a comment stamped with no time", change(func(t tree) {
			t[CommentsDirectory+"/"+b.ID+"/20261315T000000Z-0123456789abcdef.md"] = string(RenderComment(&Comment{Author: "operator", Body: "Yes.\n"}))
		}), "operator", RuleLayout},
		{"a comment on no knot", change(func(t tree) {
			t[CommentsDirectory+"/oc-0000000/20261016T000000Z-0123456789abcdef.md"] = string(RenderComment(&Comment{Author: "operator", Body: "Yes.\n"}))
		}), "operator", RuleEdgeResolves},
		{"a changed comment that records no edit", change(func(t tree) { t[comment] = string(RenderComment(&Comment{Author: "operator", Body: "Unsaid.\n"})) }), "operator", RuleProvenance},
		{"a deleted comment", change(func(t tree) { delete(t, comment) }), "operator", RuleTransition},
		{"a deleted knot", change(func(t tree) { delete(t, b.Path()) }), "operator", RuleTransition},
		{"a moved knot", change(func(t tree) {
			k := b.Clone()
			k.Slug = "b-2"
			k.ID = DeriveID(k.TypeOf(), k.Slug)
			delete(t, b.Path())
			t[k.Path()] = string(Render(k))
		}), "operator", RuleTransition},
		{"a changed created_by", change(edited(func(k *Knot) { k.CreatedBy = "alice" })), "alice", RuleTransition},
		{"a changed created date", change(edited(func(k *Knot) { k.Created = "2026-10-16" })), "operator", RuleTransition},
		{"a changed type", change(func(t tree) {
			k := a.Clone()
			k.Type, k.BlockedBy = "bug", []string{}
			t[a.Path()] = string(Render(k))
		}), "operator", RuleIDShape},
		{"a changed id", change(func(t tree) { k := a.Clone(); k.ID = "oc-0000000"; t[a.Path()] = string(Render(k)) }), "operator", RuleFilename},
		{"an edge to no knot", change(edited(func(k *Knot) { k.BlockedBy = []string{"oc-0000000"} })), "operator", RuleEdgeResolves},
		{"a parent that is an idea", change(edited(func(k *Knot) { k.Parent = b.ID })), "operator", RuleParentOutcome},
		{"a claim with no time", change(edited(func(k *Knot) { k.ClaimedBy = "run" })), "operator", RuleClaim},
	} {
		if err := push(before, x.after, x.pusher); ruleOf(err) != x.rule {
			t.Errorf("%s: %v, want rule %q", x.what, err, x.rule)
		}
	}
}

// A comment is edited in place: the edit replaces its body, keeps its
// author, and records who edited it and when. Its author edits it, and
// another principal only where the push's MayEdit allows, as the hook's
// grants do. A whole graph is held to the form of its comments alone.
func TestWhoMayEditAComment(t *testing.T) {
	a := newKnot("outcome", "A")
	a.CreatedBy = "alice"
	id := "20261025T120000Z-0123456789abcdef"
	at := CommentPath(a.ID, id)
	written := &Comment{Author: "alice", Body: "First thought.\n"}
	before := tree{}.with(a)
	before[at] = string(RenderComment(written))
	if err := (Push{New: before.index(t), Read: before.reader(), Pusher: "alice"}).Check(); err != nil {
		t.Fatalf("the create: %v", err)
	}
	with := func(c Comment) tree {
		next := tree{}.with(a)
		next[at] = string(RenderComment(&c))
		return next
	}
	edit := func(by, body string) Comment {
		return Comment{Author: "alice", Body: body, Edited: "2026-10-25T13:00:00Z", EditedBy: by}
	}
	byApprover := func(string) error { return nil }
	refusing := func(string) error { return errors.New("only an approver edits another's comment") }
	for _, x := range []struct {
		what    string
		before  tree
		after   tree
		pusher  string
		mayEdit func(string) error
		rule    Rule
	}{
		{"its author's edit", before, with(edit("alice", "Second thought.\n")), "alice", nil, ""},
		{"its author's second edit", with(edit("alice", "Second thought.\n")), with(edit("alice", "Third thought.\n")), "alice", nil, ""},
		{"another's edit, with no MayEdit", before, with(edit("operator", "Removed.\n")), "operator", nil, RuleTransition},
		{"another's edit, which MayEdit allows", before, with(edit("operator", "Removed.\n")), "operator", byApprover, ""},
		{"another's edit, which MayEdit refuses", before, with(edit("operator", "Removed.\n")), "operator", refusing, RuleTransition},
		{"an edit that records no edit", before, with(Comment{Author: "alice", Body: "Quietly.\n"}), "alice", nil, RuleProvenance},
		{"an edit naming another editor", before, with(edit("operator", "Not mine.\n")), "alice", byApprover, RuleProvenance},
		{"an edit that changes the author", before, with(Comment{Author: "operator", Body: "Mine now.\n", Edited: "2026-10-25T13:00:00Z", EditedBy: "operator"}), "operator", byApprover, RuleTransition},
		{"an edit that deletes the comment", before, tree{}.with(a), "alice", nil, RuleTransition},
		{"a new comment that records an edit", tree{}.with(a), with(edit("alice", "Never written.\n")), "alice", nil, RuleProvenance},
		{"a create whose comment records an edit", nil, with(edit("alice", "Never written.\n")), "alice", nil, RuleProvenance},
	} {
		p := Push{New: x.after.index(t), Read: x.after.mergedReader(x.before), Pusher: x.pusher, MayEdit: x.mayEdit}
		if x.before != nil {
			p.Old = x.before.index(t)
		}
		err := p.Check()
		if ruleOf(err) != x.rule {
			t.Errorf("%s: %v, want rule %q", x.what, err, x.rule)
		}
	}
	refused := Push{Old: before.index(t), New: with(edit("operator", "Removed.\n")).index(t), Pusher: "operator", MayEdit: refusing,
		Read: with(edit("operator", "Removed.\n")).mergedReader(before)}.Check()
	if refused == nil || !strings.Contains(refused.Error(), "a comment by alice, and operator is editing it: only an approver edits another's comment") {
		t.Errorf("a refused edit does not say whose comment it is, who edits it, and MayEdit's reason: %v", refused)
	}

	// A whole graph holds edited comments, and what it reads of one is
	// what the edit wrote.
	edited := with(edit("operator", "Removed.\n"))
	g, err := Load(edited.index(t), edited.reader())
	if err != nil {
		t.Fatalf("a graph with an edited comment: %v", err)
	}
	if n := g.Comments[a.ID][0]; n.ID != id || n.Author != "alice" || n.EditedBy != "operator" || n.Edited != "2026-10-25T13:00:00Z" || n.Body != "Removed.\n" {
		t.Errorf("the edited comment reads as %+v", n)
	}
}

// A comment's frontmatter is the author line, and on an edited comment the
// two edit lines after it, both or neither. A comment written before edits
// existed reads and writes back unchanged.
func TestACommentsForm(t *testing.T) {
	legacy := "---\nauthor: \"alice\"\n---\n\nNoted.\n"
	c, err := ParseComment("c", []byte(legacy))
	if err != nil || c.Edited != "" || c.EditedBy != "" || string(RenderComment(c)) != legacy {
		t.Fatalf("a comment that was never edited reads as %+v, %v", c, err)
	}
	edited := "---\nauthor: \"alice\"\nedited: \"2026-10-25T13:00:00Z\"\nedited_by: \"operator\"\n---\n\nNoted.\n"
	if c, err := ParseComment("c", []byte(edited)); err != nil || string(RenderComment(c)) != edited {
		t.Fatalf("an edited comment reads as %+v, %v", c, err)
	}
	for _, x := range []struct {
		what, front string
		rule        Rule
	}{
		{"the edit lines in the other order", "author: \"alice\"\nedited_by: \"operator\"\nedited: \"2026-10-25T13:00:00Z\"\n", RuleCanonical},
		{"the author after the edit", "edited: \"2026-10-25T13:00:00Z\"\nedited_by: \"operator\"\nauthor: \"alice\"\n", RuleCanonical},
		{"edited alone", "author: \"alice\"\nedited: \"2026-10-25T13:00:00Z\"\n", RuleProvenance},
		{"edited_by alone", "author: \"alice\"\nedited_by: \"operator\"\n", RuleProvenance},
		{"an edit time that is no UTC time", "author: \"alice\"\nedited: \"2026-10-25 13:00\"\nedited_by: \"operator\"\n", RuleFieldShape},
		{"an editor that is no name", "author: \"alice\"\nedited: \"2026-10-25T13:00:00Z\"\nedited_by: \"two words\"\n", RuleFieldShape},
		{"an edit time as a bare token", "author: \"alice\"\nedited: 2026-10-25T13:00:00Z\nedited_by: \"operator\"\n", RuleCanonical},
		{"an author named twice", "author: \"alice\"\nauthor: \"alice\"\n", RuleCanonical},
		{"no author", "edited: \"2026-10-25T13:00:00Z\"\nedited_by: \"operator\"\n", RuleCanonical},
		{"another key", "author: \"alice\"\nowner: \"alice\"\n", RuleCanonical},
	} {
		if _, err := ParseComment("c", []byte("---\n"+x.front+"---\n\nNoted.\n")); ruleOf(err) != x.rule {
			t.Errorf("%s: %v, want rule %q", x.what, err, x.rule)
		}
	}
	if !IsCommentID("20261025T120000Z-0123456789abcdef") || IsCommentID("20261025T120000Z-0123456789abcdef.md") ||
		IsCommentID("../oc-1234567/20261025T120000Z-0123456789abcdef") {
		t.Error("IsCommentID does not hold a comment's ID to <stamp>-<op>")
	}
}

// A listing is read as git writes a tree: every directory is an entry of
// its own, in the mode 40000 exactly. A tree written by hand can name a
// file ideas/<id>-<slug>.md at its root, which lists no ideas directory,
// or a directory in the mode 040000, which git reads as a tree and never
// writes.
func TestALayoutListsEveryDirectoryAsGitWritesIt(t *testing.T) {
	k := newKnot("idea", "Listed")
	knotFile := Entry{Path: k.Path(), Mode: "100644", OID: oid("knot")}
	commentDir := Entry{Path: CommentsDirectory + "/" + k.ID, Mode: "40000"}
	comment := Entry{Path: commentDir.Path + "/20261015T000000Z-0123456789abcdef.md", Mode: "100644", OID: oid("comment")}
	ideas, comments := Entry{Path: "ideas", Mode: "40000"}, Entry{Path: CommentsDirectory, Mode: "40000"}
	for _, x := range []struct {
		what string
		es   []Entry
		rule Rule
	}{
		{"every directory listed", []Entry{ideas, knotFile, comments, commentDir, comment}, ""},
		{"a knot without its directory", []Entry{knotFile}, RuleLayout},
		{"a comment without its knot's directory", []Entry{ideas, knotFile, comments, comment}, RuleLayout},
		{"a comment without comments/", []Entry{ideas, knotFile, commentDir, comment}, RuleLayout},
		{"a directory in the mode 040000", []Entry{{Path: "ideas", Mode: "040000"}, knotFile}, RuleLayout},
	} {
		if _, err := Layout(x.es); ruleOf(err) != x.rule {
			t.Errorf("%s: %v, want rule %q", x.what, err, x.rule)
		}
	}
}

// Each limit, at its edge and one past it.
func TestTheLimitsHoldAtTheirEdges(t *testing.T) {
	sized := func(n int) *Knot {
		k := newKnot("idea", "Sized")
		k.Body = "x\n"
		k.Body = strings.Repeat("x", n-len(Render(k))+1) + "\n"
		return k
	}
	for _, x := range []struct {
		what string
		k    *Knot
		rule Rule
	}{
		{"a knot of the most bytes", sized(MaxKnotSize), ""},
		{"a knot one byte larger", sized(MaxKnotSize + 1), RuleLimit},
		{"a title of the most bytes", func() *Knot { k := newKnot("idea", "T"); k.Title = strings.Repeat("t", MaxTitle); return k }(), ""},
		{"a title one byte longer", func() *Knot { k := newKnot("idea", "T"); k.Title = strings.Repeat("t", MaxTitle+1); return k }(), RuleLimit},
		{"a name of the most bytes", func() *Knot { k := newKnot("idea", "T"); k.Owner = strings.Repeat("o", MaxName); return k }(), ""},
		{"a name one byte longer", func() *Knot { k := newKnot("idea", "T"); k.Owner = strings.Repeat("o", MaxName+1); return k }(), RuleLimit},
		{"a list of the most entries", func() *Knot { k := newKnot("bug", "T"); k.BlockedBy = refs(MaxListItems); return k }(), ""},
		{"a list one entry longer", func() *Knot { k := newKnot("bug", "T"); k.BlockedBy = refs(MaxListItems + 1); return k }(), RuleLimit},
	} {
		if len(Render(x.k)) > MaxKnotSize+1 {
			t.Fatalf("%s: the knot is %d bytes", x.what, len(Render(x.k)))
		}
		_, err := ParseKnot(x.k.Path(), x.k.Slug, Render(x.k))
		if ruleOf(err) != x.rule {
			t.Errorf("%s: %v, want rule %q", x.what, err, x.rule)
		}
	}
	if n := len(Render(sized(MaxKnotSize))); n != MaxKnotSize {
		t.Fatalf("the largest knot is %d bytes", n)
	}

	c := &Comment{Author: "operator", Body: "x\n"}
	c.Body = strings.Repeat("x", MaxCommentSize-len(RenderComment(c))+1) + "\n"
	if _, err := ParseComment("c", RenderComment(c)); err != nil || len(RenderComment(c)) != MaxCommentSize {
		t.Errorf("a comment of the most bytes: %v", err)
	}
	c.Body = "x" + c.Body
	if _, err := ParseComment("c", RenderComment(c)); ruleOf(err) != RuleLimit {
		t.Errorf("a comment one byte larger: %v", err)
	}

	// The counts are read from the listing alone.
	listing := func(knots, comments int) []Entry {
		es := []Entry{{Path: "ideas", Mode: "40000"}, {Path: "comments", Mode: "40000"}}
		var first string
		for i := range knots {
			slug := fmt.Sprintf("k%d", i)
			id := DeriveID(Types[2], slug)
			first = id
			es = append(es, Entry{Path: "ideas/" + id + "-" + slug + ".md", Mode: "100644", OID: id})
		}
		es = append(es, Entry{Path: "comments/" + first, Mode: "40000"})
		for i := range comments {
			es = append(es, Entry{Path: fmt.Sprintf("comments/%s/20261015T000000Z-%016x.md", first, i), Mode: "100644", OID: "c"})
		}
		return es
	}
	for _, x := range []struct {
		knots, comments int
		rule            Rule
	}{{MaxKnots, MaxComments, ""}, {MaxKnots + 1, 1, RuleLimit}, {1, MaxComments + 1, RuleLimit}} {
		if _, err := Layout(listing(x.knots, x.comments)); ruleOf(err) != x.rule {
			t.Errorf("%d knots and %d comments: %v, want rule %q", x.knots, x.comments, err, x.rule)
		}
	}

	// A slug of the most bytes, and one byte longer.
	for _, x := range []struct {
		n    int
		rule Rule
	}{{MaxSlug, ""}, {MaxSlug + 1, RuleLimit}} {
		slug := strings.Repeat("s", x.n)
		id := DeriveID(Types[2], slug)
		_, err := Layout([]Entry{{Path: "ideas", Mode: "40000"}, {Path: "ideas/" + id + "-" + slug + ".md", Mode: "100644"}})
		if r := ruleOf(err); (x.rule == "") != (r == "") {
			t.Errorf("a slug of %d bytes: %v", x.n, err)
		}
	}

	// The paths one push may add or change.
	before := tree{}.with(newKnot("outcome", "Base"))
	grow := func(n int) tree {
		after := tree{}.with(newKnot("outcome", "Base"))
		for i := range n {
			after = after.with(newKnot("idea", fmt.Sprintf("Idea %d", i)))
		}
		return after
	}
	if err := push(before, grow(MaxChangedPaths), "operator"); err != nil {
		t.Errorf("a push of %d files: %v", MaxChangedPaths, err)
	}
	if err := push(before, grow(MaxChangedPaths+1), "operator"); ruleOf(err) != RuleLimit {
		t.Errorf("a push of %d files: %v", MaxChangedPaths+1, err)
	}
}

func refs(n int) []string {
	var list []string
	for i := range n {
		list = append(list, fmt.Sprintf("app:oc-%07x", i))
	}
	return list
}

func TestANewIDTakesASuffixOnACollision(t *testing.T) {
	ty, _ := TypeNamed("outcome")
	taken := map[string]bool{}
	var slugs []string
	for range 3 {
		id, slug := NewID(ty, "Ship it", func(id string) bool { return taken[id] })
		taken[id] = true
		slugs = append(slugs, slug)
		if id != DeriveID(ty, slug) {
			t.Errorf("%s is not derived from %s", id, slug)
		}
	}
	if strings.Join(slugs, " ") != "ship-it ship-it-2 ship-it-3" {
		t.Errorf("slugs = %v", slugs)
	}
	long := strings.Repeat("word ", 20)
	_, slug := NewID(ty, long, func(id string) bool { return id == DeriveID(ty, Slug(long)) })
	if len(slug) > MaxSlug || !strings.HasSuffix(slug, "-2") || !slugShape.MatchString(slug) {
		t.Errorf("a long title's suffixed slug is %q", slug)
	}
	if s := Slug("¿Qué? — 日本"); s != "qu" {
		t.Errorf("Slug = %q", s)
	}
	if s := Slug("日本"); s != "knot" {
		t.Errorf("Slug of a title with no ASCII letter = %q", s)
	}
}

func TestACommitNamesItsOperationOnce(t *testing.T) {
	tree := strings.Repeat("a", 40)
	head := "tree " + tree + "\nparent " + strings.Repeat("b", 40) + "\nauthor p <p> 1 +0000\ncommitter operator <p@x> 1 +0000\n\n"
	c, err := ParseCommit([]byte(head+Message("knot new oc-1234567: x", "0123456789abcdef")), 20)
	if err != nil || c.Op != "0123456789abcdef" || c.Committer != "operator" || len(c.Parents) != 1 || !c.Time.Equal(time.Unix(1, 0)) {
		t.Fatalf("ParseCommit = %+v, %v", c, err)
	}
	zoned := strings.Replace(head, "committer operator <p@x> 1 +0000", "committer operator <p@x> 1761397200 -0700", 1)
	if c, err := ParseCommit([]byte(zoned+Message("x", "0123456789abcdef")), 20); err != nil || c.Time != time.Date(2025, 10, 25, 13, 0, 0, 0, time.UTC) {
		t.Errorf("a commit's time in another zone reads as %v, %v", c.Time, err)
	}
	for _, line := range []string{"committer operator <p@x>", "committer operator <p@x> soon +0000", "committer operator <p@x> 1 UTC"} {
		if _, err := ParseCommit([]byte(strings.Replace(head, "committer operator <p@x> 1 +0000", line, 1)+Message("x", "0123456789abcdef")), 20); err == nil {
			t.Errorf("a committer line %q was read", line)
		}
	}
	for _, bad := range []string{
		"knot new\n",
		"knot new\n\nValley-Op: 0123\n",
		"knot new\n\nValley-Op: 0123456789abcdef\nmore\n",
		"Valley-Op: fedcba9876543210\n\nValley-Op: 0123456789abcdef\n",
	} {
		if _, err := ParseCommit([]byte(head+bad), 20); err == nil {
			t.Errorf("a message %q was read", bad)
		}
	}
	if _, err := ParseCommit([]byte("tree "+tree+"\ncommitter a <a> 1 +0000\nparent "+strings.Repeat("b", 40)+"\n\n"+Message("x", "0123456789abcdef")), 20); err == nil {
		t.Error("a parent after the other headers was read")
	}
}
