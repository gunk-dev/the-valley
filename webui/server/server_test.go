package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

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
// changed by change, with the bundles given in place of the fixture's.
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
	for name, path := range h.f.Bundles {
		paths[name] = path
	}
	for name, path := range bundles {
		paths[name] = path
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
		"waits on oc-a000002",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the plan page does not show %q", want)
		}
	}
	if strings.Count(page, "Ready changes") != 1 {
		t.Error("the ready changes are not one card")
	}

	// The bug nobody claims and nothing blocks is free to start.
	if !strings.Contains(page, `<i class="k ready" role="img" aria-label="free to start" title="free to start"></i><span class="body"><a class="id" href="/knots/app/bd-a000004">`) {
		t.Error("bd-a000004 is not shown as free to start")
	}

	// The milestone's path puts oc-a000003 after the knot it waits on.
	paths := section(t, page, `id="paths-h"`, `id="ms-h"`)
	second := strings.Index(paths, `<span class="stage-num">2</span>`)
	if second < 0 || strings.Index(paths, "oc-a000002") > second || strings.Index(paths, "oc-a000003") < second {
		t.Errorf("the path does not order oc-a000003 after oc-a000002:\n%s", paths)
	}

	// Nothing of the hidden project is linked, and nothing is fetched
	// from another origin.
	for _, unwanted := range []string{"/knots/site/", "/source/site/", "http://", "https://", `class="chip stale"`} {
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

// A knot's page shows its body and comments, marks the edited comment,
// and names a blocker in a hidden project without linking it.
func TestKnotPage(t *testing.T) {
	h := newHarness(t)
	page := h.page("/knots/app/oc-a000002")
	for _, want := range []string{
		"Each milestone is listed with its progress.",
		"Start with the milestones that have open children.",
		"Claimed; the change is ready for approval.",
		`<span class="chip quiet">edited</span><span>2026-10-25 10:30 UTC by agent</span>`,
		`<a href="/knots/app/oc-a000001">Readers browse the app&#39;s plan</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the knot's page does not show %q", want)
		}
	}
	if n := strings.Count(page, ">edited<"); n != 1 {
		t.Errorf("%d comments are marked edited; one was edited", n)
	}

	page = h.page("/knots/app/oc-a000003")
	if !strings.Contains(page, `<a href="/knots/app/oc-a000002">The plan page lists milestones</a>`) {
		t.Error("the local blocker is not linked")
	}
	if !strings.Contains(page, `<span class="mono">site:oc-b000001</span> <span class="muted">not published</span>`) {
		t.Error("the hidden blocker is not named as not published")
	}
	if strings.Contains(page, "/knots/site/") || strings.Contains(page, "/source/site/") {
		t.Error("the knot's page links into the hidden project")
	}
	if w := h.get("/knots/app/oc-fffffff"); w.Code != http.StatusNotFound {
		t.Errorf("a missing knot answered %d", w.Code)
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
	notFound := h.get("/knots/app/oc-fffffff")
	for _, route := range []string{
		"/knots/%s/oc-b000001",
		"/source/%s",
		"/source/%s/",
		"/source/%s/tree/" + siteTip + "/",
		"/source/%s/blob/" + siteTip + "/README.md",
		"/source/%s/log/" + siteTip,
		"/source/%s/tree/" + siteTip + "/no/such/path/",
		"/source/%s/anything",
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
	notFound := h.get("/knots/app/oc-fffffff").Body.String()

	for _, commit := range []string{side, tree, readme, tip[:12], "main", "HEAD", strings.Repeat("0", 40), tip + "~1"} {
		for _, route := range []string{"/source/app/tree/%s/", "/source/app/blob/%s/README.md", "/source/app/log/%s"} {
			target := fmt.Sprintf(route, commit)
			w := h.get(target)
			if w.Code != http.StatusNotFound || w.Body.String() != notFound {
				t.Errorf("GET %s answered %d, not the one 404", target, w.Code)
			}
		}
	}

	// A commit the tip reaches is served, as that commit had it.
	page := h.page("/source/app/blob/" + parent + "/README.md")
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

	w := h.get("/source/app/")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/source/app/tree/"+tip+"/" {
		t.Fatalf("the project's source answered %d to %q", w.Code, w.Header().Get("Location"))
	}

	root := h.page("/source/app/tree/" + tip + "/")
	docs, src, readme := strings.Index(root, ">docs/<"), strings.Index(root, ">src/<"), strings.Index(root, ">README.md<")
	if docs < 0 || src < 0 || readme < 0 || readme < src || src < docs {
		t.Errorf("the root does not list docs/ and src/ before README.md:\n%s", root)
	}
	if !strings.Contains(root, `<pre class="text"># app`) {
		t.Error("the root does not show its README")
	}
	if dir := h.page("/source/app/tree/" + tip + "/docs/"); !strings.Contains(dir, ">guide.md<") {
		t.Error("docs/ does not list guide.md")
	}
	file := h.page("/source/app/blob/" + tip + "/docs/guide.md")
	if !strings.Contains(file, `<tr id="L1"><td class="ln"><a href="#L1">1</a></td><td class="src"># Guide</td></tr>`) {
		t.Errorf("the file is not shown line by line:\n%s", file)
	}
	log := h.page("/source/app/log/" + tip)
	first, last := strings.Index(log, "docs: a guide, and the README names it"), strings.Index(log, "app: the first README")
	if first < 0 || last < first {
		t.Errorf("the log does not list main's commits newest first:\n%s", log)
	}

	links := regexp.MustCompile(`href="(/source/[^"]*)"`)
	named := regexp.MustCompile(`^/source/app/(tree|blob|log)/[0-9a-f]{40}(/|$)`)
	for _, page := range []string{root, file, log} {
		for _, m := range links.FindAllStringSubmatch(page, -1) {
			if !named.MatchString(m[1]) {
				t.Errorf("the link %s names no commit", m[1])
			}
		}
	}

	for _, target := range []string{
		"/source/app/tree/" + tip + "/README.md/",
		"/source/app/blob/" + tip + "/docs",
		"/source/app/blob/" + tip + "/docs/guide.md/",
		"/source/app/blob/" + tip + "/docs/../README.md",
		"/source/app/blob/" + tip + "/docs%2Fguide.md",
		"/source/app/tree/" + tip + "//",
		"/source/app/log/" + tip + "?skip=-1",
	} {
		if w := h.get(target); w.Code != http.StatusNotFound {
			t.Errorf("GET %s answered %d", target, w.Code)
		}
	}
}

// hostileMain writes a commit onto the fixture's app main that holds a
// file of HTML, a file larger than a page shows, a binary file, a file
// whose name is HTML, and a symbolic link out of the repository. It
// returns the new tip and a bundle of it.
func (h *harness) hostileMain() (tip, bundle string) {
	t := h.t
	repo := h.f.Repos["app"]
	var b bytes.Buffer
	data := func(s string) { fmt.Fprintf(&b, "data %d\n%s\n", len(s), s) }
	who := "Example Author <author@example.org> 1791190800 +0000"
	fmt.Fprintf(&b, "commit refs/heads/main\nauthor %s\ncommitter %s\n", who, who)
	data("hostile: <script>alert(\"subject\")</script>\n")
	fmt.Fprintf(&b, "from %s\n", h.tip())
	for _, f := range []struct{ mode, path, content string }{
		{"100644", "page.html", "<html><script>alert(\"file\")</script><img src=x onerror=alert(2)></html>\n"},
		{"100644", "big.txt", strings.Repeat("big\n", (MaxFile/4)+1)},
		{"100644", "binary.bin", "\x7fELF\x00\x01\x02<script>alert(\"binary\")</script>"},
		{"100644", "<img src=x onerror=alert(3)>.txt", "named\n"},
		{"120000", "link", "../../etc/passwd"},
	} {
		fmt.Fprintf(&b, "M %s inline %s\n", f.mode, f.path)
		data(f.content)
	}
	git(t, repo, b.Bytes(), "fast-import", "--quiet")
	tip = git(t, repo, nil, "rev-parse", export.MainRef)
	bundle = filepath.Join(t.TempDir(), "app.bundle")
	git(t, repo, nil, "bundle", "create", "--quiet", bundle, "main")
	return tip, bundle
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
	at := "/source/app/tree/" + tip + "/"
	inert(at, h.page(at),
		`&lt;script&gt;alert(&#34;readme&#34;)&lt;/script&gt;`,
		`&lt;img src=x onerror=alert(3)&gt;.txt`,
		`href="/source/app/blob/`+tip+`/%3Cimg%20src=x%20onerror=alert%283%29%3E.txt"`)
	at = "/source/app/blob/" + tip + "/%3Cimg%20src=x%20onerror=alert%283%29%3E.txt"
	inert(at, h.page(at), ">named<")
	at = "/source/app/blob/" + tip + "/page.html"
	inert(at, h.page(at), `&lt;html&gt;&lt;script&gt;alert(&#34;file&#34;)&lt;/script&gt;&lt;img src=x onerror=alert(2)&gt;&lt;/html&gt;`)
	at = "/source/app/blob/" + tip + "/big.txt"
	page := h.page(at)
	inert(at, page, "This file is 1.0 MiB, larger than the 1.0 MiB a page shows.")
	if len(page) > 64<<10 {
		t.Errorf("the large file's page is %d bytes", len(page))
	}
	at = "/source/app/blob/" + tip + "/binary.bin"
	inert(at, h.page(at), "This file is binary, 39 bytes, and is not shown.")
	at = "/source/app/blob/" + tip + "/link"
	inert(at, h.page(at), "A symbolic link to ../../etc/passwd. It is not followed.")
	at = "/source/app/log/" + tip
	inert(at, h.page(at), "hostile: &lt;script&gt;alert(&#34;subject&#34;)&lt;/script&gt;")

	at = "/knots/app/bd-a000004"
	inert(at, h.page(at),
		`A README holding &lt;script&gt;alert(&#34;knot&#34;)&lt;/script&gt; must show as text.`,
		`Also &lt;img src=x onerror=alert(1)&gt; in a comment.`)
}

// Every response carries the strict policy, and the policy admits the
// page's one inline stylesheet and one inline script by their hashes and
// nothing from another origin.
func TestEveryResponseCarriesTheCSP(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct{ method, target string }{
		{"GET", "/"},
		{"GET", "/knots/app/oc-a000002"},
		{"GET", "/source/app/"},
		{"GET", "/source/app/tree/" + h.tip() + "/"},
		{"GET", "/source/app/blob/" + h.tip() + "/README.md"},
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
		h.srv.ServeHTTP(w, httptest.NewRequest("GET", "/knots/app/oc-a000001", nil))
		served <- w
	}()
	old := <-paused

	renamed := func(title string) func(*export.Export) {
		return func(e *export.Export) { e.Projects[0].Knots[0].Title = title }
	}
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
	if !strings.Contains(h.page("/knots/app/oc-a000001"), "Readers browse the second plan") {
		t.Error("the next request does not serve the new generation")
	}

	// Released, the old generation goes with the next activation.
	h.publish(exporttest.SignedAt.Add(2*time.Minute), nil, renamed("Readers browse the third plan"))
	if _, err := os.Stat(old.Dir); !os.IsNotExist(err) {
		t.Errorf("the released generation is still on disk: %v", err)
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
