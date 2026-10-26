package readaccess

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// The tests read consents and grants out of real commits with real git,
// under the real schemas with the real cue.

const (
	idApp  = "9da6809f038df92138c131cd16bed76d"
	idSite = "273d5668702918ea7ae079de374db96b"
	// idOther is another project's identity, under app's name.
	idOther = "f83927d5a4ed068cdd02a8cdaa09f26d"

	destination = "example.org"
	key         = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"
)

var day = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func needTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
			t.Fatalf("no %s on PATH, and VALLEY_TEST_TOOLS=required", name)
		}
		t.Skipf("no %s on PATH; the nix check provides one", name)
	}
}

// repo is a bare repository for one test, with the schemas to read it under.
type repo struct {
	t      *testing.T
	dir    string
	reader *Reader
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	needTool(t, "git")
	needTool(t, "cue")
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("HOME", root)
	r := &repo{t: t, dir: filepath.Join(root, "repo.git"), reader: &Reader{
		ReadSchema: schema(t, "read.cue"), IdentitySchema: schema(t, "identity.cue")}}
	r.git("", "init", "--quiet", "--bare", r.dir)
	return r
}

func schema(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "schema", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func (r *repo) git(stdin string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	if r.dir != "" && args[0] != "init" {
		cmd = exec.Command("git", append([]string{"-C", r.dir}, args...)...)
	}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if exit, ok := err.(*exec.ExitError); ok {
			stderr = exit.Stderr
		}
		r.t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr)
	}
	return strings.TrimSpace(string(out))
}

// tree writes a tree holding the files, by path, and returns it.
func (r *repo) tree(files map[string]string) string {
	r.t.Helper()
	dirs := map[string]map[string]string{}
	var listing []string
	for path, body := range files {
		first, rest, nested := strings.Cut(path, "/")
		if nested {
			if dirs[first] == nil {
				dirs[first] = map[string]string{}
			}
			dirs[first][rest] = body
			continue
		}
		listing = append(listing, "100644 blob "+r.git(body, "hash-object", "-w", "--stdin")+"\t"+first)
	}
	for name, sub := range dirs {
		listing = append(listing, "040000 tree "+r.tree(sub)+"\t"+name)
	}
	sort.Strings(listing)
	return r.git(strings.Join(listing, "\n")+"\n", "mktree")
}

// commit writes a commit whose tree holds the files, and returns it.
func (r *repo) commit(files map[string]string) string {
	r.t.Helper()
	return r.commitTree(r.tree(files))
}

func (r *repo) commitTree(tree string) string {
	r.t.Helper()
	return r.git("", "-c", "user.name=test", "-c", "user.email=test@example.org", "commit-tree", tree, "-m", "test")
}

// policy is a policy/read for app with the readers given.
func policy(body string) map[string]string {
	return map[string]string{"policy/read/read.cue": "package read\n\n" + body + "\n"}
}

func appPolicy(readers string) map[string]string {
	return policy(`project: "app"
identity: "` + idApp + `"
readers: {` + readers + `}`)
}

// registry is a registry with a genesis entry, root, that governs it, and an
// edge read boundary, web, at example.org, with more after it.
func registry(more string) map[string]string {
	return map[string]string{"identity/registry.cue": `package identity

boundaries: {
	registry: kind: "registry"
	push: kind:     "git-push"
	web: {kind: "edge-read", destination: "` + destination + `"}
}
genesis: "root"
principals: root: {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "` + key + `"}]
	grants: govern: boundary: "registry"
}
` + more + "\n"}
}

// reader is a principal entry holding the read grant given.
func reader(name, projects string) string {
	return `principals: "` + name + `": {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "` + key + `"}]
	grants: read: {boundary: "web", projects: ` + projects + `}
}`
}

func (r *repo) consent(files map[string]string, project string) (Consent, error) {
	r.t.Helper()
	return r.reader.Consent(context.Background(), r.dir, r.commit(files), project)
}

func (r *repo) grants(files map[string]string) (Grants, error) {
	r.t.Helper()
	return r.reader.Grants(context.Background(), r.dir, r.commit(files), "identity", destination, day)
}

func mustInvalid(t *testing.T, what string, err error) {
	t.Helper()
	var bad *InvalidError
	if !errors.As(err, &bad) {
		t.Errorf("%s: got %v, want an invalid document", what, err)
	}
}

// A principal reads a project only where both consents say so: the
// project's policy/read names it, and a read grant at the destination
// covers the project with the identity the policy declares.
func TestBothConsents(t *testing.T) {
	r := newRepo(t)
	g, err := r.grants(registry(`anyone: grants: read: {boundary: "web", projects: app: "` + idApp + `"}
` + reader("alice", `"all"`) + "\n" + reader("bob", `{app: "`+idOther+`"}`)))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		readers string
		want    []string
	}{
		{"anyone: true, alice: true, bob: true", []string{"alice", "anyone"}},
		{"alice: true", []string{"alice"}},
		{"anyone: true", []string{"anyone"}},
		{"bob: true, carol: true", nil},
	}
	for _, c := range cases {
		consent, err := r.consent(appPolicy(c.readers), "app")
		if err != nil {
			t.Fatalf("%s: %v", c.readers, err)
		}
		if got := g.Readers(consent); !reflect.DeepEqual(got, c.want) {
			t.Errorf("readers {%s}: the effective readers are %v, want %v", c.readers, got, c.want)
		}
		for _, p := range []string{Anyone, "alice", "bob", "carol"} {
			want := false
			for _, w := range c.want {
				want = want || w == p
			}
			if got := g.May(p, consent); got != want {
				t.Errorf("readers {%s}: May(%s) is %v, want %v", c.readers, p, got, want)
			}
		}
	}

	// The instance's grant to anyone alone does not let anyone read a
	// project whose policy does not name anyone, and the policy alone does
	// not either.
	consent, err := r.consent(appPolicy("anyone: true"), "app")
	if err != nil {
		t.Fatal(err)
	}
	none, err := r.grants(registry(""))
	if err != nil {
		t.Fatal(err)
	}
	if none.May(Anyone, consent) || len(none) != 0 {
		t.Errorf("a registry with no read grant lets anyone read: %v", none)
	}
}

// An empty reader set and an empty scope are valid, and let no one read.
func TestExplicitEmptySets(t *testing.T) {
	r := newRepo(t)
	consent, err := r.consent(appPolicy(""), "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(consent.Readers) != 0 {
		t.Errorf("an empty reader set reads as %v", consent.Readers)
	}
	g, err := r.grants(registry(`anyone: grants: read: {boundary: "web", projects: {}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(g[Anyone]) != 1 || g.Covers(Anyone, "app", idApp) || g.Selects(Anyone, "app") {
		t.Errorf("an empty scope reads as %+v", g)
	}

	// A policy with no reader set at all is not an empty one: it consents
	// to nothing, and says so.
	_, err = r.consent(policy(`project: "app"
identity: "`+idApp+`"`), "app")
	mustInvalid(t, "a policy with no readers field", err)
}

// anyone holds read grants alone, as a variant of its own: no entry under
// principals and no key. (That it is never genesis is the identity-schema
// check's genesis-is-anyone.) No principal name is special to the
// evaluator: operator reads by the same rules as alice.
func TestAnyoneRestrictions(t *testing.T) {
	r := newRepo(t)
	for what, more := range map[string]string{
		"anyone holding a push grant":   `anyone: grants: push: boundary: "push"`,
		"anyone holding registry":       `anyone: grants: govern: boundary: "registry"`,
		"anyone holding a key":          `anyone: {keys: [{class: "ssh-ed25519", bound: "host", public: "` + key + `"}], grants: {}}`,
		"anyone named under principals": strings.Replace(reader("anyone", `"all"`), "grants: read", "grants: push", 1),
	} {
		_, err := r.grants(registry(more))
		mustInvalid(t, what, err)
	}

	g, err := r.grants(registry(reader("operator", `"all"`) + "\n" + reader("alice", `"all"`)))
	if err != nil {
		t.Fatal(err)
	}
	for _, readers := range []string{"operator: true", "alice: true"} {
		consent, err := r.consent(appPolicy(readers), "app")
		if err != nil {
			t.Fatal(err)
		}
		if got := g.Readers(consent); len(got) != 1 {
			t.Errorf("readers {%s}: the effective readers are %v", readers, got)
		}
	}
	consent, err := r.consent(appPolicy("alice: true"), "app")
	if err != nil {
		t.Fatal(err)
	}
	if g.May("operator", consent) {
		t.Errorf("operator reads a project whose policy does not name operator, under an all-project grant")
	}
}

// A policy is for the project it names, and an explicit grant is for the
// identity it names. A policy read from another project, or a project
// whose identity is not the grant's, is not read.
func TestProjectIdentityMismatch(t *testing.T) {
	r := newRepo(t)
	_, err := r.consent(appPolicy("anyone: true"), "site")
	mustInvalid(t, "app's policy read from site", err)

	g, err := r.grants(registry(`anyone: grants: read: {boundary: "web", projects: app: "` + idOther + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	consent, err := r.consent(appPolicy("anyone: true"), "app")
	if err != nil {
		t.Fatal(err)
	}
	if g.May(Anyone, consent) {
		t.Errorf("a grant for app under another identity lets anyone read app")
	}
	if !g.Selects(Anyone, "app") || g.Selects(Anyone, "site") {
		t.Errorf("Selects does not follow the names the grant gives")
	}
}

// Read grants at another destination's boundary are not this destination's,
// and an expired principal holds none, from its expiry day on.
func TestDestinationsAndExpiry(t *testing.T) {
	r := newRepo(t)
	g, err := r.grants(registry(`boundaries: other: {kind: "edge-read", destination: "other.example.org"}
anyone: grants: read: {boundary: "other", projects: "all"}
principals: alice: {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "` + key + `"}]
	grants: read: {boundary: "web", projects: "all"}
	expires: "2026-10-27"
}`))
	if err != nil {
		t.Fatal(err)
	}
	if g.Covers(Anyone, "app", idApp) {
		t.Errorf("a grant at other.example.org covers app at %s", destination)
	}
	if !g.Covers("alice", "app", idApp) {
		t.Errorf("alice's grant does not count the day before it expires")
	}
	expired, err := r.reader.Grants(context.Background(), r.dir, r.commit(registry(`principals: alice: {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "`+key+`"}]
	grants: read: {boundary: "web", projects: "all"}
	expires: "2026-10-26"
}`)), "identity", destination, day)
	if err != nil {
		t.Fatal(err)
	}
	if expired.Covers("alice", "app", idApp) {
		t.Errorf("alice's grant counts on the day it expires")
	}
}

// The old declaration path, policy/web, is not read: a project that
// declares itself public there and has no policy/read has no consent.
func TestNoOldPolicyPath(t *testing.T) {
	r := newRepo(t)
	_, err := r.consent(map[string]string{
		"policy/web/web.cue": "package web\n\nproject: visibility: \"public\"\n",
	}, "app")
	if !errors.Is(err, ErrMissing) {
		t.Errorf("a commit with policy/web alone: got %v, want ErrMissing", err)
	}
	_, err = r.grants(map[string]string{"policy/web/web.cue": "package web\n\ninstance: projects: app: visibility: \"public\"\n"})
	if !errors.Is(err, ErrMissing) {
		t.Errorf("an instance with policy/web alone: got %v, want ErrMissing", err)
	}
}

// literalTree writes a tree with the entries given, mode, name and object,
// as git would never write one: by its bytes, unchecked.
func (r *repo) literalTree(entries [][3]string) string {
	r.t.Helper()
	var raw []byte
	for _, e := range entries {
		oid, err := hex.DecodeString(e[2])
		if err != nil {
			r.t.Fatal(err)
		}
		raw = append(append(raw, e[0]+" "+e[1]+"\x00"...), oid...)
	}
	return r.git(string(raw), "hash-object", "-t", "tree", "--literally", "-w", "--stdin")
}

// A policy/read whose tree names an entry with a directory part, or two
// entries alike, is invalid, and nothing of it is written outside the
// scratch directory the reader copies it into.
func TestMalformedTreeEntryNames(t *testing.T) {
	r := newRepo(t)
	scratch := filepath.Join(t.TempDir(), "scratch")
	t.Setenv("TMPDIR", scratch)
	if err := os.Mkdir(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	good := r.git(appPolicy("anyone: true")["policy/read/read.cue"], "hash-object", "-w", "--stdin")
	for _, names := range [][]string{
		{"../escape.cue"},
		{"read.cue", "sub/escape.cue"},
		{".."},
		{"."},
		{"read.cue", "read.cue"},
	} {
		var entries [][3]string
		for _, name := range names {
			entries = append(entries, [3]string{"100644", name, good})
		}
		read := r.literalTree(entries)
		policyTree := r.git("040000 tree "+read+"\tread\n", "mktree")
		root := r.git("040000 tree "+policyTree+"\tpolicy\n", "mktree")
		_, err := r.reader.Consent(context.Background(), r.dir, r.commitTree(root), "app")
		mustInvalid(t, names[len(names)-1], err)
	}
	// The reader copies a document into a directory it makes under
	// TMPDIR, so ../escape.cue would land in TMPDIR itself. The reader
	// removes its directory once it has read, so TMPDIR holds nothing.
	if _, err := os.Stat(filepath.Join(scratch, "escape.cue")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an entry named ../escape.cue was written outside the scratch directory")
	}
	if left, err := os.ReadDir(scratch); err != nil || len(left) != 0 {
		t.Errorf("TMPDIR holds %v after the reads (%v), want nothing", left, err)
	}
}

// A tree written literally can name one directory twice on the way to the
// document, the first that consents and the second that does not. git
// ls-tree given the path would take the first. Which is the document is
// ambiguous, so the policy or the registry is invalid, and consents to
// nothing.
func TestAmbiguousDocumentDirectory(t *testing.T) {
	r := newRepo(t)
	consents := r.tree(map[string]string{"read.cue": appPolicy("anyone: true")["policy/read/read.cue"]})
	declines := r.tree(map[string]string{"read.cue": appPolicy("")["policy/read/read.cue"]})
	under := func(name, tree string) string { return r.git("040000 tree "+tree+"\t"+name+"\n", "mktree") }

	// The consenting tree alone consents, so the cases below test the
	// ambiguity and nothing else.
	consent, err := r.reader.Consent(context.Background(), r.dir, r.commitTree(under("policy", under("read", consents))), "app")
	if err != nil || !consent.Names(Anyone) {
		t.Fatalf("the consenting tree alone reads as %+v, %v", consent, err)
	}
	for what, root := range map[string]string{
		"read named twice in policy": under("policy", r.literalTree([][3]string{
			{"40000", "read", consents}, {"40000", "read", declines}})),
		"policy named twice in the root": r.literalTree([][3]string{
			{"40000", "policy", under("read", consents)}, {"40000", "policy", under("read", declines)}}),
	} {
		_, err := r.reader.Consent(context.Background(), r.dir, r.commitTree(root), "app")
		mustInvalid(t, what, err)
	}

	registryTree := r.tree(map[string]string{"registry.cue": registry(`anyone: grants: read: {boundary: "web", projects: "all"}`)["identity/registry.cue"]})
	empty := r.tree(map[string]string{"registry.cue": registry("")["identity/registry.cue"]})
	_, err = r.reader.Grants(context.Background(), r.dir, r.commitTree(r.literalTree([][3]string{
		{"40000", "identity", registryTree}, {"40000", "identity", empty}})), "identity", destination, day)
	mustInvalid(t, "identity named twice in the root", err)
}

// A document may declare definitions of the schema's again, and cue unifies
// them with the schema's. A redeclaration can reopen a closed definition to
// fields it did not allow, so the reader holds the exported JSON to the
// schema's own fields and names: no redeclaration widens what it accepts.
func TestRedeclaredDefinitionsWidenNothing(t *testing.T) {
	r := newRepo(t)
	for what, body := range map[string]string{
		"a reader name past a redeclared #Principal": `#Principal: string
project: "app"
identity: "` + idApp + `"
readers: {"../x": true}`,
		"readers reopened to any key": `readers: [string]: _
project: "app"
identity: "` + idApp + `"
readers: {"two words": true}`,
		"a project and identity past redeclared patterns": `#ProjectName: string
#Identity: string
project: "../app"
identity: "x"
readers: {}`,
	} {
		_, err := r.consent(policy(body), "app")
		mustInvalid(t, what, err)
	}

	// Reopened in the registry, #Grant, #Anyone and #Principal take fields
	// cue then accepts: a note on a grant, and a key for anyone. A field
	// can also be spelled again in another case, which cue keeps apart from
	// the schema's and encoding/json would read as the schema's: Grants
	// beside an empty grants, and Projects beside an empty projects. The
	// reader refuses every one.
	for what, more := range map[string]string{
		"a grant reopened to a note": `#Grant: note?: string
anyone: grants: read: {boundary: "web", projects: "all", note: "widened"}`,
		"anyone reopened to keys": `#Anyone: keys?: [...]
anyone: {keys: [{class: "ssh-ed25519", bound: "host", public: "` + key + `"}], grants: read: {boundary: "web", projects: "all"}}`,
		"anyone's grants spelled Grants": `#Anyone: Grants?: _
anyone: grants: {}
anyone: Grants: read: {boundary: "web", projects: "all"}`,
		"a grant's projects spelled Projects": `#Grant: Projects?: _
anyone: grants: read: {boundary: "web", projects: {}, Projects: "all"}`,
		"a principal's grants spelled Grants": `#Principal: Grants?: _
principals: alice: {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "` + key + `"}]
	grants: {}
	Grants: read: {boundary: "web", projects: "all"}
}`,
	} {
		files := registry(more)
		dir := t.TempDir()
		file := filepath.Join(dir, "registry.cue")
		if err := os.WriteFile(file, []byte(files["identity/registry.cue"]), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("cue", "vet", "-c", r.reader.IdentitySchema, file).CombinedOutput(); err != nil {
			t.Fatalf("%s: cue refused it, so the case proves nothing of the reader: %s", what, out)
		}
		_, err := r.grants(files)
		mustInvalid(t, what, err)
	}
}

// A policy past the bounds is invalid: more documents than it may have.
func TestBounds(t *testing.T) {
	r := newRepo(t)
	files := appPolicy("anyone: true")
	for i := range policyBounds.files {
		files["policy/read/more-"+string(rune('a'+i%26))+string(rune('a'+i/26))+".cue"] = "package read\n"
	}
	_, err := r.consent(files, "app")
	mustInvalid(t, "a policy of 33 documents", err)
}
