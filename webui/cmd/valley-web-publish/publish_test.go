package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"the-valley/note"
	"the-valley/webui/export"
)

// The tests run the publisher against a real valley: bare repositories
// that run no hook, as any git remote would, an instance repository that
// publishes its projects as valley-init writes the list, and a plan graph
// written with the real valley knot verbs. The valley CLI is bin/valley
// itself, run by bash, with valley-go on PATH; the instance's identity
// registry and each project's policy/read are read by the real cue under
// schema/identity.cue and schema/read.cue. Uploads go to a receiver over a
// real export.Store.

const (
	instanceName = "valley-host"
	destination  = "example.org"
	changeID     = "pqrstuvwxyzkpqrstuvwxyzkpqrstuvw"
)

// The publication identities of the test valley's projects, which their
// policy/read declares and the registry's grants name.
var identities = map[string]string{
	"app":    "9da6809f038df92138c131cd16bed76d",
	"site":   "273d5668702918ea7ae079de374db96b",
	"valley": "f83927d5a4ed068cdd02a8cdaa09f26d",
	"fresh":  "0d1f6c8e2b4a59736e8f1a2b3c4d5e6f",
}

// anyoneReadsAppAndSite is the scope of the registry's read grant to anyone
// in every valley newValley makes: app and site, by their identities.
var anyoneReadsAppAndSite = `{app: "` + identities["app"] + `", site: "` + identities["site"] + `"}`

// The private sentinels: a line of site's source, the body of site's one
// knot, and a comment on it. site is private in every valley newValley
// makes until a test lets anyone read it, so no export or bundle of a pass
// before that may hold any of them.
const (
	sentinelSource  = "site-private-source-7f3a91"
	sentinelBody    = "site-private-body-91c2e4"
	sentinelComment = "site-private-comment-4e8d07"
)

// testValley is a valley made for one test: an instance repository
// (valley), two projects (app and site), a clone of each, and the
// publisher's state, cache and display mapping.
type testValley struct {
	t          *testing.T
	root, srv  string
	work       map[string]string
	schemas    string
	state      string
	principals string
	// siteTitle is the title of site's one knot, which no export of a
	// private site may hold.
	siteTitle string
	knots     map[string]string
}

func needTool(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
			t.Fatalf("no %s on PATH, and VALLEY_TEST_TOOLS=required", name)
		}
		t.Skipf("no %s on PATH; the nix check provides one", name)
	}
	return path
}

// newValley makes the valley. The instance's registry grants anyone read
// of app and site at the destination, and the operator read of every
// project; app's policy/read lets anyone and the operator read it, and site
// has none. site carries the private sentinels. The instance repository's
// own policy/read lets the operator alone read it.
func newValley(t *testing.T) *testValley {
	t.Helper()
	bash := needTool(t, "bash")
	needTool(t, "git")
	needTool(t, "cue")
	needTool(t, "valley-go")
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	v := &testValley{t: t, root: root, srv: filepath.Join(root, "srv"), work: map[string]string{},
		schemas: filepath.Join(repoRoot, "schema"), state: filepath.Join(root, "state"),
		principals: filepath.Join(root, "principals.json"), knots: map[string]string{}}

	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := "#!" + bash + "\nexec " + bash + " " + filepath.Join(repoRoot, "bin", "valley") + " \"$@\"\n"
	v.write(filepath.Join(bin, "valley"), wrapper)
	if err := os.Chmod(filepath.Join(bin, "valley"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitconfig := filepath.Join(root, "gitconfig")
	v.write(gitconfig, "[user]\n\tname = valley-web-publish-test\n\temail = test@example.org\n[init]\n\tdefaultBranch = main\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "xdg-state"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("VALLEY_READ_SCHEMA", "")
	t.Setenv("VALLEY_IDENTITY_SCHEMA", "")
	v.write(v.principals, `{"operator": "operator"}`)

	for _, name := range []string{"valley", "app", "site"} {
		v.run("", "git", "init", "--quiet", "--bare", filepath.Join(v.srv, name+".git"))
		work := filepath.Join(root, "work", name)
		v.run("", "git", "init", "--quiet", work)
		v.run(work, "git", "remote", "add", "origin", filepath.Join(v.srv, name+".git"))
		v.run(work, "git", "config", "valley.principal", "operator")
		v.work[name] = work
	}
	v.write(filepath.Join(v.work["valley"], "policy", "instance", "floor.cue"), "package verification\n")
	v.grant(anyoneReadsAppAndSite)
	v.declare("valley", policy("valley", "operator"))
	projects := v.runInput(v.srv, "app\nsite\nvalley\n", "git", "-C", filepath.Join(v.srv, "valley.git"), "hash-object", "-w", "--stdin")
	v.run("", "git", "-C", filepath.Join(v.srv, "valley.git"), "update-ref", "refs/the-valley/projects", strings.TrimSpace(projects))

	for _, name := range []string{"app", "site"} {
		v.write(filepath.Join(v.work[name], "policy", "valley"), filepath.Join(v.srv, "valley.git")+"\n")
		readme := "# " + name + "\n"
		if name == "site" {
			readme += sentinelSource + "\n"
		}
		v.write(filepath.Join(v.work[name], "README.md"), readme)
		v.commit(name, name+": the first commit")
	}
	v.declare("app", policy("app", "anyone", "operator"))

	// The plan: a milestone in app with a child blocked by site's one knot,
	// a bug with a body and a comment by a principal the display mapping
	// does not name, and a change ready for approval.
	v.siteTitle = "Private work in site"
	v.knots["site"] = v.newKnot("app", "outcome", v.siteTitle, "--project", "site")
	v.runInput(v.work["app"], sentinelBody+"\n", "valley", "knot", "edit", v.knots["site"], "--body", "-", "--project", "site")
	v.valley("app", "knot", "comment", v.knots["site"], "-m", sentinelComment, "--project", "site")
	v.knots["milestone"] = v.newKnot("app", "outcome", "Publish the plan")
	v.knots["blocked"] = v.newKnot("app", "outcome", "Ship it", "--parent", v.knots["milestone"], "--blocked-by", "site:"+v.knots["site"])
	v.knots["bug"] = v.newKnot("app", "bug", "The page is slow", "--parent", v.knots["milestone"])
	v.run(v.work["app"], "git", "config", "valley.principal", "alice")
	v.valley("app", "knot", "comment", v.knots["bug"], "-m", "It takes a second to load.")
	v.run(v.work["app"], "git", "config", "valley.principal", "operator")
	v.run(v.work["app"], "git", "checkout", "--quiet", "-b", "plan-page")
	v.run(v.work["app"], "git", "commit", "--quiet", "--allow-empty", "-m", "app: the plan page")
	v.run(v.work["app"], "git", "push", "--quiet", "origin", "plan-page")
	v.valley("app", "ready", "--change", changeID, "plan-page")
	v.run(v.work["app"], "git", "checkout", "--quiet", "main")
	return v
}

func (v *testValley) write(path, body string) {
	v.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		v.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		v.t.Fatal(err)
	}
}

func (v *testValley) run(dir string, name string, args ...string) string {
	v.t.Helper()
	return v.runInput(dir, "", name, args...)
}

// runInput runs a command of the test's own. Its valley commands read
// through a cache of their own, so the publisher's starts empty.
func (v *testValley) runInput(dir, stdin string, name string, args ...string) string {
	v.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+filepath.Join(v.root, "test-cache"))
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		v.t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, stderr.String())
	}
	return string(out)
}

func (v *testValley) valley(project string, args ...string) string {
	v.t.Helper()
	return v.run(v.work[project], "valley", args...)
}

func (v *testValley) newKnot(from, kind, title string, args ...string) string {
	v.t.Helper()
	out := v.valley(from, append([]string{"knot", "new", kind, title, "--json"}, args...)...)
	var doc struct {
		Knot struct {
			ID string `json:"id"`
		} `json:"knot"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Knot.ID == "" {
		v.t.Fatalf("valley knot new printed %q", out)
	}
	return doc.Knot.ID
}

// declare writes a project's policy/read and pushes it to main. An empty
// body removes it.
func (v *testValley) declare(project, body string) {
	v.t.Helper()
	path := filepath.Join(v.work[project], "policy", "read", "read.cue")
	if body == "" {
		v.run(v.work[project], "git", "rm", "--quiet", "-r", "policy/read")
	} else {
		v.write(path, "package read\n\n"+body+"\n")
	}
	v.commit(project, project+": declare who may read it")
}

// policy is the body of a policy/read for the project, by its name and
// identity, naming the readers given.
func policy(project string, readers ...string) string {
	set := make([]string, len(readers))
	for i, r := range readers {
		set[i] = r + ": true"
	}
	return fmt.Sprintf("project: %q\nidentity: %q\nreaders: {%s}", project, identities[project], strings.Join(set, ", "))
}

// grant writes the instance's identity registry, whose read grant to anyone
// at the destination covers the projects given, and pushes it to main. An
// empty scope removes the registry. The registry's genesis entry, the
// operator, governs it and reads every project.
func (v *testValley) grant(projects string) {
	v.t.Helper()
	v.grantIn("valley", projects)
}

func (v *testValley) grantIn(instance, projects string) {
	v.t.Helper()
	if projects == "" {
		v.run(v.work[instance], "git", "rm", "--quiet", "-r", "identity")
		v.commit(instance, instance+": no registry")
		return
	}
	v.write(filepath.Join(v.work[instance], "identity", "registry.cue"), registryText(projects))
	v.commit(instance, instance+": grant read")
}

// registryText is the instance's identity registry whose read grant to
// anyone at the destination covers the projects given.
func registryText(projects string) string {
	return `package identity

boundaries: {
	registry: kind: "registry"
	web: {kind: "edge-read", destination: "` + destination + `"}
}
genesis: "operator"
principals: operator: {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"}]
	grants: {
		govern: boundary: "registry"
		read: {boundary: "web", projects: "all"}
	}
}
anyone: grants: read: {boundary: "web", projects: ` + projects + `}
`
}

func (v *testValley) commit(project, message string) {
	v.t.Helper()
	v.run(v.work[project], "git", "add", "--all")
	v.run(v.work[project], "git", "commit", "--quiet", "-m", message)
	v.run(v.work[project], "git", "push", "--quiet", "origin", "main")
}

// args are the publisher's arguments for this valley, with those given
// after them.
func (v *testValley) args(more ...string) []string {
	return append([]string{
		"--instance-repo", filepath.Join(v.srv, "valley.git"),
		"--instance", instanceName, "--destination", destination,
		"--state", v.state, "--principals", v.principals,
		"--read-schema", filepath.Join(v.schemas, "read.cue"),
		"--identity-schema", filepath.Join(v.schemas, "identity.cue"),
	}, more...)
}

// publish runs one pass of the publisher, and fails the test unless it
// succeeds.
func (v *testValley) publish(more ...string) (stdout, stderr string) {
	v.t.Helper()
	stdout, stderr, err := v.tryPublish(nil, more...)
	if err != nil {
		v.t.Fatalf("the pass failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	return stdout, stderr
}

// tryPublish runs one pass, with a change to the publisher before it
// runs.
func (v *testValley) tryPublish(adjust func(*publisher), more ...string) (string, string, error) {
	v.t.Helper()
	var stdout, stderr bytes.Buffer
	cfg, err := parseArgs(v.args(more...))
	if err != nil {
		v.t.Fatal(err)
	}
	p, err := newPublisher(cfg, &stdout, &stderr)
	if err != nil {
		v.t.Fatal(err)
	}
	if adjust != nil {
		adjust(p)
	}
	err = p.pass(context.Background())
	return stdout.String(), stderr.String(), err
}

// verifiers are the keys a receiver pins: the publisher's verifier line.
func (v *testValley) verifiers() []note.VerifierKey {
	v.t.Helper()
	data, err := os.ReadFile(filepath.Join(v.state, verifierFile))
	if err != nil {
		v.t.Fatal(err)
	}
	keys, err := note.ParseVerifierKeys(data)
	if err != nil {
		v.t.Fatal(err)
	}
	return keys
}

// out runs a pass with --out into a fresh directory, and returns the
// directory and its export, held to its manifest.
func (v *testValley) out() (string, *export.Manifest, *export.Export) {
	v.t.Helper()
	dir := filepath.Join(v.t.TempDir(), "export")
	v.publish("--out", dir)
	signed, err := os.ReadFile(filepath.Join(dir, "manifest"))
	if err != nil {
		v.t.Fatal(err)
	}
	m, err := export.VerifyManifest(bytes.NewReader(signed), export.Admission{
		Verifiers: v.verifiers(), Instance: instanceName, Destination: destination, Now: time.Now(),
	})
	if err != nil {
		v.t.Fatalf("the manifest the publisher wrote is not admitted: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, export.ExportFile))
	if err != nil {
		v.t.Fatal(err)
	}
	e, err := m.CheckExport(data)
	if err != nil {
		v.t.Fatalf("export.json does not agree with the manifest: %v", err)
	}
	return dir, m, e
}

func names(e *export.Export) []string {
	list := []string{}
	for _, p := range e.Projects {
		list = append(list, p.Name)
	}
	return list
}

func knotOf(t *testing.T, p export.Project, id string) export.Knot {
	t.Helper()
	for _, k := range p.Knots {
		if k.ID == id {
			return k
		}
	}
	t.Fatalf("%s holds no knot %s", p.Name, id)
	return export.Knot{}
}

// --out writes an export that a receiver admits: its manifest opens under
// the publisher's verifier, export.json agrees with it, and a store
// activates the directory's bodies. It holds the public project alone, as
// an anonymous reader may see it.
func TestOutIsAnExportTheStoreActivates(t *testing.T) {
	v := newValley(t)
	dir, m, e := v.out()
	if got := names(e); !reflect.DeepEqual(got, []string{"app"}) {
		t.Fatalf("the export holds %v, want app alone", got)
	}
	app := e.Projects[0]
	main := strings.TrimSpace(v.run("", "git", "-C", filepath.Join(v.srv, "app.git"), "rev-parse", "main"))
	if app.SourceTip != main {
		t.Errorf("app's source tip is %s, and its main is %s", app.SourceTip, main)
	}

	// The projection reached the export: the edge to site's knot keeps
	// the reference alone, and principals pass through the mapping.
	blocked := knotOf(t, app, v.knots["blocked"])
	if len(blocked.Edges) != 1 || blocked.Edges[0].State != export.EdgeHidden || blocked.Edges[0].Title != nil {
		t.Errorf("the edge to site's knot is %+v, want a hidden reference alone", blocked.Edges)
	}
	bug := knotOf(t, app, v.knots["bug"])
	if bug.CreatedBy != "operator" || len(bug.Comments) != 1 || bug.Comments[0].Author != export.Unmapped {
		t.Errorf("the bug's principals are %q and %+v, want operator and an unmapped commenter", bug.CreatedBy, bug.Comments)
	}
	if len(app.Changes) != 1 || app.Changes[0].ChangeID != changeID {
		t.Errorf("app's changes are %+v, want the ready change", app.Changes)
	}
	data, err := os.ReadFile(filepath.Join(dir, export.ExportFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(v.siteTitle)) {
		t.Errorf("export.json holds the title of a private project's knot")
	}

	// A store activates it, from the directory's bodies alone.
	s, err := export.OpenStore(t.TempDir(), func(err error) { t.Errorf("the store reported: %v", err) })
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.Stage(m)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.Inventory(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, need := range inv.Needs {
		path := filepath.Join(dir, "bundles", need.Name+".bundle")
		if need.Name == export.ExportFile {
			path = filepath.Join(dir, export.ExportFile)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPut, export.BodyPath(m.Sequence, need.SHA256), f)
		_, err = export.StageBody(st.Bodies(), export.Body{Name: need.Name, SHA256: need.SHA256, Size: need.Size}, 64<<20, req)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	g, err := s.Activate(context.Background(), st)
	if err != nil {
		t.Fatalf("the store did not activate the export: %v", err)
	}
	repo, _ := g.Repo("app")
	if ok, err := export.Reachable(context.Background(), repo, main, main); err != nil || !ok {
		t.Errorf("the activated repository does not serve app's main: %v %v", ok, err)
	}
}

// A project is public only when anyone may read it: its own policy/read
// names anyone, and the registry's read grant to anyone covers it by its
// identity. A missing policy, an invalid one, one that does not name anyone,
// and a grant that does not cover the project each keep it private, and
// the old policy/web is never read. The operator's grant to every project
// publishes nothing: the edge serves anyone alone.
func TestVisibilityNeedsBothConsents(t *testing.T) {
	v := newValley(t)
	steps := []struct {
		what    string
		change  func()
		public  []string
		private []string
		warns   string
	}{
		{"site has no policy/read", func() {}, []string{"app"}, []string{"site", "valley"}, ""},
		{"site lets the operator alone read it", func() { v.declare("site", policy("site", "operator")) },
			[]string{"app"}, []string{"site", "valley"}, ""},
		{"site lets no one read it", func() { v.declare("site", policy("site")) },
			[]string{"app"}, []string{"site", "valley"}, ""},
		{"site lets anyone read it", func() { v.declare("site", policy("site", "anyone")) },
			[]string{"app", "site"}, []string{"valley"}, ""},
		{"the instance's grant to anyone no longer covers site", func() {
			v.grant(`{app: "` + identities["app"] + `"}`)
		}, []string{"app"}, []string{"site", "valley"}, ""},
		{"the instance's grant to anyone covers every project", func() { v.grant(`"all"`) },
			[]string{"app", "site"}, []string{"valley"}, ""},
		{"the instance's grant to anyone covers no project", func() { v.grant(`{}`) },
			[]string{}, []string{"app", "site", "valley"}, ""},
		{"the instance's grant to anyone covers app under another identity", func() {
			v.grant(`{app: "` + identities["site"] + `", site: "` + identities["site"] + `"}`)
		}, []string{"site"}, []string{"app", "valley"}, ""},
		{"app's policy is invalid", func() {
			v.grant(anyoneReadsAppAndSite)
			v.declare("app", `project: "app"
identity: "`+identities["app"]+`"
reader: {anyone: true}`)
		}, []string{"site"}, []string{"app", "valley"}, "app: its policy/read at"},
		{"app's policy is site's", func() { v.declare("app", policy("site", "anyone")) },
			[]string{"site"}, []string{"app", "valley"}, `policy/read is for the project "site", and was read from "app"`},
		{"app declares itself public in the old policy/web alone", func() {
			v.declare("app", "")
			v.write(filepath.Join(v.work["app"], "policy", "web", "web.cue"), "package web\n\nproject: visibility: \"public\"\n")
			v.commit("app", "app: the old declaration")
		}, []string{"site"}, []string{"app", "valley"}, ""},
		{"app lets anyone read it again, and the instance drops its registry", func() {
			v.declare("app", policy("app", "anyone"))
			v.grant("")
		}, []string{}, []string{"app", "site", "valley"}, ""},
	}
	for _, step := range steps {
		step.change()
		dir := filepath.Join(t.TempDir(), "export")
		stdout, stderr := v.publish("--out", dir)
		data, err := os.ReadFile(filepath.Join(dir, export.ExportFile))
		if err != nil {
			t.Fatal(err)
		}
		e, err := export.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		if got := names(e); !reflect.DeepEqual(got, step.public) {
			t.Errorf("%s: the export holds %v, want %v", step.what, got, step.public)
		}
		list := func(names []string) string {
			if len(names) == 0 {
				return "none"
			}
			return strings.Join(names, ", ")
		}
		if want := "public: " + list(step.public) + "; private: " + list(step.private); !strings.Contains(stdout, want) {
			t.Errorf("%s: the pass did not say %q:\n%s", step.what, want, stdout)
		}
		if step.warns != "" && !strings.Contains(stderr, step.warns) {
			t.Errorf("%s: the pass did not warn %q:\n%s", step.what, step.warns, stderr)
		}
		exported := false
		for _, p := range e.Projects {
			exported = exported || p.Name == "site"
		}
		if holds := bytes.Contains(data, []byte(v.siteTitle)); holds != exported {
			t.Errorf("%s: export.json holds site's knot: %v; site is exported: %v", step.what, holds, exported)
		}
	}
}

// The registry is read at --registry-ref, the ref the identity compiler
// reads it at, which need not be main. A grant revoked on that ref alone
// is revoked, whatever main still says. An instance repository with no
// such ref has no registry, so every project is private, whatever another
// ref git would take the name for grants.
func TestTheRegistryIsReadAtItsRef(t *testing.T) {
	v := newValley(t)
	v.declare("site", policy("site", "anyone"))
	work := v.work["valley"]
	v.run(work, "git", "checkout", "--quiet", "-b", "registry")
	v.write(filepath.Join(work, "identity", "registry.cue"), registryText(`{app: "`+identities["app"]+`"}`))
	v.run(work, "git", "commit", "--quiet", "--all", "-m", "valley: site's grant to anyone is revoked")
	v.run(work, "git", "push", "--quiet", "origin", "registry")
	v.run(work, "git", "checkout", "--quiet", "main")

	for _, c := range []struct {
		what   string
		more   []string
		public string
		warns  string
	}{
		{"main", nil, "public: app, site; private: valley", ""},
		{"refs/heads/registry, where site's grant is revoked", []string{"--registry-ref", "refs/heads/registry"},
			"public: app; private: site, valley", ""},
		{"a ref the instance repository does not hold", []string{"--registry-ref", "refs/heads/none"},
			"public: none; private: app, site, valley", "the instance repository holds no refs/heads/none"},
	} {
		stdout, stderr := v.publish(append([]string{"--out", filepath.Join(t.TempDir(), "export")}, c.more...)...)
		mustSay(t, c.what, stdout, c.public)
		if c.warns != "" {
			mustSay(t, c.what, stderr, c.warns)
		}
	}

	// With refs/heads/registry gone, git rev-parse would resolve the name
	// to refs/heads/refs/heads/registry, a branch any pusher can make, here
	// granting anyone read of app and site. The registry is read at the
	// ref named exactly, so there is none, and every project is private.
	v.run(work, "git", "push", "--quiet", "origin", ":refs/heads/registry")
	v.run(work, "git", "push", "--quiet", "origin", "main:refs/heads/refs/heads/registry")
	stdout, stderr := v.publish("--out", filepath.Join(t.TempDir(), "export"), "--registry-ref", "refs/heads/registry")
	mustSay(t, "refs/heads/registry gone", stdout, "public: none; private: app, site, valley")
	mustSay(t, "refs/heads/registry gone", stderr, "the instance repository holds no refs/heads/registry")

	for _, ref := range []string{"main", "refs/heads/main~1", "refs/heads/../main", "refs/heads/registry.lock", "refs/heads/a b", "refs//main"} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), v.args("--out", t.TempDir(), "--registry-ref", ref), &stdout, &stderr); code != 2 {
			t.Errorf("--registry-ref %q: the pass exited %d, want 2", ref, code)
		}
	}
}

// The key is made on the first start and kept: a second pass signs with
// the same key, and says the verifier line only when it is new.
func TestKeyIsMadeOnceAndKept(t *testing.T) {
	v := newValley(t)
	first, _ := v.publish("--out", filepath.Join(t.TempDir(), "one"))
	key, err := os.ReadFile(filepath.Join(v.state, keyFile))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(v.state, keyFile))
	if err != nil || info.Mode().Perm() != 0o400 {
		t.Errorf("the key's mode is %v, want 0400", info.Mode().Perm())
	}
	verifiers := v.verifiers()
	if len(verifiers) != 1 || verifiers[0].Name != instanceName+"/web-export" {
		t.Fatalf("the verifier file holds %+v, want one key named %s/web-export", verifiers, instanceName)
	}
	if !strings.Contains(first, "the export key's verifier line is "+verifiers[0].String()) {
		t.Errorf("the first pass did not say the verifier line:\n%s", first)
	}

	dir := filepath.Join(t.TempDir(), "two")
	second, _ := v.publish("--out", dir)
	if strings.Contains(second, "verifier line") {
		t.Errorf("a pass with the same key said the verifier line again:\n%s", second)
	}
	again, err := os.ReadFile(filepath.Join(v.state, keyFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, again) {
		t.Errorf("the second pass replaced the key")
	}
	signed, err := os.ReadFile(filepath.Join(dir, "manifest"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := export.VerifyManifest(bytes.NewReader(signed), export.Admission{Verifiers: verifiers,
		Instance: instanceName, Destination: destination, Now: time.Now()}); err != nil {
		t.Errorf("the second pass's manifest does not open under the first pass's verifier: %v", err)
	}
}

// testReceiver is a receiver over a real export.Store: the three steps
// of the upload, each as package export defines it, with the publisher's
// verifier pinned. It records the requests it takes, and refuse can
// refuse one before it is handled.
type testReceiver struct {
	t      *testing.T
	v      *testValley
	server *httptest.Server

	mu       sync.Mutex
	store    *export.Store
	staged   map[int64]export.Staging
	requests []string
	// refuse is asked about each request, by its kind and sequence, and
	// a refusal it returns is replied instead. A status alone, with no
	// refusal, is replied as a plain error.
	refuse func(kind string, sequence int64) (*export.Error, int)
}

func newReceiver(t *testing.T, v *testValley) *testReceiver {
	rv := &testReceiver{t: t, v: v}
	rv.replaceStore()
	rv.server = httptest.NewServer(rv)
	t.Cleanup(rv.server.Close)
	return rv
}

// replaceStore gives the receiver an empty state directory, as a volume
// replaced after a loss would.
func (rv *testReceiver) replaceStore() {
	s, err := export.OpenStore(rv.t.TempDir(), func(err error) { rv.t.Errorf("the store reported: %v", err) })
	if err != nil {
		rv.t.Fatal(err)
	}
	rv.mu.Lock()
	rv.store, rv.staged = s, map[int64]export.Staging{}
	rv.mu.Unlock()
}

// taken are the requests since the last call.
func (rv *testReceiver) taken() []string {
	rv.mu.Lock()
	defer rv.mu.Unlock()
	r := rv.requests
	rv.requests = nil
	if r == nil {
		r = []string{}
	}
	return r
}

func (rv *testReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rv.mu.Lock()
	defer rv.mu.Unlock()
	reply := func(status int, doc any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if doc != nil {
			json.NewEncoder(w).Encode(doc)
		}
	}
	fail := func(err error) {
		var e *export.Error
		if errors.As(err, &e) {
			reply(e.Code.Status(), e.Reply())
			return
		}
		reply(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/web-export/v1/"), "/")
	kind, seq := "", int64(0)
	switch {
	case r.Method == http.MethodPost && r.URL.Path == export.ManifestPath:
		kind = "manifest"
	case r.Method == http.MethodPut && len(parts) == 2:
		kind = "body"
	case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "commit":
		kind = "commit"
	default:
		reply(http.StatusNotFound, nil)
		return
	}
	if kind != "manifest" {
		seq, _ = strconv.ParseInt(parts[0], 10, 64)
	}
	name := kind
	if kind == "body" {
		name = "body " + parts[1]
		if st, ok := rv.staged[seq]; ok {
			for _, b := range st.Manifest.Bodies() {
				if b.SHA256 == parts[1] {
					name = "body " + b.Name
				}
			}
		}
	}
	rv.requests = append(rv.requests, name)
	if rv.refuse != nil {
		if e, status := rv.refuse(kind, seq); e != nil {
			fail(e)
			return
		} else if status != 0 {
			reply(status, nil)
			return
		}
	}

	switch kind {
	case "manifest":
		hw, err := rv.store.HighWater()
		if err != nil {
			fail(err)
			return
		}
		m, err := export.VerifyManifest(r.Body, export.Admission{Verifiers: rv.v.verifiers(),
			Instance: instanceName, Destination: destination, HighWater: hw, Now: time.Now()})
		if err != nil {
			fail(err)
			return
		}
		st, err := rv.store.Stage(m)
		if err != nil {
			fail(err)
			return
		}
		rv.staged[m.Sequence] = st
		inv, err := rv.store.Inventory(st)
		if err != nil {
			fail(err)
			return
		}
		reply(http.StatusOK, inv)
	case "body":
		st, ok := rv.staged[seq]
		if !ok {
			fail(&export.Error{Code: export.CodeNoManifest, Message: "nothing is staged"})
			return
		}
		for _, b := range st.Manifest.Bodies() {
			if b.SHA256 == parts[1] {
				if _, err := export.StageBody(st.Bodies(), b, 64<<20, r); err != nil {
					fail(err)
					return
				}
				reply(http.StatusNoContent, nil)
				return
			}
		}
		fail(&export.Error{Code: export.CodeNotDeclared, Message: "not declared"})
	case "commit":
		st, ok := rv.staged[seq]
		if !ok {
			fail(&export.Error{Code: export.CodeNoManifest, Message: "nothing is staged"})
			return
		}
		g, err := rv.store.Activate(r.Context(), st)
		if err != nil {
			fail(err)
			return
		}
		reply(http.StatusOK, export.Committed{Schema: export.CommittedSchema, Sequence: g.Sequence})
	}
}

// active is the export the receiver serves.
func (rv *testReceiver) active() *export.Export {
	rv.t.Helper()
	rv.mu.Lock()
	defer rv.mu.Unlock()
	g, release, err := rv.store.Open()
	defer release()
	if err != nil {
		rv.t.Fatal(err)
	}
	e, err := g.Export()
	if err != nil {
		rv.t.Fatal(err)
	}
	return e
}

func sameRequests(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: the receiver took %q, want %q", what, got, want)
	}
}

// An upload is made only when the content digest changed since the
// receiver acknowledged it, or when the acknowledgement is older than
// --recheck; then the inventory shows whether the receiver still serves
// it, and a receiver that lost its state is sent everything again.
func TestUploadSkipsAnUnchangedDigest(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	upload := func(more ...string) string {
		stdout, _ := v.publish(append([]string{"--receiver", rv.server.URL}, more...)...)
		return stdout
	}

	upload()
	sameRequests(t, "the first pass", rv.taken(), "manifest", "body export.json", "body app", "commit")
	if got := names(rv.active()); !reflect.DeepEqual(got, []string{"app"}) {
		t.Fatalf("the receiver serves %v, want app", got)
	}

	if out := upload(); !strings.Contains(out, "nothing has changed since; nothing is sent") {
		t.Errorf("a pass with nothing changed did not say so:\n%s", out)
	}
	sameRequests(t, "a pass with nothing changed", rv.taken())

	// A comment changes export.json and not app's main, so the receiver
	// carries app's repository over and needs no bundle.
	v.valley("app", "knot", "comment", v.knots["milestone"], "-m", "The first page is up.")
	upload()
	sameRequests(t, "a pass after a comment", rv.taken(), "manifest", "body export.json", "commit")
	milestone := knotOf(t, rv.active().Projects[0], v.knots["milestone"])
	if len(milestone.Comments) != 1 {
		t.Errorf("the receiver serves the milestone with %d comments, want the new one", len(milestone.Comments))
	}

	// Past --recheck the publisher asks again, and stops at the inventory.
	if out := upload("--recheck", "0s"); !strings.Contains(out, "the receiver serves this export already") {
		t.Errorf("a recheck of a served export did not say so:\n%s", out)
	}
	sameRequests(t, "a recheck", rv.taken(), "manifest")

	// A receiver that lost its state is sent everything at the recheck.
	rv.replaceStore()
	upload()
	sameRequests(t, "a pass within --recheck after the receiver lost its state", rv.taken())
	upload("--recheck", "0s")
	sameRequests(t, "a recheck after the receiver lost its state", rv.taken(), "manifest", "body export.json", "body app", "commit")
	if got := names(rv.active()); !reflect.DeepEqual(got, []string{"app"}) {
		t.Errorf("the receiver serves %v after it was synchronised again, want app", got)
	}
}

// The upload retries what the receiver's reply says can succeed later, and
// stops at what cannot.
func TestUploadRetriesByTheReceiversReply(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	type refusal struct {
		kind   string
		code   export.Code
		status int
	}
	cases := []struct {
		name    string
		refuse  refusal
		fails   string
		takes   []string
		pausing bool
	}{
		{
			name:   "replayed: the clock is waited for",
			refuse: refusal{kind: "manifest", code: export.CodeReplayed},
			takes:  []string{"manifest", "manifest", "body export.json", "body app", "commit"},
		},
		{
			name:   "superseded at the commit: a new manifest, and its bodies again",
			refuse: refusal{kind: "commit", code: export.CodeSuperseded},
			takes: []string{"manifest", "body export.json", "body app", "commit",
				"manifest", "body export.json", "body app", "commit"},
			pausing: true,
		},
		{
			name:    "a failure in transit: the same manifest, and what is still needed",
			refuse:  refusal{kind: "body", status: http.StatusBadGateway},
			takes:   []string{"manifest", "body export.json", "manifest", "body export.json", "body app", "commit"},
			pausing: true,
		},
		{
			name:   "out-of-window: no retry",
			refuse: refusal{kind: "manifest", code: export.CodeOutOfWindow},
			fails:  "check this host's clock",
			takes:  []string{"manifest"},
		},
		{
			name:   "bad-signature: no retry",
			refuse: refusal{kind: "manifest", code: export.CodeBadSignature},
			fails:  "bad-signature",
			takes:  []string{"manifest"},
		},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rv.replaceStore()
			v.valley("app", "knot", "comment", v.knots["milestone"], "-m", "Comment "+strconv.Itoa(i)+".")
			var highWater int64
			refused := false
			rv.mu.Lock()
			rv.refuse = func(kind string, seq int64) (*export.Error, int) {
				if refused || kind != c.refuse.kind {
					return nil, 0
				}
				refused = true
				if c.refuse.status != 0 {
					return nil, c.refuse.status
				}
				e := &export.Error{Code: c.refuse.code, Message: "refused by the test"}
				if c.refuse.code == export.CodeReplayed {
					highWater = time.Now().Add(time.Second).UnixNano()
					e.HighWater = highWater
				}
				return e, 0
			}
			rv.mu.Unlock()
			rv.taken()
			before, _ := os.ReadFile(filepath.Join(v.state, acknowledgedFile))

			// The clock runs at real time, plus every pause the pass
			// takes, so a wait for the receiver's mark is a short one
			// and a backoff costs nothing.
			var mu sync.Mutex
			var skipped time.Duration
			var pauses []time.Duration
			stdout, stderr, err := v.tryPublish(func(p *publisher) {
				p.now = func() time.Time {
					mu.Lock()
					defer mu.Unlock()
					return time.Now().Add(skipped)
				}
				p.sleep = func(_ context.Context, d time.Duration) error {
					mu.Lock()
					defer mu.Unlock()
					skipped += d
					pauses = append(pauses, d)
					return nil
				}
			}, "--receiver", rv.server.URL)
			sameRequests(t, c.name, rv.taken(), c.takes...)
			if c.fails != "" {
				if err == nil || !strings.Contains(err.Error(), c.fails) {
					t.Fatalf("the pass ended with %v, want an error saying %q", err, c.fails)
				}
				// An upload attempted and refused is not acknowledged.
				after, _ := os.ReadFile(filepath.Join(v.state, acknowledgedFile))
				if !bytes.Equal(before, after) {
					t.Errorf("a refused upload changed the acknowledgement")
				}
				return
			}
			if err != nil {
				t.Fatalf("the pass failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}
			if got := len(knotOf(t, rv.active().Projects[0], v.knots["milestone"]).Comments); got != i+1 {
				t.Errorf("the receiver serves %d comments on the milestone, want %d", got, i+1)
			}
			if highWater != 0 {
				ack, err := (&publisher{cfg: config{state: v.state}, stderr: &bytes.Buffer{}}).readAcknowledged()
				if err != nil || ack.Sequence <= highWater {
					t.Errorf("the upload after replayed has sequence %d, not past the mark %d", ack.Sequence, highWater)
				}
			}
			if c.pausing && len(pauses) == 0 {
				t.Errorf("the pass retried without a pause")
			}
		})
	}
}
