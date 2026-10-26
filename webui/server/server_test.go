package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"the-valley/knot"
	"the-valley/webui/export"
	"the-valley/webui/export/exporttest"
)

// harness is a server over a real store that the fixture's export was
// uploaded to, with a clock the test sets.
type harness struct {
	t     *testing.T
	f     *exporttest.Fixture
	store *export.Store
	srv   *Server
	now   time.Time
}

// newHarness serves the fixture's export, two minutes after it was
// collected.
func newHarness(t *testing.T) *harness {
	t.Helper()
	f := exporttest.New(t)
	store, err := export.OpenStore(t.TempDir(), func(err error) { t.Errorf("the store reported: %v", err) })
	if err != nil {
		t.Fatal(err)
	}
	f.Upload(t, store)
	h := &harness{t: t, f: f, store: store, now: exporttest.Collected.Add(2 * time.Minute)}
	h.srv = New(Config{Store: store, Now: func() time.Time { return h.now },
		Log: func(err error) { t.Errorf("the server logged: %v", err) }})
	return h
}

func (h *harness) do(method, target string) *httptest.ResponseRecorder {
	h.t.Helper()
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

func (h *harness) get(target string) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.do("GET", target)
}

// page is the body of a GET that must answer 200.
func (h *harness) page(target string) string {
	h.t.Helper()
	w := h.get(target)
	if w.Code != http.StatusOK {
		h.t.Fatalf("GET %s answered %d:\n%s", target, w.Code, w.Body)
	}
	return w.Body.String()
}

// tip is the fixture's published source tip of app.
func (h *harness) tip() string { return h.f.Export.Projects[0].SourceTip }

// publish activates a new generation, signed at: the fixture's export
// changed by change, with the bundles given, by project, in place of the
// fixture's.
func (h *harness) publish(at time.Time, bundles map[string]string, change func(*export.Export)) export.Generation {
	t := h.t
	t.Helper()
	e, err := export.Decode(h.f.ExportJSON)
	if err != nil {
		t.Fatal(err)
	}
	change(e)
	data, err := e.Encode()
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, p := range e.Projects {
		if paths[p.Name] = bundles[p.Name]; paths[p.Name] == "" {
			paths[p.Name] = h.f.Bundles[p.Name]
		}
	}
	bodies := map[string]export.Body{}
	for name, path := range paths {
		if bodies[name], err = export.FileBody(name, path); err != nil {
			t.Fatal(err)
		}
	}
	m, err := export.NewManifest(exporttest.Instance, exporttest.Destination, at.UnixNano(), e, data, bodies)
	if err != nil {
		t.Fatal(err)
	}
	hw, err := h.store.HighWater()
	if err != nil {
		t.Fatal(err)
	}
	a := h.f.Admission()
	a.Now, a.HighWater = at.Add(time.Second), hw
	if m, err = export.VerifyManifest(bytes.NewReader(h.f.Sign(t, m)), a); err != nil {
		t.Fatal(err)
	}
	st, err := h.store.Stage(m)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := h.store.Inventory(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, need := range inv.Needs {
		body := data
		if need.Name != export.ExportFile {
			if body, err = os.ReadFile(paths[need.Name]); err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest("PUT", export.BodyPath(m.Sequence, need.SHA256), bytes.NewReader(body))
		if _, err := export.StageBody(st.Bodies(), export.Body{Name: need.Name, SHA256: need.SHA256, Size: need.Size}, 64<<20, req); err != nil {
			t.Fatal(err)
		}
	}
	g, err := h.store.Activate(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// active is the generation the store serves.
func (h *harness) active() export.Generation {
	h.t.Helper()
	g, release, err := h.store.Open()
	if err != nil {
		h.t.Fatal(err)
	}
	release()
	return g
}

// git runs git on the repository at repo, with no configuration of the
// caller's and a fixed identity for the commits it writes.
func git(t *testing.T, repo string, stdin []byte, args ...string) string {
	t.Helper()
	cmd := exec.Command(export.GitProgram, append([]string{"--git-dir=" + repo}, args...)...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Example Author", "GIT_AUTHOR_EMAIL=author@example.org", "GIT_AUTHOR_DATE=1791190800 +0000",
		"GIT_COMMITTER_NAME=Example Author", "GIT_COMMITTER_EMAIL=author@example.org", "GIT_COMMITTER_DATE=1791190800 +0000")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// section is the part of a page from one marker up to the next.
func section(t *testing.T, page, from, to string) string {
	t.Helper()
	i := strings.Index(page, from)
	j := strings.Index(page[max(i, 0):], to)
	if i < 0 || j < 0 {
		t.Fatalf("the page has no section from %q to %q", from, to)
	}
	return page[i : i+j]
}

// The plan page keeps the template's reading order, says what free to
// start means, shows the ready changes as one card in no order, and
// shows how old the export is.
func TestPlanPage(t *testing.T) {
	h := newHarness(t)
	page := h.page("/")

	at := -1
	for _, heading := range []string{">Waiting on you<", ">In flight<", ">Paths<", ">Milestones<"} {
		i := strings.Index(page, heading)
		if i <= at {
			t.Fatalf("%s is not where the reading order puts it", heading)
		}
		at = i
	}
	for _, want := range []string{
		"free to start: an open, unclaimed, unblocked outcome or bug",
		"Nothing here waits on you.",
		"Ready changes — order not assessed",
		`<span class="handle">kmno</span>`, "plan: list the milestones", "app v2", "approved · integrator: stale",
		"Collected 2026-10-25 12:00 UTC, 2 minutes ago",
		"claimed by agent 2 hours ago",
		"in progress, unclaimed",
		"Readers browse the app&#39;s plan", "1 of 5 done",
		"Unfiled", "Render Markdown in a later slice",
		`<span class="chip blocked">waits on oc-a000002, a knot in an unpublished project</span>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the plan page does not show %q", want)
		}
	}
	if strings.Count(page, "Ready changes") != 1 {
		t.Error("the ready changes are not one card")
	}

	// The bug nobody claims and nothing blocks is free to start.
	if !strings.Contains(page, `<i class="k ready" role="img" aria-label="free to start" title="free to start"></i><span class="body"><a class="id" href="/app/knots/bd-a000004">`) {
		t.Error("bd-a000004 is not shown as free to start")
	}

	// The milestone's path puts oc-a000003 after the knot it waits on.
	paths := section(t, page, `id="paths-h"`, `id="ms-h"`)
	second := strings.Index(paths, `<span class="stage-num">2</span>`)
	if second < 0 || strings.Index(paths, "oc-a000002") > second || strings.Index(paths, "oc-a000003") < second {
		t.Errorf("the path does not order oc-a000003 after oc-a000002:\n%s", paths)
	}

	// Nothing of the hidden project is linked or named, and nothing is
	// fetched from another origin.
	for _, want := range []string{"lands in an unpublished project", "lands in a knot in an unpublished project"} {
		if !strings.Contains(page, want) {
			t.Errorf("the plan page does not show %q", want)
		}
	}
	for _, unwanted := range append([]string{"/site/knots/", "/site/source/", "http://", "https://", `class="chip stale"`}, unpublishedText...) {
		if strings.Contains(page, unwanted) {
			t.Errorf("the plan page holds %q", unwanted)
		}
	}

	h.now = exporttest.Collected.Add(6 * time.Minute)
	page = h.page("/")
	if !strings.Contains(page, `Collected 2026-10-25 12:00 UTC, 6 minutes ago <span class="chip stale">stale</span>`) {
		t.Error("an export six minutes old is not marked stale")
	}
}

// A blocked knot's chip names the published knots it waits on, and says
// once that it also waits on unpublished work, naming nothing of it:
// oc-a000003 waits on oc-a000002 and on a knot of the hidden site, which
// the export leaves out of waiting_on and marks with waiting_on_hidden.
// A knot waiting on unpublished work alone says only that. An export
// without the field, as v1 exports before it, is read, and its chip
// names what waiting_on holds.
func TestWaitsOnChip(t *testing.T) {
	h := newHarness(t)
	chip := func(text string) string { return `<span class="chip blocked">waits on ` + text + `</span>` }
	cases := []struct {
		name   string
		change func(*export.Knot)
		want   string
	}{
		{"a published and an unpublished blocker", func(*export.Knot) {}, "oc-a000002, a knot in an unpublished project"},
		{"two published blockers and an unpublished one", func(k *export.Knot) { k.WaitingOn = append(k.WaitingOn, "oc-a000006") },
			"oc-a000002, oc-a000006, a knot in an unpublished project"},
		{"an unpublished blocker alone", func(k *export.Knot) { k.WaitingOn = []string{} }, "a knot in an unpublished project"},
		{"an export without waiting_on_hidden", func(k *export.Knot) { k.WaitingOnHidden = false }, "oc-a000002"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h.publish(exporttest.SignedAt.Add(time.Duration(i+1)*time.Minute), nil, func(e *export.Export) {
				for j := range e.Projects[0].Knots {
					if k := &e.Projects[0].Knots[j]; k.ID == "oc-a000003" {
						if !k.WaitingOnHidden {
							t.Fatal("the fixture's oc-a000003 does not carry waiting_on_hidden")
						}
						c.change(k)
					}
				}
			})
			page := h.page("/")
			if !strings.Contains(page, chip(c.want)) {
				t.Errorf("the plan page does not show %q:\n%s", chip(c.want), section(t, page, `id="ms-h"`, "</body>"))
			}
			if n := strings.Count(page, `<span class="chip blocked">`); n != 1 {
				t.Errorf("the plan page shows %d blocked chips", n)
			}
			for _, unwanted := range unpublishedText {
				if strings.Contains(page, unwanted) {
					t.Errorf("the plan page holds %q", unwanted)
				}
			}
		})
	}
}

// unpublishedText is what of the hidden project site the fixture's
// references hold: its name and its knots' IDs. No page shows any of it.
var unpublishedText = []string{"site:", "oc-b000001", "oc-b000002", "lands in site", ">site<", "valley:site"}

// A knot's page shows its body and comments, and marks the edited
// comment. A reference into a hidden project is shown as unpublished,
// with nothing of its text, and a missing knot of a public project is
// named as missing.
func TestKnotPage(t *testing.T) {
	h := newHarness(t)
	page := h.page("/app/knots/oc-a000002")
	for _, want := range []string{
		"Each milestone is listed with its progress.",
		"Start with the milestones that have open children.",
		"Claimed; the change is ready for approval.",
		`<span class="chip quiet">edited</span><span>2026-10-25 10:30 UTC by agent</span>`,
		`<a href="/app/knots/oc-a000001">Readers browse the app&#39;s plan</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the knot's page does not show %q", want)
		}
	}
	if n := strings.Count(page, ">edited<"); n != 1 {
		t.Errorf("%d comments are marked edited; one was edited", n)
	}

	page = h.page("/app/knots/oc-a000003")
	if !strings.Contains(page, `<a href="/app/knots/oc-a000002">The plan page lists milestones</a>`) {
		t.Error("the local blocker is not linked")
	}
	for _, want := range []string{
		`<span class="nochild"></span><span class="t"><span class="muted">a knot in an unpublished project</span></span>`,
		"<dt>Lands in</dt><dd>an unpublished project</dd>",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the knot's page does not show %q", want)
		}
	}
	page += h.page("/app/knots/bd-a000004")
	if !strings.Contains(page, "<dt>Lands in</dt><dd>a knot in an unpublished project</dd>") {
		t.Error("a target in the hidden project is not shown as unpublished")
	}
	for _, unwanted := range append([]string{"/site/knots/", "/site/source/"}, unpublishedText...) {
		if strings.Contains(page, unwanted) {
			t.Errorf("the knot's page shows %q", unwanted)
		}
	}
	if w := h.get("/app/knots/oc-fffffff"); w.Code != http.StatusNotFound {
		t.Errorf("a missing knot answered %d", w.Code)
	}

	// A knot of a public project the graph does not hold, and a local
	// knot that is not there, are named as missing. A change of the
	// hidden project is not named.
	h.publish(exporttest.SignedAt.Add(time.Minute), nil, func(e *export.Export) {
		for i := range e.Projects[0].Knots {
			if k := &e.Projects[0].Knots[i]; k.ID == "oc-a000003" {
				k.BlockedBy = append(k.BlockedBy, "app:oc-fffffff", "oc-eeeeeee")
				k.Edges = append(k.Edges, export.Edge{Field: "blocked_by", Ref: "app:oc-fffffff", Project: "app", ID: "oc-fffffff", State: export.EdgeDangling})
				k.Changes = append(k.Changes, "valley:site/kmnopqrstuvwxyzkmnopqrstuvwxyzkm", "valley:app/kmnopqrstuvwxyzkmnopqrstuvwxyzkm")
			}
		}
	})
	page = h.page("/app/knots/oc-a000003")
	for _, want := range []string{
		`<span class="mono">app:oc-fffffff</span> <span class="muted">not found in its graph</span>`,
		`<span class="mono">oc-eeeeeee</span> <span class="muted">not found</span>`,
		`<li class="mono">a change in an unpublished project</li>`,
		`<li class="mono">valley:app/kmnopqrstuvwxyzkmnopqrstuvwxyzkm</li>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the knot's page does not show %q", want)
		}
	}
	for _, unwanted := range unpublishedText {
		if strings.Contains(page, unwanted) {
			t.Errorf("the knot's page shows %q", unwanted)
		}
	}
}

// A project the export does not hold as public answers every route
// exactly as a project that does not exist, even when its repository is
// in the generation's directory.
func TestHiddenProjectAnswersLikeMissing(t *testing.T) {
	h := newHarness(t)
	g := h.active()
	if err := os.CopyFS(filepath.Join(g.Dir, "repos", "site.git"), os.DirFS(h.f.Repos["site"])); err != nil {
		t.Fatal(err)
	}
	siteTip := git(t, h.f.Repos["site"], nil, "rev-parse", export.MainRef)
	notFound := h.get("/app/knots/oc-fffffff")
	for _, route := range []string{
		"/%s",
		"/%s/",
		"/%s/knots/oc-b000001",
		"/%s/source",
		"/%s/source/",
		"/%s/source/tree/" + siteTip + "/",
		"/%s/source/blob/" + siteTip + "/README.md",
		"/%s/source/log/" + siteTip,
		"/%s/source/tree/" + siteTip + "/no/such/path/",
		"/%s/source/anything",
	} {
		hidden, missing := h.get(fmt.Sprintf(route, "site")), h.get(fmt.Sprintf(route, "nosuch"))
		if hidden.Code != http.StatusNotFound || missing.Code != http.StatusNotFound {
			t.Errorf("%s answered %d hidden and %d missing", route, hidden.Code, missing.Code)
		}
		if fmt.Sprint(hidden.Header()) != fmt.Sprint(missing.Header()) {
			t.Errorf("%s answered with other headers hidden than missing:\n%v\n%v", route, hidden.Header(), missing.Header())
		}
		if !bytes.Equal(hidden.Body.Bytes(), missing.Body.Bytes()) || !bytes.Equal(hidden.Body.Bytes(), notFound.Body.Bytes()) {
			t.Errorf("%s answered with another page hidden than missing", route)
		}
	}
}

// A project's name may be any word the server's routes use: source,
// knots, or web-export, the receiver's first segment. Each such project
// is served on every route, and /<project>/ redirects to its tree. The
// server's own paths, under /-/, are never a project's.
func TestProjectNamesBesideTheServersPaths(t *testing.T) {
	for _, name := range []string{"source", "knots", "web-export"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.publish(exporttest.SignedAt.Add(time.Minute), map[string]string{name: h.f.Bundles["app"]},
				func(e *export.Export) { e.Projects[0].Name = name })
			tip := h.tip()
			tree := "/" + name + "/source/tree/" + tip + "/"
			if plan := h.page("/"); !strings.Contains(plan, `href="/`+name+`/knots/oc-a000002"`) {
				t.Errorf("the plan page does not link %s's knots", name)
			}
			for _, target := range []string{
				"/" + name + "/knots/oc-a000002",
				tree,
				"/" + name + "/source/blob/" + tip + "/docs/guide.md",
				"/" + name + "/source/log/" + tip,
			} {
				h.page(target)
			}
			for _, target := range []string{"/" + name + "/", "/" + name, "/" + name + "/source/"} {
				if w := h.get(target); w.Code != http.StatusFound || w.Header().Get("Location") != tree {
					t.Errorf("GET %s answered %d to %q, not a redirect to %s", target, w.Code, w.Header().Get("Location"), tree)
				}
			}
			for _, target := range []string{
				"/-", "/-/", "/-/" + name + "/", "/-/knots/oc-a000002", "/-/source/tree/" + tip + "/",
				"/-/" + name + "/source/tree/" + tip + "/", "/app/", "/site/", "/site",
			} {
				if w := h.get(target); w.Code != http.StatusNotFound {
					t.Errorf("GET %s answered %d, not 404", target, w.Code)
				}
			}
		})
	}
}

// A source route serves a commit only when main's published tip reaches
// it, and refuses every other object ID or name with the one 404.
func TestUnreachableCommitIsRefused(t *testing.T) {
	h := newHarness(t)
	repo, _ := h.active().Repo("app")
	tip := h.tip()
	tree := git(t, repo, nil, "rev-parse", tip+"^{tree}")
	readme := git(t, repo, nil, "rev-parse", tip+":README.md")
	side := git(t, repo, nil, "commit-tree", tree, "-p", tip, "-m", "a commit main does not reach")
	parent := git(t, repo, nil, "rev-parse", tip+"~2")
	notFound := h.get("/app/knots/oc-fffffff").Body.String()

	for _, commit := range []string{side, tree, readme, tip[:12], "main", "HEAD", strings.Repeat("0", 40), tip + "~1"} {
		for _, route := range []string{"/app/source/tree/%s/", "/app/source/blob/%s/README.md", "/app/source/log/%s"} {
			target := fmt.Sprintf(route, commit)
			w := h.get(target)
			if w.Code != http.StatusNotFound || w.Body.String() != notFound {
				t.Errorf("GET %s answered %d, not the one 404", target, w.Code)
			}
		}
	}

	// A commit the tip reaches is served, as that commit had it.
	page := h.page("/app/source/blob/" + parent + "/README.md")
	if !strings.Contains(page, "A synthetic project for the web UI&#39;s tests.") || strings.Contains(page, "readme") {
		t.Errorf("the first README is not served as the first commit had it:\n%s", page)
	}
}

// The source browser lists a directory and its README, shows a file and
// main's log, and every source link names its project, a full commit and
// a path.
func TestSourceBrowser(t *testing.T) {
	h := newHarness(t)
	tip := h.tip()

	for _, path := range []string{"/app/source", "/app/source/"} {
		w := h.get(path)
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/app/source/tree/"+tip+"/" {
			t.Fatalf("the project's source answered %d to %q", w.Code, w.Header().Get("Location"))
		}
	}

	root := h.page("/app/source/tree/" + tip + "/")
	docs, src, readme := strings.Index(root, ">docs/<"), strings.Index(root, ">src/<"), strings.Index(root, ">README.md<")
	if docs < 0 || src < 0 || readme < 0 || readme < src || src < docs {
		t.Errorf("the root does not list docs/ and src/ before README.md:\n%s", root)
	}
	if !strings.Contains(root, `<pre class="text"># app`) {
		t.Error("the root does not show its README")
	}
	if dir := h.page("/app/source/tree/" + tip + "/docs/"); !strings.Contains(dir, ">guide.md<") {
		t.Error("docs/ does not list guide.md")
	}
	file := h.page("/app/source/blob/" + tip + "/docs/guide.md")
	if !strings.Contains(file, `<tr id="L1"><td class="ln"><a href="#L1">1</a></td><td class="src"># Guide</td></tr>`) {
		t.Errorf("the file is not shown line by line:\n%s", file)
	}
	log := h.page("/app/source/log/" + tip)
	first, last := strings.Index(log, "docs: a guide, and the README names it"), strings.Index(log, "app: the first README")
	if first < 0 || last < first {
		t.Errorf("the log does not list main's commits newest first:\n%s", log)
	}

	links := regexp.MustCompile(`href="(/[^"]*/source/[^"]*)"`)
	named := regexp.MustCompile(`^/app/source/(tree|blob|log)/[0-9a-f]{40}(/|$)`)
	for _, page := range []string{root, file, log} {
		for _, m := range links.FindAllStringSubmatch(page, -1) {
			if !named.MatchString(m[1]) {
				t.Errorf("the link %s names no commit", m[1])
			}
		}
	}

	for _, target := range []string{
		"/app/source/tree/" + tip + "/README.md/",
		"/app/source/blob/" + tip + "/docs",
		"/app/source/blob/" + tip + "/docs/guide.md/",
		"/app/source/blob/" + tip + "/docs/../README.md",
		"/app/source/blob/" + tip + "/docs%2Fguide.md",
		"/app/source/tree/" + tip + "//",
		"/app/source/log/" + tip + "?skip=-1",
	} {
		if w := h.get(target); w.Code != http.StatusNotFound {
			t.Errorf("GET %s answered %d", target, w.Code)
		}
	}
}

// file is one file a test commits: its mode, path and content.
type file struct{ mode, path, content string }

// commitMain writes a commit onto the fixture's app main, with message,
// that holds the files given beside what main holds. It returns the new
// tip and a bundle of it. Each content is written once, however many
// files hold it.
func (h *harness) commitMain(message string, files ...file) (tip, bundle string) {
	t := h.t
	t.Helper()
	repo := h.f.Repos["app"]
	var b bytes.Buffer
	data := func(s string) { fmt.Fprintf(&b, "data %d\n%s\n", len(s), s) }
	marks := map[string]int{}
	for _, f := range files {
		if _, ok := marks[f.content]; !ok {
			marks[f.content] = len(marks) + 1
			fmt.Fprintf(&b, "blob\nmark :%d\n", marks[f.content])
			data(f.content)
		}
	}
	who := "Example Author <author@example.org> 1791190800 +0000"
	fmt.Fprintf(&b, "commit refs/heads/main\nauthor %s\ncommitter %s\n", who, who)
	data(message)
	fmt.Fprintf(&b, "from %s\n", git(t, repo, nil, "rev-parse", export.MainRef))
	for _, f := range files {
		fmt.Fprintf(&b, "M %s :%d %s\n", f.mode, marks[f.content], f.path)
	}
	git(t, repo, b.Bytes(), "fast-import", "--quiet")
	tip = git(t, repo, nil, "rev-parse", export.MainRef)
	bundle = filepath.Join(t.TempDir(), "app.bundle")
	git(t, repo, nil, "bundle", "create", "--quiet", bundle, "main")
	return tip, bundle
}

// publishMain activates a generation whose app main is tip, carried by
// bundle.
func (h *harness) publishMain(at time.Time, tip, bundle string) {
	h.t.Helper()
	h.publish(at, map[string]string{"app": bundle}, func(e *export.Export) { e.Projects[0].SourceTip = tip })
}

// hostileMain writes a commit onto the fixture's app main that holds a
// file of HTML, a file larger than a page shows, a binary file, a file
// whose name is HTML, and a symbolic link out of the repository. It
// returns the new tip and a bundle of it.
func (h *harness) hostileMain() (tip, bundle string) {
	return h.commitMain("hostile: <script>alert(\"subject\")</script>\n",
		file{"100644", "page.html", "<html><script>alert(\"file\")</script><img src=x onerror=alert(2)></html>\n"},
		file{"100644", "big.txt", strings.Repeat("big\n", (MaxFile/4)+1)},
		file{"100644", "binary.bin", "\x7fELF\x00\x01\x02<script>alert(\"binary\")</script>"},
		file{"100644", "<img src=x onerror=alert(3)>.txt", "named\n"},
		file{"120000", "link", "../../etc/passwd"},
	)
}

// HTML, scripts, large files and binary files from a repository or the
// plan render inert: escaped as text, or not shown.
func TestHostileContentRendersInert(t *testing.T) {
	h := newHarness(t)
	tip, bundle := h.hostileMain()
	h.publish(exporttest.SignedAt.Add(time.Minute), map[string]string{"app": bundle}, func(e *export.Export) {
		e.Projects[0].SourceTip = tip
	})

	inert := func(target, page string, wants ...string) {
		t.Helper()
		// Escaped, a tag is text; only a tag the page did not escape, or
		// an attribute it let a value break out into, is active.
		for _, active := range []string{"<script", "<img", `" onerror`, "\x00"} {
			if strings.Contains(page, active) {
				t.Errorf("GET %s holds %q", target, active)
			}
		}
		for _, want := range wants {
			if !strings.Contains(page, want) {
				t.Errorf("GET %s does not show %q", target, want)
			}
		}
	}
	at := "/app/source/tree/" + tip + "/"
	inert(at, h.page(at),
		`&lt;script&gt;alert(&#34;readme&#34;)&lt;/script&gt;`,
		`&lt;img src=x onerror=alert(3)&gt;.txt`,
		`href="/app/source/blob/`+tip+`/%3Cimg%20src=x%20onerror=alert%283%29%3E.txt"`)
	at = "/app/source/blob/" + tip + "/%3Cimg%20src=x%20onerror=alert%283%29%3E.txt"
	inert(at, h.page(at), ">named<")
	at = "/app/source/blob/" + tip + "/page.html"
	inert(at, h.page(at), `&lt;html&gt;&lt;script&gt;alert(&#34;file&#34;)&lt;/script&gt;&lt;img src=x onerror=alert(2)&gt;&lt;/html&gt;`)
	at = "/app/source/blob/" + tip + "/big.txt"
	page := h.page(at)
	inert(at, page, "This file is 1.0 MiB, larger than the 1.0 MiB a page shows.")
	if len(page) > 64<<10 {
		t.Errorf("the large file's page is %d bytes", len(page))
	}
	at = "/app/source/blob/" + tip + "/binary.bin"
	inert(at, h.page(at), "This file is binary, 39 bytes, and is not shown.")
	at = "/app/source/blob/" + tip + "/link"
	inert(at, h.page(at), "A symbolic link to ../../etc/passwd. It is not followed.")
	at = "/app/source/log/" + tip
	inert(at, h.page(at), "hostile: &lt;script&gt;alert(&#34;subject&#34;)&lt;/script&gt;")

	at = "/app/knots/bd-a000004"
	inert(at, h.page(at),
		`A README holding &lt;script&gt;alert(&#34;knot&#34;)&lt;/script&gt; must show as text.`,
		`Also &lt;img src=x onerror=alert(1)&gt; in a comment.`)
}

// A directory larger than a page reads is listed in part, and a commit
// message larger than a page reads ends its page of history early. git
// is stopped once its listing passes MaxRead, and nothing past a record's
// bound is kept.
func TestOversizedTreeAndHistory(t *testing.T) {
	h := newHarness(t)
	// Each entry of huge/ takes 308 bytes of git ls-tree's output, so
	// 15,000 of them pass MaxRead at about entry 13,600.
	longest := "!" + strings.Repeat("l", 4095)
	files := []file{{"100644", "huge/" + longest, "x\n"}}
	name := func(i int) string { return fmt.Sprintf("%05d-%s", i, strings.Repeat("n", 240)) }
	for i := range 15_000 {
		files = append(files, file{"100644", "huge/" + name(i), "x\n"})
	}
	if n := 15_000 * (6 + 1 + 4 + 1 + 40 + 1 + 7 + 1 + 246 + 1); n <= MaxRead {
		t.Fatalf("the listing of huge/ is %d bytes, within MaxRead", n)
	}
	h.commitMain("app: a directory larger than a page reads\n", files...)
	h.commitMain("long: " + strings.Repeat("a long subject ", (100<<10)/15) + "\n")
	tip, bundle := h.commitMain("longer: " + strings.Repeat("a subject past the bound ", (5<<20)/25) + "\n")
	h.publishMain(exporttest.SignedAt.Add(time.Minute), tip, bundle)

	at := "/app/source/tree/" + tip + "/huge/"
	page := h.page(at)
	if !strings.Contains(page, "This directory is larger than a page reads, so only part of it is listed.") {
		t.Error("huge/ is not marked as listed in part")
	}
	if n := strings.Count(page, `<tr><td class="name">`); n != MaxEntries {
		t.Errorf("huge/ lists %d entries, not %d", n, MaxEntries)
	}
	if !strings.Contains(page, `">`+longest+`</a>`) {
		t.Error("the entry with the longest name git accepts is not listed whole")
	}
	if len(page) > 1<<20 {
		t.Errorf("huge/'s page is %d bytes", len(page))
	}
	if w := h.get("/app/source/blob/" + tip + "/huge/" + name(0)); w.Code != http.StatusOK {
		t.Errorf("an entry read before MaxRead answered %d", w.Code)
	}
	if w := h.get("/app/source/blob/" + tip + "/huge/" + name(14_999)); w.Code != http.StatusNotFound {
		t.Errorf("an entry past MaxRead answered %d", w.Code)
	}

	at = "/app/source/log/" + tip
	page = h.page(at)
	if !strings.Contains(page, "This page ends early: its commits are larger than a page reads.") {
		t.Error("the page of history holding a 5 MiB message does not end early")
	}
	if n := strings.Count(page, `<tr><td class="name">`); n != 1 {
		t.Errorf("the page that ends early lists %d commits, not the one it cut", n)
	}
	if m := regexp.MustCompile(`<td>(longer: a subject past the bound[^<]*)…</td>`).FindStringSubmatch(page); m == nil || len(m[1]) > maxLogText {
		t.Error("the cut commit's subject is not shown cut to maxLogText")
	}
	if !strings.Contains(page, `href="/app/source/log/`+tip+`?skip=1"`) {
		t.Error("the page that ends early does not link the commits after the one it cut")
	}
	page = h.page(at + "?skip=1")
	if strings.Contains(page, "ends early") || !strings.Contains(page, "long: a long subject") || !strings.Contains(page, "app: the first README") {
		t.Errorf("the next page of history is not whole:\n%s", page)
	}
	if strings.Contains(page, strings.Repeat("a long subject ", (maxLogText/15)+1)) {
		t.Error("a subject longer than a page shows is not cut")
	}
	for _, p := range []string{at, at + "?skip=1"} {
		if n := len(h.page(p)); n > 64<<10 {
			t.Errorf("GET %s is %d bytes", p, n)
		}
	}
}

// The directories along a path share one budget of MaxRead bytes of git
// ls-tree's output. Each of big/ and big/zz/ lists about 3 MB before
// the entry the path names next, within MaxRead alone, so the directory
// big/zz/ is served. A path through both, to big/zz/zz/f, passes the
// budget and answers 404. The directory's own listing has a budget of
// its own.
func TestResolveSharesOneBudget(t *testing.T) {
	h := newHarness(t)
	name := func(i int) string { return fmt.Sprintf("%05d-%s", i, strings.Repeat("n", 240)) }
	var files []file
	for i := range 10_000 {
		files = append(files, file{"100644", "big/" + name(i), "x\n"}, file{"100644", "big/zz/" + name(i), "x\n"})
	}
	files = append(files, file{"100644", "big/zz/zz/f", "the file\n"})
	if each := 6 + 1 + 4 + 1 + 40 + 1 + 246 + 1; 10_000*each > MaxRead || 2*10_000*each <= MaxRead {
		t.Fatalf("the listings take %d bytes each, not between half MaxRead and MaxRead", 10_000*each)
	}
	tip, bundle := h.commitMain("app: two large directories, one in the other\n", files...)
	h.publishMain(exporttest.SignedAt.Add(time.Minute), tip, bundle)

	h.page("/app/source/tree/" + tip + "/big/zz/")
	h.page("/app/source/blob/" + tip + "/big/zz/" + name(0))
	for _, target := range []string{"/app/source/tree/" + tip + "/big/zz/zz/", "/app/source/blob/" + tip + "/big/zz/zz/f"} {
		if w := h.get(target); w.Code != http.StatusNotFound {
			t.Errorf("GET %s, through %d bytes of listings, answered %d", target, 2*10_000*300, w.Code)
		}
	}
}

// A page of history skips at most MaxSkip commits. The page at MaxSkip
// links the commits older than it as the history that ends at its last
// commit, less that commit, and that page is served.
func TestLogSkipIsBounded(t *testing.T) {
	h := newHarness(t)
	repo := h.f.Repos["app"]
	var b bytes.Buffer
	who := "Example Author <author@example.org> 1791190800 +0000"
	n := MaxSkip + LogPage + 10
	for i := range n {
		fmt.Fprintf(&b, "commit refs/heads/main\nauthor %s\ncommitter %s\n", who, who)
		msg := fmt.Sprintf("commit %d\n", i)
		fmt.Fprintf(&b, "data %d\n%s\n", len(msg), msg)
		if i == 0 {
			fmt.Fprintf(&b, "from %s\n", git(t, repo, nil, "rev-parse", export.MainRef))
		}
	}
	git(t, repo, b.Bytes(), "fast-import", "--quiet")
	tip := git(t, repo, nil, "rev-parse", export.MainRef)
	bundle := filepath.Join(t.TempDir(), "app.bundle")
	git(t, repo, nil, "bundle", "create", "--quiet", bundle, "main")
	h.publishMain(exporttest.SignedAt.Add(time.Minute), tip, bundle)

	at := "/app/source/log/" + tip
	if page := h.page(at + fmt.Sprintf("?skip=%d", MaxSkip-LogPage)); !strings.Contains(page, fmt.Sprintf(`href="%s?skip=%d"`, at, MaxSkip)) {
		t.Errorf("the page before MaxSkip does not link the page at it")
	}
	page := h.page(at + fmt.Sprintf("?skip=%d", MaxSkip))
	// The page at MaxSkip shows commits n-1-MaxSkip down to
	// n-MaxSkip-LogPage, the last of them, counting from the oldest new
	// commit as 0.
	last := git(t, repo, nil, "rev-parse", fmt.Sprintf("%s~%d", tip, MaxSkip+LogPage-1))
	older := "/app/source/log/" + last + "?skip=1"
	if !strings.Contains(page, `href="`+older+`"`) {
		t.Errorf("the page at MaxSkip does not link its older commits as the history of %s", last)
	}
	page = h.page(older)
	if !strings.Contains(page, fmt.Sprintf("commit %d<", n-MaxSkip-LogPage-1)) || !strings.Contains(page, "app: the first README") {
		t.Errorf("the history past MaxSkip is not served:\n%s", page)
	}
	if strings.Contains(page, fmt.Sprintf("commit %d<", n-MaxSkip-LogPage)) {
		t.Error("the history past MaxSkip shows the commit it starts after")
	}
	if w := h.get(at + fmt.Sprintf("?skip=%d", MaxSkip+1)); w.Code != http.StatusNotFound {
		t.Errorf("a page skipping past MaxSkip answered %d", w.Code)
	}
}

// A source request's git commands have a bound of their own, far within
// the request's time: a git that does not finish is stopped once it
// passes, and the request answers 503 and gives back its git slot. The
// failure is logged.
func TestSourceGitHasItsOwnBound(t *testing.T) {
	h := newHarness(t)
	real, err := exec.LookPath(export.GitProgram)
	if err != nil {
		t.Fatal(err)
	}
	if real, err = filepath.Abs(real); err != nil {
		t.Fatal(err)
	}
	slow := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nfor a; do [ \"$a\" = ls-tree ] && exec sleep 60; done\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(slow, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	program := export.GitProgram
	export.GitProgram = slow
	t.Cleanup(func() { export.GitProgram = program })
	var logged []error
	var mu sync.Mutex
	h.srv.log = func(err error) { mu.Lock(); logged = append(logged, err); mu.Unlock() }
	h.srv.gitTime = 300 * time.Millisecond

	start := time.Now()
	w := h.get("/app/source/tree/" + h.tip() + "/")
	if took := time.Since(start); w.Code != http.StatusServiceUnavailable || took > 5*time.Second {
		t.Errorf("a source request whose git does not finish answered %d after %v", w.Code, took)
	}
	if len(h.srv.slots) != 0 {
		t.Errorf("the request holds %d git slots once it answered", len(h.srv.slots))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 || !strings.Contains(logged[0].Error(), "the request's git ran past 300ms") {
		t.Errorf("the server logged %v", logged)
	}
	if w := h.get("/app/knots/oc-a000002"); w.Code != http.StatusOK {
		t.Errorf("a knot answered %d", w.Code)
	}
}

// A SHA-256 project's commits are served by their 64 hex digits alone.
// The first 40 of them, which git would read as an abbreviation, answer
// 404 on every source route, so each page has one address.
func TestSHA256CommitIDs(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "app.git")
	git(t, repo, nil, "init", "--quiet", "--bare", "--template=", "--initial-branch=main", "--object-format=sha256")
	readme := "# app\n\nA SHA-256 repository.\n"
	who := "Example Author <author@example.org> 1791190800 +0000"
	stream := fmt.Sprintf("commit refs/heads/main\nauthor %s\ncommitter %s\ndata 7\nsha256\n\nM 100644 inline README.md\ndata %d\n%s\n",
		who, who, len(readme), readme)
	git(t, repo, []byte(stream), "fast-import", "--quiet")
	tip := git(t, repo, nil, "rev-parse", export.MainRef)
	if len(tip) != 64 {
		t.Fatalf("the tip is %q", tip)
	}
	bundle := filepath.Join(t.TempDir(), "app.bundle")
	git(t, repo, nil, "bundle", "create", "--quiet", bundle, "main")
	h.publishMain(exporttest.SignedAt.Add(time.Minute), tip, bundle)

	for _, route := range []string{"/app/source/tree/%s/", "/app/source/blob/%s/README.md", "/app/source/log/%s"} {
		if page := h.page(fmt.Sprintf(route, tip)); !strings.Contains(page, tip[:12]) {
			t.Errorf("%s does not name the commit", fmt.Sprintf(route, tip))
		}
		if w := h.get(fmt.Sprintf(route, tip[:40])); w.Code != http.StatusNotFound {
			t.Errorf("%s answered %d", fmt.Sprintf(route, tip[:40]), w.Code)
		}
	}
	if w := h.get("/app/source/"); w.Header().Get("Location") != "/app/source/tree/"+tip+"/" {
		t.Errorf("/app/source/ redirects to %q", w.Header().Get("Location"))
	}
}

// A file of exactly MaxFile bytes of newlines is shown to MaxLines lines,
// and its page says where it stops. A README longer than a page shows is
// shown in part.
func TestLongFileIsShownInPart(t *testing.T) {
	h := newHarness(t)
	tip, bundle := h.commitMain("app: long files\n",
		file{"100644", "newlines.txt", strings.Repeat("\n", MaxFile)},
		file{"100644", "long/README.md", strings.Repeat("a line of the README\n", 2*MaxLines)},
		file{"100644", "wide.txt", strings.Repeat("<", MaxFile)},
	)
	h.publishMain(exporttest.SignedAt.Add(time.Minute), tip, bundle)

	page := h.page("/app/source/blob/" + tip + "/newlines.txt")
	last := fmt.Sprintf(`id="L%d"`, MaxLines)
	if !strings.Contains(page, last) || strings.Contains(page, fmt.Sprintf(`id="L%d"`, MaxLines+1)) {
		t.Errorf("the file of newlines is not shown to line %d", MaxLines)
	}
	if want := fmt.Sprintf("The page stops at line %d of %d.", MaxLines, MaxFile); !strings.Contains(page, want) {
		t.Errorf("the page does not say %q", want)
	}
	if len(page) > MaxShown {
		t.Errorf("the page of a file of newlines is %d bytes, past MaxShown", len(page))
	}

	// One line of MaxFile characters that each escape to four bytes is
	// cut where MaxShown is reached.
	page = h.page("/app/source/blob/" + tip + "/wide.txt")
	if !strings.Contains(page, "The page stops at line 1 of 1.") || len(page) > MaxShown+64<<10 {
		t.Errorf("the page of one wide line is %d bytes", len(page))
	}

	page = h.page("/app/source/tree/" + tip + "/long/")
	if !strings.Contains(page, "This README is longer than a page shows, and only its start is shown.") {
		t.Error("the long README is not marked as shown in part")
	}
	if n := strings.Count(page, "a line of the README"); n != MaxLines {
		t.Errorf("the long README shows %d lines, not %d", n, MaxLines)
	}
}

// A chain of knots as deep as the graph's knot limit allows renders its
// milestone to MaxPlanDepth levels, counts every knot under it, and
// names MaxPlanDepth parents in the deepest knot's trail. The plan page's
// structure is built once for the generation. Siblings that block one
// another in a cycle share the last stage of their path.
func TestDeepPlan(t *testing.T) {
	h := newHarness(t)
	var chain []string
	h.publish(exporttest.SignedAt.Add(time.Minute), nil, func(e *export.Export) {
		p := &e.Projects[0]
		for i := range knot.MaxKnots - len(p.Knots) - 3 {
			id := fmt.Sprintf("oc-c%06x", i)
			k := export.Knot{ID: id, Type: "outcome", Status: "open", Active: true, State: "ready",
				Title: fmt.Sprintf("Level %d of the chain", i), Created: "2026-10-01", CreatedBy: "operator"}
			if i == 0 {
				p.Roots = append(p.Roots, id)
			} else {
				k.Parent = chain[i-1]
				p.Knots[len(p.Knots)-1].Children = []string{id}
			}
			chain = append(chain, id)
			p.Knots = append(p.Knots, k)
		}
		// Under the first link, a path whose two last siblings wait on
		// each other.
		free, a, b := "oc-d000001", "oc-d000002", "oc-d000003"
		for _, k := range []export.Knot{
			{ID: free, BlockedBy: []string{}},
			{ID: a, BlockedBy: []string{free, b}},
			{ID: b, BlockedBy: []string{a}},
		} {
			k.Type, k.Status, k.State, k.Title, k.Parent = "outcome", "open", "blocked", "Sibling "+k.ID, chain[0]
			p.Knots = append(p.Knots, k)
		}
		for i := range p.Knots {
			if p.Knots[i].ID == chain[0] {
				p.Knots[i].Children = append(p.Knots[i].Children, free, a, b)
			}
		}
	})

	page := h.page("/")
	if want := fmt.Sprintf("%d of %d done", 0, len(chain)-1+3); !strings.Contains(page, want) {
		t.Errorf("the chain's milestone does not count every knot under it as %q", want)
	}
	if !strings.Contains(page, `href="/app/knots/`+chain[MaxPlanDepth]+`"`) || strings.Contains(page, `href="/app/knots/`+chain[MaxPlanDepth+1]+`"`) {
		t.Errorf("the chain's tree is not shown to %d levels", MaxPlanDepth)
	}
	if n := strings.Count(page, ">more on its page<"); n != 1 {
		t.Errorf("%d rows say there is more on their page; the chain's deepest shown row does", n)
	}
	if len(page) > 256<<10 {
		t.Errorf("the plan page is %d bytes", len(page))
	}
	paths := section(t, page, `id="paths-h"`, `id="ms-h"`)
	stage := regexp.MustCompile(`<span class="stage-num">(\d+)</span><ul class="plain">(.*?)</ul>`)
	var stages []string
	for _, m := range stage.FindAllStringSubmatch(paths, -1) {
		if strings.Contains(m[2], "Sibling") {
			var ids []string
			for _, id := range regexp.MustCompile(`"mono muted">(oc-d00000\d)<`).FindAllStringSubmatch(m[2], -1) {
				ids = append(ids, id[1])
			}
			stages = append(stages, m[1]+":"+strings.Join(ids, ","))
		}
	}
	if got := strings.Join(stages, " "); got != "1:oc-d000001 2:oc-d000002,oc-d000003" {
		t.Errorf("the siblings' path has stages %q", got)
	}

	h.srv.mu.Lock()
	plan := h.srv.cached.idx.plan
	h.srv.mu.Unlock()
	h.page("/")
	h.srv.mu.Lock()
	again := h.srv.cached.idx.plan
	h.srv.mu.Unlock()
	if plan != again {
		t.Error("the plan page's structure was built again for the same generation")
	}

	deepest := chain[len(chain)-1]
	page = h.page("/app/knots/" + deepest)
	trail := section(t, page, `<nav class="trail"`, `</nav>`)
	if n := strings.Count(trail, `<a href="/app/knots/`); n != MaxPlanDepth || !strings.Contains(trail, "<span>…</span>") {
		t.Errorf("the deepest knot's trail names %d parents, or does not mark those above them", n)
	}
	if !strings.Contains(trail, chain[len(chain)-2]) || strings.Contains(trail, `"/app/knots/`+chain[len(chain)-2-MaxPlanDepth]+`"`) {
		t.Error("the trail does not name the nearest parents alone")
	}
}

// Every response carries the strict policy, and the policy admits the
// page's one inline stylesheet and one inline script by their hashes and
// nothing from another origin.
func TestEveryResponseCarriesTheCSP(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct{ method, target string }{
		{"GET", "/"},
		{"GET", "/app/knots/oc-a000002"},
		{"GET", "/app/source/"},
		{"GET", "/app/source/tree/" + h.tip() + "/"},
		{"GET", "/app/source/blob/" + h.tip() + "/README.md"},
		{"GET", "/no/such/page"},
		{"POST", "/"},
		{"HEAD", "/"},
	} {
		w := h.do(c.method, c.target)
		got := w.Header().Get("Content-Security-Policy")
		if got != csp || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s %s answered with policy %q", c.method, c.target, got)
		}
	}
	for _, directive := range []string{"default-src 'self'", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Errorf("the policy lacks %s", directive)
		}
	}
	for _, loose := range []string{"unsafe-inline", "unsafe-eval", "http", "*", "data:"} {
		if strings.Contains(csp, loose) {
			t.Errorf("the policy holds %q", loose)
		}
	}

	page := h.page("/")
	inline := func(tag string) string {
		t.Helper()
		m := regexp.MustCompile(`(?s)<`+tag+`>(.*?)</`+tag+`>`).FindAllStringSubmatch(page, -1)
		if len(m) != 1 || strings.Count(page, "<"+tag) != 1 {
			t.Fatalf("the plan page has %d %s elements; it has one", len(m), tag)
		}
		sum := sha256.Sum256([]byte(m[0][1]))
		return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	if hash := inline("script"); !strings.Contains(csp, "script-src "+hash+";") {
		t.Errorf("the policy does not admit the page's script, %s", hash)
	}
	if hash := inline("style"); !strings.Contains(csp, "style-src "+hash+";") {
		t.Errorf("the policy does not admit the page's stylesheet, %s", hash)
	}
	for _, external := range []string{"<link", " src=", "@import", "url("} {
		if strings.Contains(page, external) {
			t.Errorf("the plan page holds %q", external)
		}
	}
}

// A request serves everything from the generation it opened, though
// another activates while it runs, and that generation outlives the
// activation until the request releases it.
func TestARequestKeepsItsGeneration(t *testing.T) {
	h := newHarness(t)
	paused, resume := make(chan export.Generation), make(chan struct{})
	var once sync.Once
	h.srv.opened = func(g export.Generation) { once.Do(func() { paused <- g; <-resume }) }
	served := make(chan *httptest.ResponseRecorder)
	go func() {
		w := httptest.NewRecorder()
		h.srv.ServeHTTP(w, httptest.NewRequest("GET", "/app/knots/oc-a000001", nil))
		served <- w
	}()
	old := <-paused

	next := h.publish(exporttest.SignedAt.Add(time.Minute), nil, renamed("Readers browse the second plan"))
	if next.Sequence == old.Sequence {
		t.Fatal("the publish did not activate a new generation")
	}
	if _, err := os.Stat(old.Dir); err != nil {
		t.Fatalf("the generation a request holds was deleted: %v", err)
	}
	close(resume)
	w := <-served
	if body := w.Body.String(); w.Code != http.StatusOK || !strings.Contains(body, "Readers browse the app&#39;s plan") || strings.Contains(body, "second plan") {
		t.Errorf("the request did not serve the generation it opened: %d\n%s", w.Code, body)
	}
	if !strings.Contains(h.page("/app/knots/oc-a000001"), "Readers browse the second plan") {
		t.Error("the next request does not serve the new generation")
	}

	// Released, the old generation goes with the next activation.
	h.publish(exporttest.SignedAt.Add(2*time.Minute), nil, renamed("Readers browse the third plan"))
	if _, err := os.Stat(old.Dir); !os.IsNotExist(err) {
		t.Errorf("the released generation is still on disk: %v", err)
	}
}

// eventually waits up to ten seconds for cond to hold.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
	}
}

// renamed changes the title of the fixture's first knot.
func renamed(title string) func(*export.Export) {
	return func(e *export.Export) { e.Projects[0].Knots[0].Title = title }
}

// Requests that wait for a generation together read its export once.
// While it is read, a newer generation is read on its own, and at most
// MaxLoads exports are read at once. A request whose load ends after a
// newer generation was activated waits for that one, so every request is
// served the generation active when its snapshot is ready, and an older
// read that finishes last does not displace the newest snapshot.
func TestExportLoadsCoalesce(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	loads := map[int64]int{}
	holds := map[int64]chan struct{}{}
	hold := func(g export.Generation) chan struct{} {
		mu.Lock()
		defer mu.Unlock()
		if holds[g.Sequence] == nil {
			holds[g.Sequence] = make(chan struct{})
		}
		return holds[g.Sequence]
	}
	h.srv.loading = func(g export.Generation) {
		mu.Lock()
		loads[g.Sequence]++
		mu.Unlock()
		<-hold(g)
	}
	loaded := func(g export.Generation) int {
		mu.Lock()
		defer mu.Unlock()
		return loads[g.Sequence]
	}
	var opens atomic.Int32
	h.srv.opened = func(export.Generation) { opens.Add(1) }
	get := func(n int) chan *httptest.ResponseRecorder {
		served := make(chan *httptest.ResponseRecorder, n)
		for range n {
			go func() {
				w := httptest.NewRecorder()
				h.srv.ServeHTTP(w, httptest.NewRequest("GET", "/app/knots/oc-a000001", nil))
				served <- w
			}()
		}
		return served
	}
	expect := func(served chan *httptest.ResponseRecorder, n int, title string) {
		t.Helper()
		for range n {
			if w := <-served; w.Code != http.StatusOK || !strings.Contains(w.Body.String(), title) {
				t.Errorf("a request answered %d, not with %q", w.Code, title)
			}
		}
	}
	waiting := func(n int) func() bool { return func() bool { return len(h.srv.loadWaiting) == n } }

	first := h.active()
	firstServed := get(10)
	eventually(t, "ten requests wait for the first generation's read", func() bool {
		return loaded(first) == 1 && len(h.srv.loadWaiting) == 10
	})
	second := h.publish(exporttest.SignedAt.Add(time.Minute), nil, renamed("Readers browse the second plan"))
	secondServed := get(1)
	eventually(t, "the second generation is read", func() bool { return loaded(second) == 1 })

	// Both loaders are taken, so the third generation waits for one.
	third := h.publish(exporttest.SignedAt.Add(2*time.Minute), nil, renamed("Readers browse the third plan"))
	thirdServed := get(1)
	eventually(t, "a request waits for the third generation", waiting(12))
	time.Sleep(20 * time.Millisecond)
	if n := loaded(third); n != 0 {
		t.Errorf("a third export was read while %d were", MaxLoads)
	}
	if n := opens.Load(); n != 0 {
		t.Errorf("%d requests opened a generation while they waited for its read", n)
	}

	// The second read ends, and its request finds the third generation
	// active, and waits for that read with the third's request.
	close(hold(second))
	eventually(t, "the third generation is read", func() bool { return loaded(third) == 1 })
	close(hold(third))
	expect(secondServed, 1, "Readers browse the third plan")
	expect(thirdServed, 1, "Readers browse the third plan")
	close(hold(first))
	expect(firstServed, 10, "Readers browse the third plan")

	if !strings.Contains(h.page("/app/knots/oc-a000001"), "Readers browse the third plan") {
		t.Error("the first generation's read displaced the third's")
	}
	for _, g := range []export.Generation{first, second, third} {
		if n := loaded(g); n != 1 {
			t.Errorf("generation %d's export was read %d times", g.Sequence, n)
		}
	}
}

// Source requests past MaxGit wait, MaxWaiting of them, and one more
// answers 503 at once. A waiting request holds no generation, so an
// activation while the server is saturated deletes the old generation,
// and every waiting request serves the new one. A request's time runs
// from its arrival, so one that waits past it answers 503.
func TestSourceAdmission(t *testing.T) {
	h := newHarness(t)
	var admitted atomic.Int32
	release := make(chan struct{})
	h.srv.admitted = func() {
		admitted.Add(1)
		<-release
	}
	target := "/app/source/tree/" + h.tip() + "/"
	get := func(n int) chan *httptest.ResponseRecorder {
		served := make(chan *httptest.ResponseRecorder, n)
		for range n {
			go func() {
				w := httptest.NewRecorder()
				h.srv.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
				served <- w
			}()
		}
		return served
	}

	old := h.active()
	served := get(MaxGit + MaxWaiting)
	eventually(t, "every slot is taken and MaxWaiting requests wait", func() bool {
		return admitted.Load() == MaxGit && len(h.srv.waiting) == MaxWaiting
	})
	if w := h.get(target); w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Errorf("a source request past MaxWaiting answered %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	h.page("/") // a page that runs no git is served still

	tip, bundle := h.commitMain("app: a commit while the server is saturated\n", file{"100644", "saturated.txt", "saturated\n"})
	h.publishMain(exporttest.SignedAt.Add(time.Minute), tip, bundle)
	if _, err := os.Stat(old.Dir); !os.IsNotExist(err) {
		t.Errorf("a generation no request had opened outlived the activation: %v", err)
	}
	close(release)
	for range MaxGit + MaxWaiting {
		if w := <-served; w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "/app/source/tree/"+tip+"/") {
			t.Errorf("a waiting request answered %d, not from the new generation", w.Code)
		}
	}

	// A request that waits past its time answers 503. Those that held
	// the slots meanwhile ran out of time too.
	h.srv.timeout = 200 * time.Millisecond
	h.srv.log = func(error) {}
	admitted.Store(0)
	release = make(chan struct{})
	held := get(MaxGit)
	eventually(t, "every slot is taken", func() bool { return admitted.Load() == MaxGit })
	start := time.Now()
	if w := h.get(target); w.Code != http.StatusServiceUnavailable {
		t.Errorf("a request that waited past its time answered %d", w.Code)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Errorf("a request waited %v past its time of 200ms", waited)
	}
	close(release)
	for range MaxGit {
		if w := <-held; w.Code != http.StatusServiceUnavailable {
			t.Errorf("a request that ran out of time answered %d", w.Code)
		}
	}
}

// stallingListener accepts connections whose writes stall until release
// is closed, while stall is set. Each sends on stalled the first time one
// of its writes stalls.
type stallingListener struct {
	net.Listener
	stall   atomic.Bool
	release chan struct{}
	stalled chan struct{}
}

func (l *stallingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil || !l.stall.Load() {
		return c, err
	}
	return &stallingConn{Conn: c, l: l}, nil
}

type stallingConn struct {
	net.Conn
	l    *stallingListener
	once sync.Once
}

func (c *stallingConn) Write(p []byte) (int, error) {
	c.once.Do(func() { c.l.stalled <- struct{}{} })
	<-c.l.release
	return c.Conn.Write(p)
}

// Readers that never read their responses hold no git slot and no
// generation: more of them than MaxGit and MaxWaiting together are
// stalled writing, and a new reader is still served, and an activation
// deletes the generation they were served from.
func TestSlowReadersHoldNothing(t *testing.T) {
	h := newHarness(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	n := MaxGit + MaxWaiting + 4
	sl := &stallingListener{Listener: ln, release: make(chan struct{}), stalled: make(chan struct{}, n)}
	sl.stall.Store(true)
	srv := &http.Server{Handler: h.srv}
	go srv.Serve(sl)
	var conns []net.Conn
	defer func() {
		close(sl.release)
		for _, c := range conns {
			c.Close()
		}
		srv.Close()
	}()

	old := h.active()
	target := "/app/source/blob/" + h.tip() + "/docs/guide.md"
	for range n {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: valley-host\r\n\r\n", target)
	}
	for i := range n {
		select {
		case <-sl.stalled:
		case <-time.After(10 * time.Second):
			t.Fatalf("%d of %d slow readers' responses were written; the rest wait", i, n)
		}
	}
	sl.stall.Store(false)
	if len(h.srv.slots) != 0 || len(h.srv.waiting) != 0 {
		t.Errorf("slow readers hold %d git slots, and %d requests wait", len(h.srv.slots), len(h.srv.waiting))
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("http://" + ln.Addr().String() + target)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a new reader answered %d while slow readers stall", resp.StatusCode)
	}
	h.publish(exporttest.SignedAt.Add(time.Minute), nil, func(*export.Export) {})
	if _, err := os.Stat(old.Dir); !os.IsNotExist(err) {
		t.Errorf("the generation slow readers were served from outlived the activation: %v", err)
	}
}

// Before the first activation the server says nothing is published, and
// it answers GET and HEAD alone.
func TestNothingPublishedAndMethods(t *testing.T) {
	store, err := export.OpenStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Store: store, Log: func(err error) { t.Errorf("the server logged: %v", err) }})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "Nothing published yet") {
		t.Errorf("an empty store answered %d:\n%s", w.Code, w.Body)
	}

	h := newHarness(t)
	for _, method := range []string{"POST", "PUT", "DELETE", "OPTIONS"} {
		w := h.do(method, "/")
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s answered %d, allowing %q", method, w.Code, w.Header().Get("Allow"))
		}
	}
}

// tooLarge checks that GET target answers with the one short page that
// says a page is too large.
func (h *harness) tooLarge(target string) {
	h.t.Helper()
	w := h.get(target)
	if w.Code != http.StatusInternalServerError || !bytes.Equal(w.Body.Bytes(), tooLargePage) {
		h.t.Errorf("GET %s answered %d with %d bytes, not the page that says it is too large", target, w.Code, w.Body.Len())
	}
	if n := len(h.srv.budget); n != 0 {
		h.t.Errorf("GET %s kept %d pieces of the budget", target, n)
	}
}

// A response's body is at most MaxResponse bytes. A page that would pass
// the bound answers with the one short page that says it is too large,
// and the failure is logged: a knot whose body passes MaxResponse, and,
// under a smaller bound, a file, a directory, a page of history and the
// plan page. A page within the bound is served on each route.
func TestPageTooLarge(t *testing.T) {
	h := newHarness(t)
	var logged []string
	h.srv.log = func(err error) { logged = append(logged, err.Error()) }
	files := []file{{"100644", "wide.txt", strings.Repeat("<", 16<<10)}}
	for i := range 32 {
		files = append(files, file{"100644", fmt.Sprintf("wide/%02d-%s", i, strings.Repeat("n", 1000)), "x\n"})
	}
	h.commitMain("app: a wide file and a wide directory\n", files...)
	var tip, bundle string
	for i := range 8 {
		tip, bundle = h.commitMain(fmt.Sprintf("%d %s\n", i, strings.Repeat(`"`, maxLogText)))
	}
	h.publish(exporttest.SignedAt.Add(time.Minute), map[string]string{"app": bundle}, func(e *export.Export) {
		p := &e.Projects[0]
		p.SourceTip = tip
		p.Knots[0].Body = strings.Repeat("a", MaxResponse)
		for i := range 100 {
			p.Knots = append(p.Knots, export.Knot{ID: fmt.Sprintf("oc-e%06x", i), Type: "outcome", Status: "open",
				Active: true, State: "ready", Title: strings.Repeat(`"`, knot.MaxTitle), Created: "2026-10-01", CreatedBy: "operator"})
		}
	})

	h.tooLarge("/app/knots/" + h.f.Export.Projects[0].Knots[0].ID)
	h.page("/app/knots/oc-a000002")

	h.srv.maxResponse = 32 << 10
	at := "/app/source/blob/" + tip + "/"
	h.tooLarge(at + "wide.txt")
	h.page(at + "docs/guide.md")
	at = "/app/source/tree/" + tip + "/"
	h.tooLarge(at + "wide/")
	h.page(at + "docs/")
	h.tooLarge("/app/source/log/" + tip)
	h.page("/app/source/log/" + tip + "?skip=8")
	h.tooLarge("/")

	if len(logged) != 5 {
		t.Errorf("%d failures were logged for 5 pages too large: %q", len(logged), logged)
	}
	for _, l := range logged {
		if !strings.Contains(l, "larger than") {
			t.Errorf("the server logged %q", l)
		}
	}
	if len(tooLargePage) > 16<<10 {
		t.Errorf("the page that says a page is too large is %d bytes", len(tooLargePage))
	}
}

// Rendered responses share a budget of MaxBuffered bytes, which each
// holds from when it starts rendering until it is written. What a
// response renders past its first SmallPage bytes takes only MaxLarge of
// it. Readers that never read large pages ask for twice the budget in
// all, with pages sized so that, but for MaxLarge, sixteen of them would
// hold all of it. Once their pages hold MaxLarge, a large page answers
// 503 at once, while small pages still render: the plan, a knot and a
// directory. The server's heap grows by no more than the budget, and the
// budget is whole again once the readers read.
func TestSlowReadersShareTheBudget(t *testing.T) {
	h := newHarness(t)
	target := "/app/knots/" + h.f.Export.Projects[0].Knots[0].ID
	body := func(at time.Duration, text string) {
		h.publish(exporttest.SignedAt.Add(at), nil, func(e *export.Export) { e.Projects[0].Knots[0].Body = text })
	}
	body(time.Minute, "x")
	rest := len(h.page(target)) - 1
	pieces := 64
	body(2*time.Minute, strings.Repeat("a line of a long body\n", (pieces*pieceSize-rest-1024)/22))
	size := len(h.page(target))
	if got := (size + pieceSize - 1) / pieceSize; got != pieces || cap(h.srv.budget)%pieces != 0 {
		t.Fatalf("the large page takes %d pieces, not %d, which divide the budget", got, pieces)
	}
	fit := cap(h.srv.large) / (pieces - smallPieces)
	n := 2*MaxBuffered/size + 1

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sl := &stallingListener{Listener: ln, release: make(chan struct{}), stalled: make(chan struct{}, n)}
	sl.stall.Store(true)
	srv := &http.Server{Handler: h.srv}
	go srv.Serve(sl)
	var conns []net.Conn
	released := false
	defer func() {
		if !released {
			close(sl.release)
		}
		for _, c := range conns {
			c.Close()
		}
		srv.Close()
	}()

	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	// One reader at a time, each stalled before the next asks, so the
	// readers that fill the budget are the first fit of them.
	for i := range n {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: valley-host\r\n\r\n", target)
		select {
		case <-sl.stalled:
		case <-time.After(10 * time.Second):
			t.Fatalf("reader %d of %d was not answered", i+1, n)
		}
	}
	sl.stall.Store(false)
	var after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&after)
	if held, large := len(h.srv.budget), len(h.srv.large); held != fit*pieces || large != fit*(pieces-smallPieces) {
		t.Errorf("slow readers hold %d pieces, %d of them large; %d pages of %d pieces fit", held, large, fit, pieces)
	}
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > MaxBuffered+8<<20 {
		t.Errorf("%d slow readers of %d bytes each grew the heap by %d bytes, past MaxBuffered", n, size, grew)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	get := func(target string) (int, string) {
		t.Helper()
		resp, err := client.Get("http://" + ln.Addr().String() + target)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, resp.Header.Get("Retry-After")
	}
	if code, retry := get(target); code != http.StatusServiceUnavailable || retry == "" {
		t.Errorf("a page the budget has no room for answered %d, Retry-After %q", code, retry)
	}
	for _, small := range []string{"/", "/app/knots/oc-a000002", "/app/source/tree/" + h.tip() + "/"} {
		if code, _ := get(small); code != http.StatusOK {
			t.Errorf("the small page %s answered %d while slow readers hold MaxLarge", small, code)
		}
	}

	close(sl.release)
	released = true
	codes := map[int]int{}
	for _, c := range conns {
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			t.Fatal(err)
		}
		read, err := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode == http.StatusOK && read != int64(size) {
			t.Errorf("a slow reader read %d bytes of %d: %v", read, size, err)
		}
		codes[resp.StatusCode]++
	}
	if codes[http.StatusOK] != fit || codes[http.StatusServiceUnavailable] != n-fit {
		t.Errorf("of %d slow readers, %d were served and %d answered 503; %d pages fit the budget",
			n, codes[http.StatusOK], codes[http.StatusServiceUnavailable], fit)
	}
	eventually(t, "the budget is whole again", func() bool { return len(h.srv.budget) == 0 })
}

// A load starts only once a request that waits for it takes a loader
// slot. While MaxLoads loads run, a request for a newer generation waits
// for a slot, holding no generation, until its time runs out, whatever
// its route, or its reader goes away. Neither leaves a load behind.
func TestExportLoadWaitsEnd(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	var logged []string
	h.srv.log = func(err error) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, err.Error())
	}
	loads := map[int64]int{}
	hold := make(chan struct{})
	h.srv.loading = func(g export.Generation) {
		mu.Lock()
		loads[g.Sequence]++
		mu.Unlock()
		<-hold
	}
	loaded := func(g export.Generation) int {
		mu.Lock()
		defer mu.Unlock()
		return loads[g.Sequence]
	}
	serve := func(r *http.Request) chan *httptest.ResponseRecorder {
		served := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			w := httptest.NewRecorder()
			h.srv.ServeHTTP(w, r)
			served <- w
		}()
		return served
	}
	expect := func(served chan *httptest.ResponseRecorder, title string) {
		t.Helper()
		select {
		case w := <-served:
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), title) {
				t.Errorf("a request answered %d, not with %q", w.Code, title)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("a request was not answered with %q", title)
		}
	}

	first := h.active()
	firstServed := serve(httptest.NewRequest("GET", "/", nil))
	eventually(t, "the first generation is read", func() bool { return loaded(first) == 1 })
	second := h.publish(exporttest.SignedAt.Add(time.Minute), nil, renamed("Readers browse the second plan"))
	secondServed := serve(httptest.NewRequest("GET", "/", nil))
	eventually(t, "both loader slots are taken", func() bool { return loaded(second) == 1 })

	// A request that runs no git runs out of time waiting, and answers
	// 503.
	h.srv.timeout = 200 * time.Millisecond
	third := h.publish(exporttest.SignedAt.Add(2*time.Minute), nil, renamed("Readers browse the third plan"))
	select {
	case w := <-serve(httptest.NewRequest("GET", "/app/knots/oc-a000001", nil)):
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
			t.Errorf("a request that waited past its time for a read answered %d", w.Code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a request waits for a read past its time of 200ms")
	}

	// A request whose reader goes away stops waiting, and answers
	// nothing.
	h.srv.timeout = time.Hour
	fourth := h.publish(exporttest.SignedAt.Add(3*time.Minute), nil, renamed("Readers browse the fourth plan"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gone := serve(httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	eventually(t, "a request waits for the fourth generation", func() bool { return len(h.srv.loadWaiting) == 3 })
	cancel()
	select {
	case w := <-gone:
		if w.Body.Len() != 0 {
			t.Errorf("a request whose reader went away answered %d", w.Code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a request whose reader went away still waits")
	}

	h.srv.mu.Lock()
	left := len(h.srv.loads)
	h.srv.mu.Unlock()
	if left != MaxLoads {
		t.Errorf("%d loads may be joined; only the %d that run are wanted", left, MaxLoads)
	}
	h.publish(exporttest.SignedAt.Add(4*time.Minute), nil, renamed("Readers browse the fifth plan"))
	for _, g := range []export.Generation{third, fourth} {
		if _, err := os.Stat(g.Dir); !os.IsNotExist(err) {
			t.Errorf("generation %d outlived an activation after its request gave up: %v", g.Sequence, err)
		}
	}

	// The first two reads end, and their requests are served the newest
	// generation.
	close(hold)
	expect(firstServed, "Readers browse the fifth plan")
	expect(secondServed, "Readers browse the fifth plan")
	for _, g := range []export.Generation{third, fourth} {
		if n := loaded(g); n != 0 {
			t.Errorf("generation %d's export was read %d times after every request for it gave up", g.Sequence, n)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 || !strings.Contains(logged[0], "deadline exceeded") {
		t.Errorf("the server logged %q; it logs the one request that ran out of time", logged)
	}
}

// A load that reads slowly does not hold the request that waits for it:
// the request answers 503 at its deadline. With no request left waiting,
// the load stops reading part way through the export, caches nothing, and
// gives back its generation, which the next activation deletes. A load
// that ends is kept, and the requests after it are served without another
// read.
func TestSlowLoadEndsWithItsRequests(t *testing.T) {
	h := newHarness(t)
	h.srv.timeout = 200 * time.Millisecond
	var mu sync.Mutex
	var logged []string
	h.srv.log = func(err error) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, err.Error())
	}
	var reads atomic.Int32
	h.srv.loading = func(export.Generation) { reads.Add(1) }

	// The active generation's export.json becomes a pipe the test writes
	// a few bytes at a time, as a slow disk would give it: all of it
	// takes about 4 seconds, twenty times the request's time.
	old := h.active()
	data, err := os.ReadFile(old.ExportPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(old.ExportPath()); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(old.ExportPath(), 0o600); err != nil {
		t.Fatal(err)
	}
	piece := max(1, len(data)/200)
	wrote := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(old.ExportPath(), os.O_WRONLY, 0)
		if err != nil {
			wrote <- err
			return
		}
		defer f.Close()
		for p := data; len(p) > 0; p = p[min(len(p), piece):] {
			if _, err := f.Write(p[:min(len(p), piece)]); err != nil {
				wrote <- err
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		wrote <- nil
	}()

	start := time.Now()
	if w := h.get("/"); w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Errorf("a request whose load ran past its time answered %d", w.Code)
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Errorf("a request waited %v for a slow load, past its time of 200ms", waited)
	}
	select {
	case err := <-wrote:
		if !errors.Is(err, syscall.EPIPE) {
			t.Errorf("the load read the whole export after its request gave up: the writer ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the load still reads the export")
	}
	eventually(t, "the load ends", func() bool { return len(h.srv.loaders) == 0 })
	h.srv.mu.Lock()
	cached, loads := h.srv.cached, len(h.srv.loads)
	h.srv.mu.Unlock()
	if cached != nil || loads != 0 {
		t.Errorf("a load that stopped left a snapshot %v and %d loads", cached != nil, loads)
	}
	// The store reads the active export.json when it takes an upload.
	if err := os.Remove(old.ExportPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old.ExportPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	h.publish(exporttest.SignedAt.Add(time.Minute), nil, renamed("Readers browse the second plan"))
	if _, err := os.Stat(old.Dir); !os.IsNotExist(err) {
		t.Errorf("the generation a stopped load opened outlived an activation: %v", err)
	}

	h.srv.timeout = requestTimeout
	for range 3 {
		if !strings.Contains(h.page("/app/knots/oc-a000001"), "Readers browse the second plan") {
			t.Error("a request after the stopped load is not served the new generation")
		}
	}
	if n := reads.Load(); n != 2 {
		t.Errorf("exports were read %d times: once in part, and once for the new generation", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 || !strings.Contains(logged[0], "export to be read: context deadline exceeded") {
		t.Errorf("the server logged %q; it logs the one request that ran out of time", logged)
	}
}

// openFiles is the number of files the process holds open, and false
// where the system does not list them.
func openFiles(t *testing.T) (int, bool) {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, false
	}
	return len(entries), true
}

// A flood of requests while a load runs: MaxLoadWaiting of them wait, and
// the rest answer 503 at once, unlogged. A waiting request holds no
// generation, so the goroutines and files the server holds stay within a
// bound however large the flood. Once the load ends, every request that
// waited is served from its one read.
func TestLoadFlood(t *testing.T) {
	h := newHarness(t)
	var reads atomic.Int32
	reading, hold := make(chan struct{}, 1), make(chan struct{})
	h.srv.loading = func(export.Generation) {
		reads.Add(1)
		reading <- struct{}{}
		<-hold
	}
	get := func(n int) chan *httptest.ResponseRecorder {
		served := make(chan *httptest.ResponseRecorder, n)
		for range n {
			go func() {
				w := httptest.NewRecorder()
				h.srv.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
				served <- w
			}()
		}
		return served
	}

	first := get(1)
	select {
	case <-reading:
	case <-time.After(10 * time.Second):
		t.Fatal("no load started")
	}
	goroutines := runtime.NumGoroutine()
	files, listed := openFiles(t)

	flood := 4 * MaxLoadWaiting
	served := get(flood)
	refused := flood - (MaxLoadWaiting - 1)
	for range refused {
		if w := <-served; w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
			t.Fatalf("a request past MaxLoadWaiting answered %d", w.Code)
		}
	}
	if n := len(h.srv.loadWaiting); n != MaxLoadWaiting {
		t.Errorf("%d requests wait for the load, not %d", n, MaxLoadWaiting)
	}
	const slack = 8
	eventually(t, "the refused requests' goroutines end", func() bool {
		return runtime.NumGoroutine() <= goroutines+MaxLoadWaiting-1+slack
	})
	if n, _ := openFiles(t); listed && n > files+slack {
		t.Errorf("the flood holds %d files open, %d more than before it", n, n-files)
	}

	close(hold)
	for range MaxLoadWaiting - 1 {
		if w := <-served; w.Code != http.StatusOK {
			t.Errorf("a request that waited for the load answered %d", w.Code)
		}
	}
	if w := <-first; w.Code != http.StatusOK {
		t.Errorf("the request that started the load answered %d", w.Code)
	}
	if n := reads.Load(); n != 1 {
		t.Errorf("the export was read %d times for one generation", n)
	}
	if n := len(h.srv.loadWaiting); n != 0 {
		t.Errorf("%d requests still hold a place among those that wait", n)
	}
}

// A request's time runs from its arrival through its rendering: a request
// for a page that runs no git, held past its time before it renders,
// answers 503, and the server logs it.
func TestRenderingIsWithinTheDeadline(t *testing.T) {
	h := newHarness(t)
	h.page("/")
	h.srv.timeout = 50 * time.Millisecond
	var logged []string
	h.srv.log = func(err error) { logged = append(logged, err.Error()) }
	h.srv.opened = func(export.Generation) { time.Sleep(100 * time.Millisecond) }
	if w := h.get("/"); w.Code != http.StatusServiceUnavailable || !bytes.Equal(w.Body.Bytes(), busyPage) {
		t.Errorf("a request that ran out of time before it rendered answered %d", w.Code)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "rendering plan: context deadline exceeded") {
		t.Errorf("the server logged %q", logged)
	}
	if n := len(h.srv.budget); n != 0 {
		t.Errorf("a page that stopped rendering kept %d pieces of the budget", n)
	}
}

// A FIFO with a writer that sends no bytes keeps Read blocked until the
// request deadline. A FIFO without a writer must not block Open.
func TestStalledLoadReleasesGeneration(t *testing.T) {
	for _, writer := range []bool{false, true} {
		t.Run(fmt.Sprint(writer), func(t *testing.T) {
			h := newHarness(t)
			h.srv.timeout = 100 * time.Millisecond
			h.srv.log = func(error) {}
			old := h.active()
			data, err := os.ReadFile(old.ExportPath())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(old.ExportPath()); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(old.ExportPath(), 0600); err != nil {
				t.Fatal(err)
			}
			if writer {
				f, err := os.OpenFile(old.ExportPath(), os.O_RDWR|syscall.O_NONBLOCK, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if _, err := f.Write([]byte("{")); err != nil {
					t.Fatal(err)
				}
			}
			if w := h.get("/"); w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d", w.Code)
			}
			eventually(t, "the stalled worker exits", func() bool { return len(h.srv.loaders) == 0 })
			h.srv.mu.Lock()
			cached := h.srv.cached
			h.srv.mu.Unlock()
			if cached != nil {
				t.Fatal("stalled load cached a snapshot")
			}
			if err := os.Remove(old.ExportPath()); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(old.ExportPath(), data, 0644); err != nil {
				t.Fatal(err)
			}
			h.publish(exporttest.SignedAt.Add(time.Minute), nil, renamed("The second plan"))
			if _, err := os.Stat(old.Dir); !os.IsNotExist(err) {
				t.Fatalf("generation retained: %v", err)
			}
			h.srv.timeout = requestTimeout
			if !strings.Contains(h.page("/app/knots/oc-a000001"), "The second plan") {
				t.Fatal("loader slot is not reusable")
			}
		})
	}
}

// cancelInPhase cancels a real context on the third cancellation check
// inside a named processing function. It makes cancellation during CPU
// work deterministic without replacing decoding or graph traversal.
type cancelInPhase struct {
	context.Context
	cancel context.CancelFunc
	phase  string
	checks int
}

func (c *cancelInPhase) Err() error {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, c.phase) {
			c.checks++
			if c.checks == 3 {
				c.cancel()
			}
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}

func TestProcessingCancellationReleasesGeneration(t *testing.T) {
	for _, phase := range []string{".decodeValue", ".(*Export).ValidateContext", ".(*index).planPage", ".(*index).paths", ".(*index).milestones"} {
		t.Run(phase, func(t *testing.T) {
			h := newHarness(t)
			old := h.active()
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelInPhase{Context: base, cancel: cancel, phase: phase}
			l := &load{done: make(chan struct{}), cancel: cancel}
			h.srv.loads[l] = struct{}{}
			h.srv.loaders <- struct{}{}
			finished := make(chan struct{})
			go func() { h.srv.run(ctx, l); close(finished) }()
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Fatal("processing did not stop")
			}
			if ctx.checks < 3 {
				t.Fatalf("phase was not reached: %d checks", ctx.checks)
			}
			if !errors.Is(l.err, context.Canceled) {
				t.Fatalf("load error = %v", l.err)
			}
			if h.srv.cached != nil || len(h.srv.loaders) != 0 || len(h.srv.loads) != 0 {
				t.Fatal("cancelled processing retained load state")
			}
			h.publish(exporttest.SignedAt.Add(time.Minute), nil, renamed("The second plan"))
			if _, err := os.Stat(old.Dir); !os.IsNotExist(err) {
				t.Fatalf("generation retained: %v", err)
			}
			if !strings.Contains(h.page("/app/knots/oc-a000001"), "The second plan") {
				t.Fatal("loader slot is not reusable")
			}
		})
	}
}
