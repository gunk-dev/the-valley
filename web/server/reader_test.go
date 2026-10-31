package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"the-valley/web/export"
	"the-valley/web/export/exporttest"
)

// readerKey is the request context's key under which a test names the
// reader a request is read as.
type readerKey struct{}

// as answers a request for target read as reader, with header's fields.
func (h *harness) as(reader, method, target string, header http.Header) *httptest.ResponseRecorder {
	h.t.Helper()
	r := httptest.NewRequest(method, target, nil)
	for k, vs := range header {
		r.Header[k] = vs
	}
	r = r.WithContext(context.WithValue(r.Context(), readerKey{}, reader))
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, r)
	return w
}

// pageAs is the body of a GET read as reader that must answer 200.
func (h *harness) pageAs(reader, target string) string {
	h.t.Helper()
	w := h.as(reader, "GET", target, nil)
	if w.Code != http.StatusOK {
		h.t.Fatalf("GET %s as %s answered %d:\n%s", target, reader, w.Code, w.Body)
	}
	return w.Body.String()
}

// The publication identities these tests give site, which the fixture
// does not export, and docs, which the tests make of site.
const (
	siteIdentity = "273d5668702918ea7ae079de374db96b"
	docsIdentity = "5b1e0c7a9f3d2e4c6a8b0d1f3e5c7a92"
)

// buildExport is the fixture's valley exported by export.Build, as a
// publisher builds it, with its plan changed by change first, when change
// is not nil: change is given the plan's knot of a project by ID. With
// site set, site is exported too, as for an edge where site has a named
// reader, and bundles holds its bundle.
func (h *harness) buildExport(site bool, change func(knot func(project, id string) map[string]any)) (e *export.Export, bundles map[string]string) {
	t := h.t
	t.Helper()
	var plan map[string]any
	if err := json.Unmarshal(h.f.Inputs.Plan, &plan); err != nil {
		t.Fatal(err)
	}
	project := func(name string) map[string]any {
		for _, p := range plan["projects"].([]any) {
			if p := p.(map[string]any); p["project"] == name {
				return p
			}
		}
		t.Fatalf("the fixture's plan has no project %s", name)
		return nil
	}
	knot := func(name, id string) map[string]any {
		for _, k := range project(name)["knots"].([]any) {
			if k := k.(map[string]any); k["id"] == id {
				return k
			}
		}
		t.Fatalf("the fixture's plan has no knot %s:%s", name, id)
		return nil
	}
	if change != nil {
		change(knot)
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	in := h.f.Inputs
	in.Plan = data
	in.Identities = map[string]string{"app": exporttest.Identities["app"]}
	in.SourceTips = map[string]string{"app": h.f.Inputs.SourceTips["app"]}
	in.Details = slices.Clone(h.f.Inputs.Details)
	bundles = map[string]string{}
	if site {
		in.Identities["site"] = siteIdentity
		in.SourceTips["site"] = git(t, h.f.Repos["site"], nil, "rev-parse", export.MainRef)
		for _, k := range project("site")["knots"].([]any) {
			id := k.(map[string]any)["id"].(string)
			detail, err := json.Marshal(map[string]any{"schema": "valley-knot/v1", "project": "site", "tip": project("site")["tip"],
				"knot": map[string]any{"id": id, "body": "What site says of " + id + ".\n", "comments": []any{}}})
			if err != nil {
				t.Fatal(err)
			}
			in.Details = append(in.Details, detail)
		}
		bundles["site"] = filepath.Join(t.TempDir(), "site.bundle")
		git(t, h.f.Repos["site"], nil, "bundle", "create", "--quiet", bundles["site"], "main")
	}
	if e, err = export.Build(in); err != nil {
		t.Fatal(err)
	}
	return e, bundles
}

// withDocs adds to e a project docs, a copy of site with an identity of
// its own, in a group of its own, pages, whose alias is p. docs was
// renamed from old-docs, and has the short alias d. Its bundle is site's.
func withDocs(e *export.Export, bundles map[string]string) {
	var site export.Project
	for _, p := range e.Projects {
		if p.Name == "site" {
			site = p
		}
	}
	docs := site
	docs.Name, docs.Identity, docs.Group, docs.WebName = "docs", docsIdentity, "web", "docs"
	docs.FormerNames, docs.Aliases = []string{"old-docs"}, []string{"d"}
	e.Projects = append(e.Projects, docs)
	sort.Slice(e.Projects, func(i, j int) bool { return e.Projects[i].Name < e.Projects[j].Name })
	e.Groups = append(e.Groups, export.Group{ID: "web", Name: "pages", Aliases: []string{"p"}})
	bundles["docs"] = bundles["site"]
}

// authorizeReaders stores an authorization one sequence past the stored
// one's that lets the readers given for each project of e read it, under
// e's identity. A project given no reader is not named. The policy check's
// time is kept, so the pages' freshness does not change.
func (h *harness) authorizeReaders(e *export.Export, readers map[string][]string) {
	h.t.Helper()
	a, _, err := h.store.Authorization()
	if err != nil {
		h.t.Fatal(err)
	}
	next := &export.Authorization{Instance: exporttest.Instance, Destination: exporttest.Destination,
		Sequence: a.Sequence + 1, Checked: a.Checked}
	for _, p := range e.Projects {
		if rs := readers[p.Name]; len(rs) > 0 {
			next.Projects = append(next.Projects, export.AuthorizedProject{Name: p.Name, Identity: p.Identity, Readers: rs})
		}
	}
	h.authorize(next)
}

// edge activates, signed at, an export that holds app, site and docs, as
// an edge holds them for site's and docs' named readers, and stores an
// authorization that lets the readers given for each read it. It returns
// the export.
func (h *harness) edge(at time.Time, readers map[string][]string) *export.Export {
	h.t.Helper()
	e, bundles := h.buildExport(true, nil)
	withDocs(e, bundles)
	h.authorizeReaders(e, readers)
	h.publishExport(at, e, bundles)
	return e
}

// anonymousIndex is the index of the newest generation's view of what
// anyone may see, under the last authorization read.
func (s *Server) anonymousIndex() *index {
	s.mu.Lock()
	defer s.mu.Unlock()
	var newest *view
	for _, v := range s.views {
		if v.idx != nil && maps.Equal(v.shown, visible(v.e, s.auth.a, export.Anyone)) && (newest == nil || v.key.seq > newest.key.seq) {
			newest = v
		}
	}
	if newest == nil {
		return nil
	}
	return newest.idx
}

// heldSlots is how many source requests hold a git slot, and
// waitingForSlots how many wait for one.
func (s *Server) heldSlots() int {
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	return s.running
}

func (s *Server) waitingForSlots() int {
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	return len(s.queue)
}

// runningLoads is how many loader slots loads hold.
func (s *Server) runningLoads() int { return len(s.anonLoader) + len(s.loaders) }

// loadWaiters counts the requests that wait for the loads a request may
// still join, anonymous and named apart.
func (s *Server) loadWaiters() (anonymous, named int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for l := range s.loads {
		anonymous += l.anon
		named += l.wants - l.anon
	}
	return anonymous, named
}

// stoppingViews counts the views whose builds were stopped and whose
// workers have not ended.
func (s *Server) stoppingViews() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.stopping)
}

// heldViews counts the places of each pool that the views the server
// holds take, and the pins on all of them. A stopped build's place is not
// counted.
func (s *Server) heldViews() (anonymous, named, pins int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.views {
		if v.holds[anonymousPool] {
			anonymous++
		}
		if v.holds[namedPool] {
			named++
		}
		pins += v.pins[anonymousPool] + v.pins[namedPool]
	}
	return anonymous, named, pins
}

// Each reader is served from the projects they may see: what anyone may
// read, app, and what their principal may read, site for alice and docs
// for bob. For each reader, every route, the navigation, the landing, the
// projects page, each group's page, each project's knots page, the
// aliases, the former names, the references between projects, the
// rendered Markdown and the archives are built from those projects alone.
// A project the reader may not see answers every route exactly as one that
// does not exist, takes no git slot, and its group, when none of the
// group's projects is the reader's, has no page and no alias.
func TestNamedReadersSeeTheirOwnProjects(t *testing.T) {
	h := newHarness(t)
	h.edge(exporttest.SignedAt.Add(time.Minute), map[string][]string{
		"app": {export.Anyone}, "site": {"alice"}, "docs": {"bob"}})
	appTip, siteTip := h.tip(), git(t, h.f.Repos["site"], nil, "rev-parse", export.MainRef)
	var admitted atomic.Int32
	h.srv.admitted = func() { admitted.Add(1) }

	type project struct{ base, tip, readme string }
	projects := map[string]project{
		"app":  {"/tools/app", appTip, `<h1 id="app">`},
		"site": {"/tools/site", siteTip, `<h1 id="site">`},
		"docs": {"/pages/docs", siteTip, `<h1 id="site">`},
	}
	readers := map[string][]string{export.Anyone: {"app"}, "alice": {"app", "site"}, "bob": {"app", "docs"}}
	for reader, sees := range readers {
		t.Run(reader, func(t *testing.T) {
			// The navigation, the knots page and the landing: a reader who
			// may see app alone lands on its tree, and one who may see more
			// on the projects page.
			plan := h.pageAs(reader, "/-/knots")
			if nav := section(t, plan, `<nav class="site"`, `<span class="fresh">`); nav != `<nav class="site" aria-label="Pages"><a href="/-/projects">projects</a>` {
				t.Errorf("the navigation is\n%s", nav)
			}
			if !strings.Contains(plan, "Policy last checked") {
				t.Error("the knots page does not say when the policy was checked")
			}
			landing := "/-/projects"
			if len(sees) == 1 {
				landing = "/tools/app/source/tree/" + appTip + "/"
			}
			if w := h.as(reader, "GET", "/", nil); w.Code != http.StatusFound || w.Header().Get("Location") != landing {
				t.Errorf("/ answered %d to %q, not to %s", w.Code, w.Header().Get("Location"), landing)
			}

			// The projects page lists each project the reader may see, by
			// group, and each group's page those of the group alone. A group
			// none of whose projects the reader may see answers as a path
			// that does not exist.
			row := func(name string) string {
				p := projects[name]
				return `<li class="listed"><a class="mono" href="` + p.base + `/source/tree/` + p.tip + `/">` +
					strings.TrimPrefix(p.base, "/") + `</a><a href="` + p.base + `/knots">knots</a></li>`
			}
			listed := h.pageAs(reader, "/-/projects")
			groups := map[string][]string{"/tools/": nil, "/pages/": nil}
			for _, name := range sees {
				group := "/" + strings.Split(projects[name].base, "/")[1] + "/"
				groups[group] = append(groups[group], row(name))
			}
			if n := strings.Count(listed, `<li class="listed">`); n != len(sees) || !strings.Contains(listed, `<a href="/-/knots">all knots</a>`) {
				t.Errorf("the projects page lists %d projects, not %d, or does not link all knots", n, len(sees))
			}
			missing := h.as(reader, "GET", "/tools/nosuch/", nil)
			for group, rows := range groups {
				for _, r := range rows {
					if !strings.Contains(listed, r) {
						t.Errorf("the projects page does not list %s", r)
					}
				}
				for _, target := range []string{group, strings.TrimSuffix(group, "/")} {
					w := h.as(reader, "GET", target, nil)
					if rows == nil {
						if w.Code != http.StatusNotFound || fmt.Sprint(w.Header()) != fmt.Sprint(missing.Header()) ||
							!bytes.Equal(w.Body.Bytes(), missing.Body.Bytes()) {
							t.Errorf("GET %s answered %d, not as a path that does not exist", target, w.Code)
						}
						continue
					}
					page := w.Body.String()
					if w.Code != http.StatusOK || strings.Count(page, `<li class="listed">`) != len(rows) {
						t.Errorf("GET %s answered %d without its group's projects alone", target, w.Code)
					}
					for _, r := range rows {
						if !strings.Contains(page, r) {
							t.Errorf("GET %s does not list %s", target, r)
						}
					}
				}
			}

			// Each project's knots page shows its own knots alone, under
			// its project header, whose knots tab is current.
			for _, name := range sees {
				p := projects[name]
				page := h.pageAs(reader, p.base+"/knots")
				head := `<header class="project-head">
  <span class="mono">` + strings.TrimPrefix(p.base, "/") + `</span>
  <nav class="tabs" aria-label="Project"><a href="` + p.base + `/source/tree/` + p.tip + `/">source</a><a href="` + p.base + `/knots" aria-current="page">knots</a></nav>
</header>`
				if !strings.Contains(page, head) {
					t.Errorf("%s's knots page has no project header with its knots tab current", name)
				}
				links := regexp.MustCompile(`href="([^"]*/knots/[^"]*)"`).FindAllStringSubmatch(page, -1)
				if len(links) == 0 {
					t.Errorf("%s's knots page links no knot", name)
				}
				for _, l := range links {
					if !strings.HasPrefix(l[1], p.base+"/knots/") {
						t.Errorf("%s's knots page links %s", name, l[1])
					}
				}
				if !strings.Contains(page, "<script>") {
					t.Errorf("%s's knots page carries no script", name)
				}
			}
			seesSite := slices.Contains(sees, "site")
			if strings.Contains(plan, "/tools/site/knots/") != seesSite {
				t.Errorf("the knots page shows site's knots %v, though the reader sees site %v", !seesSite, seesSite)
			}
			if strings.Contains(plan, "/pages/docs/") != slices.Contains(sees, "docs") {
				t.Error("the knots page links docs's knots to a reader who may not see it, or not to one who may")
			}

			// The references from app's knots into site.
			blocked := h.pageAs(reader, "/tools/app/knots/oc-a000003") + h.pageAs(reader, "/tools/app/knots/bd-a000004")
			for text, shown := range map[string]bool{
				`<a href="/tools/site/knots/oc-b000001">The site deploys the app</a>`:                   seesSite,
				`<dt>Lands in</dt><dd><a href="/tools/site/source/tree/` + siteTip + `/">site</a></dd>`: seesSite,
				`<dt>Lands in</dt><dd><a href="/tools/site/knots/oc-b000002">site:oc-b000002</a></dd>`:  seesSite,
				`<span class="t"><span class="muted">a knot in an unpublished project</span></span>`:    !seesSite,
				"<dt>Lands in</dt><dd>an unpublished project</dd>":                                      !seesSite,
				"<dt>Lands in</dt><dd>a knot in an unpublished project</dd>":                            !seesSite,
				"site:oc-b000001":          seesSite,
				"The site serves the docs": false,
			} {
				if strings.Contains(blocked, text) != shown {
					t.Errorf("app's blocked knots show %q %v, not %v", text, !shown, shown)
				}
			}

			// Each project the reader may see, on every route.
			for _, name := range sees {
				p := projects[name]
				admitted.Store(0)
				if page := h.pageAs(reader, p.base+"/source/tree/"+p.tip+"/"); !strings.Contains(page, p.readme) {
					t.Errorf("%s's tree does not render its README", name)
				}
				h.pageAs(reader, p.base+"/source/blob/"+p.tip+"/README.md")
				h.pageAs(reader, p.base+"/source/log/"+p.tip)
				archive := h.as(reader, "GET", p.base+"/source/archive/"+p.tip+".tar.gz", nil)
				if archive.Code != http.StatusOK || !strings.Contains(archive.Header().Get("Content-Disposition"), name+"-"+p.tip[:12]+".tar.gz") {
					t.Errorf("%s's archive answered %d, %q", name, archive.Code, archive.Header().Get("Content-Disposition"))
				} else if _, ok := archiveEntries(t, archive.Body.Bytes())[name+"-"+p.tip[:12]+"/README.md"]; !ok {
					t.Errorf("%s's archive does not hold its README", name)
				}
				if n := admitted.Load(); n != 4 {
					t.Errorf("%s's four source routes took %d git slots", name, n)
				}
				for target, to := range map[string]string{
					p.base + "/":                           p.base + "/source/tree/" + p.tip + "/",
					p.base + "/source/":                    p.base + "/source/tree/" + p.tip + "/",
					p.base + "/source/archive/main.tar.gz": p.base + "/source/archive/" + p.tip + ".tar.gz",
				} {
					if w := h.as(reader, "GET", target, nil); w.Code != http.StatusFound || w.Header().Get("Location") != to {
						t.Errorf("GET %s answered %d to %q, not to %s", target, w.Code, w.Header().Get("Location"), to)
					}
				}
			}
			aliases := map[string]string{
				"/t/app/": "/tools/app/", "/tools/old-app/": "/tools/app/", "/app/knots/oc-a000002": "/tools/app/knots/oc-a000002",
				"/t/site/": "/tools/site/", "/t/nosuch/": "/tools/nosuch/",
			}
			if seesSite {
				aliases["/site/"], aliases["/tools/old-site/"] = "/tools/site/", "/tools/site/"
			}
			if slices.Contains(sees, "docs") {
				aliases["/p/docs/"], aliases["/d/"], aliases["/pages/old-docs/x"] = "/pages/docs/", "/pages/docs/", "/pages/docs/x"
			}
			for from, to := range aliases {
				if w := h.as(reader, "GET", from, nil); w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != to {
					t.Errorf("GET %s answered %d to %q, not to %s", from, w.Code, w.Header().Get("Location"), to)
				}
			}

			// Each project the reader may not see answers as one that does
			// not exist, on every route, and takes no git slot.
			for _, name := range []string{"site", "docs"} {
				if slices.Contains(sees, name) {
					continue
				}
				p := projects[name]
				admitted.Store(0)
				var hidden []string
				for _, route := range []string{"", "/", "/knots/oc-b000001", "/source", "/source/", "/source/tree/" + p.tip + "/",
					"/source/blob/" + p.tip + "/README.md", "/source/log/" + p.tip, "/source/archive/" + p.tip + ".tar.gz",
					"/source/archive/main.tar.gz"} {
					hidden = append(hidden, p.base+route)
				}
				if name == "site" {
					hidden = append(hidden, "/site/", "/site/knots/oc-b000001", "/tools/old-site/source/")
				} else {
					hidden = append(hidden, "/p/docs/", "/p/", "/d/", "/pages/old-docs/", "/pages/")
				}
				for _, target := range hidden {
					w := h.as(reader, "GET", target, nil)
					if w.Code != http.StatusNotFound || fmt.Sprint(w.Header()) != fmt.Sprint(missing.Header()) ||
						!bytes.Equal(w.Body.Bytes(), missing.Body.Bytes()) {
						t.Errorf("GET %s answered %d, not as a project that does not exist", target, w.Code)
					}
				}
				// A knots page's path carries the knots pages' policy, whether
				// or not its project is one the reader may see.
				w, none := h.as(reader, "GET", p.base+"/knots", nil), h.as(reader, "GET", "/tools/nosuch/knots", nil)
				if w.Code != http.StatusNotFound || fmt.Sprint(w.Header()) != fmt.Sprint(none.Header()) ||
					!bytes.Equal(w.Body.Bytes(), none.Body.Bytes()) || w.Header().Get("Content-Security-Policy") != knotsPolicy {
					t.Errorf("GET %s/knots answered %d, not as a project that does not exist", p.base, w.Code)
				}
				if n := admitted.Load(); n != 0 {
					t.Errorf("the routes of %s, which the reader may not see, took %d git slots", name, n)
				}
			}
		})
	}

	// With app withdrawn from anyone, / lands on the one project each
	// named reader may see, and on the projects page for a reader who may
	// see none, which says so.
	e, _ := h.buildExport(true, nil)
	withDocs(e, map[string]string{"site": ""})
	h.authorizeReaders(e, map[string][]string{"app": {"carol"}, "site": {"alice"}, "docs": {"bob"}})
	for reader, to := range map[string]string{export.Anyone: projectsURL, "alice": "/tools/site/source/tree/" + siteTip + "/",
		"bob": "/pages/docs/source/tree/" + siteTip + "/", "carol": "/tools/app/source/tree/" + appTip + "/"} {
		if w := h.as(reader, "GET", "/", nil); w.Code != http.StatusFound || w.Header().Get("Location") != to {
			t.Errorf("/ as %s answered %d to %q, not to %s", reader, w.Code, w.Header().Get("Location"), to)
		}
	}
	empty := h.pageAs(export.Anyone, "/-/projects")
	if !strings.Contains(empty, `<p class="muted">No project is readable here.</p>`) || strings.Contains(empty, "all knots") ||
		strings.Contains(empty, `<li class="listed">`) || strings.Contains(empty, "tools") {
		t.Errorf("with no project to see, the projects page is not its empty state:\n%s", empty)
	}
	if w := h.as(export.Anyone, "GET", "/tools/", nil); w.Code != http.StatusNotFound {
		t.Errorf("a group none of whose projects anyone may see answered %d", w.Code)
	}
	if w := h.as(export.Anyone, "GET", "/t/app/", nil); w.Code != http.StatusNotFound {
		t.Errorf("a group none of whose projects anyone may see kept its alias: %d", w.Code)
	}
}

// A reader may see a project when the authorization names it, under the
// identity the export gives it, for anyone or for the reader's principal:
// the union of the two. A grant under another identity grants nothing.
func TestVisibilityIsTheUnionUnderThePublicationIdentity(t *testing.T) {
	e := &export.Export{Groups: []export.Group{{ID: "core", Name: "tools"}}, Projects: []export.Project{
		{Name: "app", Identity: exporttest.Identities["app"], Group: "core"},
		{Name: "loose", Identity: docsIdentity},
		{Name: "site", Identity: siteIdentity, Group: "core"},
	}}
	a := &export.Authorization{Projects: []export.AuthorizedProject{
		{Name: "app", Identity: exporttest.Identities["app"], Readers: []string{"alice"}},
		{Name: "loose", Identity: docsIdentity, Readers: []string{export.Anyone, "alice"}},
		{Name: "site", Identity: siteIdentity, Readers: []string{export.Anyone, "bob"}},
	}}
	for reader, want := range map[string][]string{
		export.Anyone: {"site"}, "alice": {"app", "site"}, "bob": {"site"}, "carol": {"site"},
	} {
		var got []string
		for name := range visible(e, a, reader) {
			got = append(got, name)
		}
		sort.Strings(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s may see %v, not %v", reader, got, want)
		}
	}
	a.Projects[0].Identity = siteIdentity
	if shown := visible(e, a, "alice"); shown["app"] {
		t.Error("a grant under another identity lets alice see app")
	}
	if shown := visible(e, nil, "alice"); len(shown) != 0 {
		t.Errorf("no authorization lets alice see %v", shown)
	}
}

// Readers who may see the same projects share one index, and a named
// reader who may see no more than anyone shares anyone's. The index names
// no reader, and neither does any page built from it.
func TestIdenticalViewsShareAnIndex(t *testing.T) {
	h := newHarness(t)
	h.edge(exporttest.SignedAt.Add(time.Minute), map[string][]string{
		"app": {export.Anyone}, "site": {"alice", "carol"}, "docs": {"bob"}})
	var built []string
	var mu sync.Mutex
	h.srv.indexing = func(_ context.Context, key viewKey) {
		mu.Lock()
		defer mu.Unlock()
		built = append(built, key.projects)
	}
	pages := map[string]string{}
	for _, reader := range []string{export.Anyone, "alice", "carol", "dave", "alice", export.Anyone} {
		pages[reader] = h.pageAs(reader, "/-/knots") + h.pageAs(reader, "/tools/app/knots/oc-a000003")
	}
	if len(built) != 2 {
		t.Errorf("the server built %d indexes for two views:\n%s", len(built), strings.Join(built, "--\n"))
	}
	if anonymous, named, pins := h.srv.heldViews(); anonymous != 1 || named != 1 || pins != 0 {
		t.Errorf("the server holds %d views of what anyone may see and %d others, pinned %d times", anonymous, named, pins)
	}
	if pages["alice"] != pages["carol"] || pages["dave"] != pages[export.Anyone] || pages["alice"] == pages[export.Anyone] {
		t.Error("readers who may see the same projects were served other pages")
	}
	for reader, page := range pages {
		for _, name := range []string{"alice", "carol", "dave", "bob"} {
			if strings.Contains(page, name) {
				t.Errorf("the page served to %s names %s", reader, name)
			}
		}
	}
}

// A knot whose one blocker is in a project the reader may not see reads
// the same whether the export leaves that project out or holds it for
// another reader: its projected fields, the knots page and its own page
// are the same, byte for byte. Its blocker done, neither says it waits;
// its blocker open, both say once that it waits on something the reader
// may not see.
func TestAHiddenBlockerReadsAlikeInEitherExport(t *testing.T) {
	for _, status := range []string{"done", "open"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			change := func(knot func(project, id string) map[string]any) {
				k := knot("app", "oc-a000003")
				k["blocked_by"] = []any{"site:oc-b000001"}
				k["waiting_on"], k["state"], k["ready"] = []any{"site:oc-b000001"}, "blocked", false
				k["edges"].([]any)[0].(map[string]any)["status"] = status
				knot("app", "oc-a000002")["blocks"] = []any{}
				if status == "done" {
					k["waiting_on"], k["state"], k["ready"] = []any{}, "ready", true
					b := knot("site", "oc-b000001")
					b["status"], b["active"], b["state"], b["ready"] = "done", false, "done", false
				}
			}
			targets := []string{"/-/knots", "/tools/app/knots/oc-a000003", "/tools/app/knots/bd-a000004", "/tools/app/knots/oc-a000001"}
			served := func(at time.Time, site bool) (map[string]string, *export.Project) {
				t.Helper()
				e, bundles := h.buildExport(site, change)
				h.authorizeReaders(e, map[string][]string{"app": {export.Anyone}, "site": {"alice"}})
				h.publishExport(at, e, bundles)
				pages := map[string]string{}
				for _, target := range targets {
					pages[target] = h.page(target)
				}
				return pages, h.srv.anonymousIndex().projects["app"]
			}
			without, projectedWithout := served(exporttest.SignedAt.Add(time.Minute), false)
			with, projectedWith := served(exporttest.SignedAt.Add(2*time.Minute), true)
			if !reflect.DeepEqual(projectedWithout, projectedWith) {
				t.Errorf("app is projected one way from the export without site and another from the one with it:\n%+v\n%+v",
					projectedWithout.Knots[2], projectedWith.Knots[2])
			}
			for _, target := range targets {
				if without[target] != with[target] {
					t.Errorf("GET %s answered one page from the export without site and another from the one with it", target)
				}
			}
			plan := with["/-/knots"]
			switch status {
			case "done":
				if strings.Contains(plan, `class="chip blocked"`) {
					t.Error("a knot whose hidden blocker is done is shown waiting")
				}
			case "open":
				if strings.Count(plan, `<span class="chip blocked">`) != 1 ||
					!strings.Contains(plan, `<span class="chip blocked">waits on a knot in an unpublished project</span>`) {
					t.Errorf("a knot whose hidden blocker is open does not say once that it waits:\n%s", section(t, plan, `id="ms-h"`, "</body>"))
				}
			}
			for _, page := range with {
				for _, unwanted := range append([]string{"/tools/site/"}, unpublishedText...) {
					if strings.Contains(page, unwanted) {
						t.Errorf("a page holds %q", unwanted)
					}
				}
			}
			if page := h.pageAs("alice", "/tools/app/knots/oc-a000003"); !strings.Contains(page, `<a href="/tools/site/knots/oc-b000001">The site deploys the app</a>`) {
				t.Error("alice, who may see site, is not shown the blocker")
			}
		})
	}
}

// The projection copies the projects a reader may see, and of each knot
// keeps what it says of those projects and the export's opaque summary
// alone: the references into other projects leave its four derived lists,
// its WaitingOnHidden is set when one leaves WaitingOn, and its edges into
// them are hidden, with nothing of their targets kept. The export it reads
// does not change.
func TestProjectionHidesOtherProjects(t *testing.T) {
	h := newHarness(t)
	e, _ := h.buildExport(true, nil)
	app := &e.Projects[0]
	at := slices.IndexFunc(app.Knots, func(k export.Knot) bool { return k.ID == "oc-a000003" })
	k := &app.Knots[at]
	why, status, title := "a reason", "open", "A title of site's"
	// "site" alone is neither a knot's ID nor <project>:<id>, so it names
	// no knot the reader may see, and is taken out.
	k.WaitingOn = []string{"oc-a000002", "site:oc-b000001", "app:oc-a000006", "gone:oc-c000001", "site"}
	k.ClearedByEnding = []string{"site:oc-b000002", "oc-a000006"}
	k.Dangling = []string{"site:oc-bfffff1", "app:oc-afffff1"}
	k.Unresolved = []string{"site:oc-b000003", "oc-a000001", "site"}
	k.Edges = []export.Edge{
		{Field: "blocked_by", Ref: "site:oc-b000001", Project: "site", ID: "oc-b000001", State: export.EdgeResolved, Why: &why, Status: &status, Title: &title},
		{Field: "blocked_by", Ref: "site:oc-bfffff1", Project: "site", ID: "oc-bfffff1", State: export.EdgeDangling, Why: &why},
		{Field: "blocked_by", Ref: "app:oc-afffff1", Project: "app", ID: "oc-afffff1", State: export.EdgeDangling},
		{Field: "target", Ref: "gone:oc-c000001", Project: "gone", ID: "oc-c000001", State: export.EdgeHidden},
	}
	before, err := e.Encode()
	if err != nil {
		t.Fatal(err)
	}
	projected, err := projectForReader(context.Background(), e, map[string]bool{"app": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(projected.Projects) != 1 || projected.Projects[0].Name != "app" || len(projected.Groups) != 1 {
		t.Fatalf("the projection holds %d projects and %d groups", len(projected.Projects), len(projected.Groups))
	}
	got := projected.Projects[0].Knots[at]
	hide := func(edge export.Edge) export.Edge {
		return export.Edge{Field: edge.Field, Ref: edge.Ref, Project: edge.Project, ID: edge.ID, State: export.EdgeHidden}
	}
	for field, c := range map[string][2]any{
		"WaitingOn":       {got.WaitingOn, []string{"oc-a000002", "app:oc-a000006"}},
		"WaitingOnHidden": {got.WaitingOnHidden, true},
		"ClearedByEnding": {got.ClearedByEnding, []string{"oc-a000006"}},
		"Dangling":        {got.Dangling, []string{"app:oc-afffff1"}},
		"Unresolved":      {got.Unresolved, []string{"oc-a000001"}},
		"Edges":           {got.Edges, []export.Edge{hide(k.Edges[0]), hide(k.Edges[1]), k.Edges[2], k.Edges[3]}},
		"State":           {got.State, k.State},
		"Ready":           {got.Ready, k.Ready},
		"BlockedBy":       {got.BlockedBy, k.BlockedBy},
	} {
		if !reflect.DeepEqual(c[0], c[1]) {
			t.Errorf("the projected %s is %v, not %v", field, c[0], c[1])
		}
	}

	// Everything kept for a reader who may see site, and the export read
	// is the same bytes, though the copies change.
	all, err := projectForReader(context.Background(), e, map[string]bool{"app": true, "site": true})
	if err != nil {
		t.Fatal(err)
	}
	if kept := all.Projects[0].Knots[at]; !reflect.DeepEqual(kept.WaitingOn, k.WaitingOn[:3]) || !kept.WaitingOnHidden ||
		!reflect.DeepEqual(kept.Edges, k.Edges) || !reflect.DeepEqual(kept.Dangling, k.Dangling) || len(all.Projects) != 2 {
		t.Errorf("a reader who may see site is not told what the export says of it: %+v", kept)
	}
	projected.Projects[0].Knots[at].Title = "changed"
	projected.Projects[0].Knots[at].WaitingOn[0] = "changed"
	projected.Projects[0].Knots[at].Edges[2].ID = "changed"
	all.Projects[0].Knots[at].Dangling[0] = "changed"
	if after, err := e.Encode(); err != nil || !bytes.Equal(before, after) {
		t.Errorf("projecting the export, or changing a projection, changed the export: %v", err)
	}
}

// privateViews activates an export that holds app, which anyone may
// read, and four private projects, p1 to p4, copies of site. It stores an
// authorization that lets each of the principals r1 to r15 read another
// set of the four, so each of them has a view of their own. It returns the
// export and its bundles.
func (h *harness) privateViews() (*export.Export, map[string]string) {
	h.t.Helper()
	e, bundles := h.buildExport(true, nil)
	var site export.Project
	for _, p := range e.Projects {
		if p.Name == "site" {
			site = p
		}
	}
	readers := map[string][]string{"app": {export.Anyone}}
	for i := 1; i <= 4; i++ {
		p := site
		p.Name, p.WebName, p.Identity, p.FormerNames, p.Aliases = fmt.Sprintf("p%d", i), fmt.Sprintf("p%d", i), fmt.Sprintf("%032x", i), nil, nil
		e.Projects = append(e.Projects, p)
		bundles[p.Name] = bundles["site"]
		for set := 1; set < 16; set++ {
			if set&(1<<(i-1)) != 0 {
				readers[p.Name] = append(readers[p.Name], fmt.Sprintf("r%d", set))
			}
		}
	}
	sort.Slice(e.Projects, func(i, j int) bool { return e.Projects[i].Name < e.Projects[j].Name })
	h.authorizeReaders(e, readers)
	h.publishExport(exporttest.SignedAt.Add(time.Minute), e, bundles)
	return e, bundles
}

// serving answers a GET of target read as reader, under ctx, in a
// goroutine of its own, and sends its response on the channel it returns.
func (h *harness) serving(reader, target string, ctx context.Context) chan *httptest.ResponseRecorder {
	served := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", target, nil)
		h.srv.ServeHTTP(w, r.WithContext(context.WithValue(ctx, readerKey{}, reader)))
		served <- w
	}()
	return served
}

// A target names a project, or a knot of one, and never a knot of its
// own graph, so a project may be named as a knot's ID and still be a
// project. Here oc-c000001, a copy of site that alice alone may read, is
// the target of app's oc-a000003. A reader who may not see it is told the
// knot lands in an unpublished project, on its page and on the knots
// page, and no page of theirs names it. alice is shown it and linked to
// its source. The other references a knot's page names are held to their
// own fields: graduated_into names a knot of the knot's own graph, and a
// blocked_by entry, a waiting_on entry or a changes link that names a
// hidden project in neither of the shapes its field allows names nothing
// of it.
func TestATargetNamesNoProjectTheReaderMayNotSee(t *testing.T) {
	h := newHarness(t)
	e, bundles := h.buildExport(true, nil)
	const hidden = "oc-c000001"
	for _, p := range e.Projects {
		if p.Name == "site" {
			p.Name, p.WebName, p.Identity, p.FormerNames, p.Aliases = hidden, hidden, fmt.Sprintf("%032x", 0xc1), nil, nil
			e.Projects = append(e.Projects, p)
			bundles[hidden] = bundles["site"]
			break
		}
	}
	sort.Slice(e.Projects, func(i, j int) bool { return e.Projects[i].Name < e.Projects[j].Name })
	app := &e.Projects[0]
	for i := range app.Knots {
		switch k := &app.Knots[i]; k.ID {
		case "oc-a000003":
			k.Target = hidden
			k.BlockedBy = append(k.BlockedBy, "site")
			k.WaitingOn = append(k.WaitingOn, "site")
			k.Changes = append(k.Changes, "valley:site")
		case "bd-a000004":
			k.GraduatedInto = "oc-a000002"
		case "oc-a000006":
			k.GraduatedInto = "site"
		}
	}
	h.authorizeReaders(e, map[string][]string{"app": {export.Anyone}, hidden: {"alice"}})
	h.publishExport(exporttest.SignedAt.Add(time.Minute), e, bundles)
	tip := git(t, h.f.Repos["site"], nil, "rev-parse", export.MainRef)

	for reader, sees := range map[string]bool{export.Anyone: false, "bob": false, "alice": true} {
		t.Run(reader, func(t *testing.T) {
			knot := h.pageAs(reader, "/tools/app/knots/oc-a000003")
			plan := h.pageAs(reader, "/-/knots")
			lands := "<dt>Lands in</dt><dd>an unpublished project</dd>"
			chip := `<span class="chip quiet">lands in an unpublished project</span>`
			if sees {
				lands = `<dt>Lands in</dt><dd><a href="/tools/` + hidden + `/source/tree/` + tip + `/">` + hidden + `</a></dd>`
				chip = `<span class="chip quiet">lands in ` + hidden + `</span>`
			}
			if !strings.Contains(knot, lands) {
				t.Errorf("oc-a000003's page does not show %q", lands)
			}
			if !strings.Contains(plan, chip) {
				t.Errorf("the knots page does not show %q", chip)
			}

			// Every page the reader may see of app, and the landing.
			pages := []string{knot, plan}
			for _, k := range app.Knots {
				pages = append(pages, h.pageAs(reader, "/tools/app/knots/"+k.ID))
			}
			pages = append(pages, h.pageAs(reader, "/tools/app/source/tree/"+h.tip()+"/"))
			if w := h.as(reader, "GET", "/", nil); w.Code != http.StatusFound {
				t.Errorf("/ answered %d", w.Code)
			} else {
				pages = append(pages, w.Header().Get("Location")+w.Body.String())
			}
			all := strings.Join(pages, "\n")
			if strings.Contains(all, hidden) != sees {
				t.Errorf("the reader's pages name %s %v, though the reader sees it %v", hidden, !sees, sees)
			}
			for _, unwanted := range []string{">site<", "site,", ", site", "valley:site", "/tools/site/"} {
				if strings.Contains(all, unwanted) {
					t.Errorf("the reader's pages hold %q", unwanted)
				}
			}

			// The fields beside the target, each by its own shape.
			for _, want := range []string{
				`<span class="chip blocked">waits on oc-a000002, a knot in an unpublished project</span>`,
			} {
				if !strings.Contains(plan, want) {
					t.Errorf("the knots page does not show %q", want)
				}
			}
			for target, want := range map[string]string{
				"/tools/app/knots/oc-a000003": `<li class="mono">a change in an unpublished project</li>`,
				"/tools/app/knots/bd-a000004": `<dt>Graduated into</dt><dd><a href="/tools/app/knots/oc-a000002">oc-a000002</a></dd>`,
				"/tools/app/knots/oc-a000006": "<dt>Graduated into</dt><dd>a knot in an unpublished project</dd>",
			} {
				if page := h.pageAs(reader, target); !strings.Contains(page, want) {
					t.Errorf("%s does not show %q", target, want)
				}
			}
			if n := strings.Count(knot, `<span class="muted">a knot in an unpublished project</span>`); n != 2 {
				t.Errorf("oc-a000003's page names %d blockers as unpublished, not its two", n)
			}
		})
	}
}

// A reference is read by the whole of a shape its field allows before
// anything of it is resolved or shown (knot/validate.go): a project's
// name, <project>:<id>, a knot's ID, or valley:<project>/<change ID>. Here
// app is anyone's, site is alice's alone, and each value opens with app,
// which every reader sees, but has none of its field's shapes. Every page
// that names the field shows it as unpublished, with none of its text,
// whatever the reader sees, and the projection takes it out of the four
// derived lists.
func TestMalformedReferencesNameNothing(t *testing.T) {
	malformed := []string{"app:site:oc-b000001", "app:oc-b000001:site", "app:site", "app/site", "app:site/oc-b000001"}
	for _, c := range []struct {
		field  string
		knot   string
		values []string
		set    func(k *export.Knot, value string)
		shown  map[string]string // a route that names the field, and what it shows there
	}{
		{"blocked_by", "oc-a000003", malformed,
			func(k *export.Knot, v string) { k.BlockedBy = []string{"oc-a000002", v} },
			map[string]string{"/tools/app/knots/oc-a000003": `<span class="t"><span class="muted">a knot in an unpublished project</span></span>`}},
		{"target", "oc-a000003", malformed,
			func(k *export.Knot, v string) { k.Target = v },
			map[string]string{
				"/tools/app/knots/oc-a000003": "<dt>Lands in</dt><dd>an unpublished project</dd>",
				"/-/knots":                    `<span class="chip quiet">lands in an unpublished project</span>`,
			}},
		{"waiting_on", "oc-a000003", malformed,
			func(k *export.Knot, v string) { k.WaitingOn = []string{"oc-a000002", v} },
			map[string]string{"/-/knots": `<span class="chip blocked">waits on oc-a000002, a knot in an unpublished project</span>`}},
		{"graduated_into", "oc-a000006", append([]string{"site", "app:oc-a000002", "bd-a000004", "ida-a000007"}, malformed...),
			func(k *export.Knot, v string) { k.GraduatedInto = v },
			map[string]string{"/tools/app/knots/oc-a000006": "<dt>Graduated into</dt><dd>a knot in an unpublished project</dd>"}},
		{"changes", "oc-a000003", []string{"valley:app/site/secret", "valley:app/site", "valley:app:site",
			"valley:app/kmnopqrstuvwxyzkmnopqrstuvwxyzkm/site", "valley:app/kmnopqrstuvwxyzkmnopqrstuvwxyzkm site"},
			func(k *export.Knot, v string) { k.Changes = []string{v} },
			map[string]string{"/tools/app/knots/oc-a000003": `<li class="mono">a change in an unpublished project</li>`}},
	} {
		t.Run(c.field, func(t *testing.T) {
			h := newHarness(t)
			e, bundles := h.buildExport(true, nil)
			h.authorizeReaders(e, map[string][]string{"app": {export.Anyone}, "site": {"alice"}})
			for i, value := range c.values {
				next := *e
				next.Projects = slices.Clone(e.Projects)
				app := &next.Projects[0]
				app.Knots = slices.Clone(app.Knots)
				at := slices.IndexFunc(app.Knots, func(k export.Knot) bool { return k.ID == c.knot })
				c.set(&app.Knots[at], value)
				h.publishExport(exporttest.SignedAt.Add(time.Duration(i+1)*time.Minute), &next, bundles)
				// A bare word is looked for as an element's text, since
				// the pages' own markup holds words such as site.
				named := value
				if !strings.ContainsAny(value, ":/") {
					named = ">" + value + "<"
				}
				for _, reader := range []string{export.Anyone, "bob", "alice"} {
					for route, want := range c.shown {
						page := h.pageAs(reader, route)
						if !strings.Contains(page, want) {
							t.Errorf("%s = %q: %s as %s does not show %q", c.field, value, route, reader, want)
						}
						if strings.Contains(page, named) {
							t.Errorf("%s = %q: %s as %s names it", c.field, value, route, reader)
						}
					}
				}
			}
		})
	}

	t.Run("derived lists", func(t *testing.T) {
		h := newHarness(t)
		e, _ := h.buildExport(true, nil)
		app := &e.Projects[0]
		at := slices.IndexFunc(app.Knots, func(k export.Knot) bool { return k.ID == "oc-a000003" })
		k := &app.Knots[at]
		k.WaitingOn = append([]string{"oc-a000002"}, malformed...)
		k.ClearedByEnding, k.Dangling, k.Unresolved = malformed, malformed, malformed
		for _, shown := range []map[string]bool{{"app": true}, {"app": true, "site": true}} {
			projected, err := projectForReader(context.Background(), e, shown)
			if err != nil {
				t.Fatal(err)
			}
			got := projected.Projects[0].Knots[at]
			if !slices.Equal(got.WaitingOn, []string{"oc-a000002"}) || !got.WaitingOnHidden ||
				len(got.ClearedByEnding) != 0 || len(got.Dangling) != 0 || len(got.Unresolved) != 0 {
				t.Errorf("projected for %v, the lists keep %v, %v, %v and %v, and waiting_on_hidden is %v",
					shown, got.WaitingOn, got.ClearedByEnding, got.Dangling, got.Unresolved, got.WaitingOnHidden)
			}
		}
	})
}

// The views of what anyone may see have places no named reader's view
// takes. While every named place holds a view being built, a named
// reader's request whose view needs another waits, holding no loader
// slot, no git slot and no generation, and answers 503 at its deadline,
// or nothing once its reader goes away, leaving no view behind. So does a
// named reader who may see no more than anyone, though it would share
// anyone's index. Meanwhile anyone is served, a load of a new generation
// runs, and the new generation's view of what anyone may see is built and
// served. Once the builds end, every request that waited for one is
// served.
func TestAnonymousViewsSurviveNamedSaturation(t *testing.T) {
	h := newHarness(t)
	e, bundles := h.privateViews()
	h.page("/-/knots")

	hold := make(chan struct{})
	var holding atomic.Int32
	h.srv.indexing = func(ctx context.Context, key viewKey) {
		if strings.Contains(key.projects, "\np") {
			holding.Add(1)
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}
	}
	var gitSlots atomic.Int32
	h.srv.admitted = func() { gitSlots.Add(1) }
	serve := h.serving
	var waiting []chan *httptest.ResponseRecorder
	for i := 1; i <= MaxViews; i++ {
		waiting = append(waiting, serve(fmt.Sprintf("r%d", i), "/-/knots", context.Background()))
	}
	eventually(t, "every named place holds a view being built", func() bool { return holding.Load() == MaxViews })

	// A ninth named view finds no room. It waits holding nothing, and
	// answers 503 at its deadline; another whose reader goes away answers
	// nothing. Neither leaves a view or a pin behind.
	h.srv.timeout = 300 * time.Millisecond
	h.srv.log = func(error) {}
	late := serve("r9", "/tools/p1/source/tree/"+h.tip()+"/", context.Background())
	// The eight wait for their builds, each in a waiting place, and the
	// ninth waits for room in one more.
	eventually(t, "the ninth named view waits for room", func() bool { return len(h.srv.loadWaiting) == MaxViews+1 })
	if h.srv.runningLoads() != 0 || h.srv.heldSlots() != 0 || gitSlots.Load() != 0 {
		t.Errorf("a request waiting for room holds %d loader slots and %d git slots", h.srv.runningLoads(), h.srv.heldSlots())
	}
	select {
	case w := <-late:
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
			t.Errorf("a named view that found no room answered %d", w.Code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a named view that found no room waited past its deadline")
	}
	h.srv.timeout = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	gone := serve("r10", "/-/knots", ctx)
	eventually(t, "the tenth named view waits for room", func() bool { return len(h.srv.loadWaiting) == MaxViews+1 })
	cancel()
	if w := <-gone; w.Body.Len() != 0 {
		t.Errorf("a request whose reader went away answered %d", w.Code)
	}
	if anonymous, named, _ := h.srv.heldViews(); anonymous != 1 || named != MaxViews {
		t.Errorf("the server holds %d views of what anyone may see and %d others", anonymous, named)
	}

	// Anyone is served still, from the generation's view, and from the
	// next generation's, whose export is read and indexed meanwhile. r0,
	// who may see no more than anyone, shares anyone's index, but takes a
	// named place for it, so it waits for room.
	h.page("/-/knots")
	r0 := serve("r0", "/tools/app/knots/oc-a000002", context.Background())
	eventually(t, "r0 waits for a named place", func() bool { return len(h.srv.loadWaiting) == MaxViews+1 })
	app := e.Projects[0]
	app.Knots = slices.Clone(app.Knots)
	app.Knots[0].Title = "Readers browse the next plan"
	next := *e
	next.Projects = slices.Clone(e.Projects)
	next.Projects[0] = app
	h.publishExport(exporttest.SignedAt.Add(2*time.Minute), &next, bundles)
	if !strings.Contains(h.page("/tools/app/knots/oc-a000001"), "Readers browse the next plan") {
		t.Error("anyone is not served the next generation while named views fill their places")
	}

	select {
	case w := <-r0:
		t.Errorf("r0 answered %d while every named place was taken", w.Code)
	default:
	}

	close(hold)
	for _, served := range waiting {
		if w := <-served; w.Code != http.StatusOK {
			t.Errorf("a named view that was built answered %d", w.Code)
		}
	}
	if w := <-r0; w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "/tools/app/knots/oc-a000001") {
		t.Errorf("r0 answered %d once there was room", w.Code)
	}
	if page := h.pageAs("r15", "/-/knots"); !strings.Contains(page, "/tools/p4/knots/") || !strings.Contains(page, "next plan") {
		t.Error("a named reader is not served the next generation once there is room")
	}
	if _, named, pins := h.srv.heldViews(); named > MaxViews || pins != 0 {
		t.Errorf("the server holds %d named views, pinned %d times", named, pins)
	}
	if n := len(h.srv.loadWaiting); n != 0 {
		t.Errorf("%d requests still hold a place among those that wait", n)
	}
}

// A named reader who may see no more than anyone shares anyone's index,
// but takes a named place for it, and never an anonymous one. Here r0's
// source requests hold the indexes of what anyone may see of generations
// A and B while they wait in their git slots, and generation C is
// activated. An anonymous request for the knots page is served from C at
// once, while they still hold them. With every other named place held by
// a view being built, it is served at once too, while r0's request for
// the knots page of C waits for a named place. Once the named requests
// let their places go, every request is served.
func TestNamedRequestsTakeNoAnonymousPlace(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "named places free", true: "named places full"}[full], func(t *testing.T) {
			h := newHarness(t)
			e, bundles := h.privateViews()
			activate := func(at time.Duration, title string) {
				app := e.Projects[0]
				app.Knots = slices.Clone(app.Knots)
				app.Knots[0].Title = title
				next := *e
				next.Projects = slices.Clone(e.Projects)
				next.Projects[0] = app
				h.publishExport(exporttest.SignedAt.Add(at), &next, bundles)
			}
			held := make(chan struct{})
			var admitted atomic.Int32
			h.srv.admitted = func() {
				admitted.Add(1)
				<-held
			}
			hold := make(chan struct{})
			var holding atomic.Int32
			h.srv.indexing = func(ctx context.Context, key viewKey) {
				if strings.Contains(key.projects, "\np") {
					holding.Add(1)
					select {
					case <-hold:
					case <-ctx.Done():
					}
				}
			}

			// r0 holds generation A's index of what anyone may see, and
			// then generation B's, each in a request that waits in its
			// git slot.
			source := "/tools/app/source/tree/" + h.tip() + "/"
			named := []chan *httptest.ResponseRecorder{h.serving("r0", source, context.Background())}
			eventually(t, "r0 holds generation A's index", func() bool { return admitted.Load() == 1 })
			activate(2*time.Minute, "Readers browse plan B")
			named = append(named, h.serving("r0", source, context.Background()))
			eventually(t, "r0 holds generation B's index", func() bool { return admitted.Load() == 2 })
			if anonymous, places, pins := h.srv.heldViews(); anonymous != 0 || places != 2 || pins != 2 {
				t.Fatalf("named requests hold %d anonymous places and %d named ones, pinned %d times", anonymous, places, pins)
			}
			if full {
				for i := 1; i <= MaxViews-2; i++ {
					named = append(named, h.serving(fmt.Sprintf("r%d", i), "/-/knots", context.Background()))
				}
				eventually(t, "every named place is taken", func() bool { return holding.Load() == MaxViews-2 })
			}

			activate(3*time.Minute, "Readers browse plan C")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if w := <-h.serving(export.Anyone, "/-/knots", ctx); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "plan C") {
				t.Errorf("an anonymous request for generation C answered %d, %d bytes, while named requests held their places", w.Code, w.Body.Len())
			}
			late := h.serving("r0", "/-/knots", context.Background())
			if full {
				eventually(t, "r0 waits for a named place", func() bool { return len(h.srv.namedWaiting) == MaxViews-1 })
				time.Sleep(20 * time.Millisecond)
				select {
				case w := <-late:
					t.Errorf("r0 answered %d while every named place was taken", w.Code)
				default:
				}
			}
			if admitted.Load() != 2 {
				t.Errorf("%d source requests took git slots, not r0's two", admitted.Load())
			}

			close(hold)
			close(held)
			for _, served := range append(named, late) {
				if w := <-served; w.Code != http.StatusOK {
					t.Errorf("a named reader's request answered %d", w.Code)
				}
			}
			if page := h.pageAs("r0", source); !strings.Contains(page, "/tools/app/") {
				t.Error("r0 is not served once the places are free")
			}
			if anonymous, places, pins := h.srv.heldViews(); anonymous > anonymousViews || places > MaxViews || pins != 0 {
				t.Errorf("the server holds %d anonymous places and %d named ones, pinned %d times", anonymous, places, pins)
			}
			if len(h.srv.loadWaiting) != 0 || len(h.srv.namedWaiting) != 0 {
				t.Errorf("%d requests, %d of them named, still hold a place among those that wait", len(h.srv.loadWaiting), len(h.srv.namedWaiting))
			}
		})
	}
}

// Named readers take MaxNamedWaiting of the MaxLoadWaiting places at
// most, and the rest are anonymous requests' alone. While every named
// place holds a view being built, and named readers' requests wait in
// every place they may take, a named reader's request past them answers
// 503 at once. A new generation is then activated, and as many anonymous
// requests as the places left wait for its load together, and each is
// served from it. Once the builds end, every named request that waited is
// served.
func TestAnonymousRequestsKeepTheirWaitingPlaces(t *testing.T) {
	h := newHarness(t)
	e, bundles := h.privateViews()
	h.page("/-/knots")

	hold := make(chan struct{})
	var holding atomic.Int32
	h.srv.indexing = func(ctx context.Context, key viewKey) {
		if strings.Contains(key.projects, "\np") {
			holding.Add(1)
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}
	}
	var named []chan *httptest.ResponseRecorder
	for i := 1; i <= MaxViews; i++ {
		named = append(named, h.serving(fmt.Sprintf("r%d", i), "/-/knots", context.Background()))
	}
	eventually(t, "every named place holds a view being built", func() bool { return holding.Load() == MaxViews })

	// Named readers with no room for their views try to take every place
	// left. MaxNamedWaiting of them wait, counting the eight, and the rest
	// answer 503 at once, well before their deadline.
	var refused atomic.Int32
	var waited []chan *httptest.ResponseRecorder
	for i := range MaxLoadWaiting - MaxViews {
		served := h.serving(fmt.Sprintf("r%d", 9+i%7), "/-/knots", context.Background())
		answered := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			w := <-served
			if w.Code == http.StatusServiceUnavailable && w.Header().Get("Retry-After") != "" {
				refused.Add(1)
			}
			answered <- w
		}()
		waited = append(waited, answered)
	}
	eventually(t, "named readers fill the places they may take", func() bool {
		return len(h.srv.namedWaiting) == MaxNamedWaiting && refused.Load() == MaxLoadWaiting-MaxNamedWaiting
	})
	if n := len(h.srv.loadWaiting); n != MaxNamedWaiting {
		t.Fatalf("%d requests wait, not the %d named readers may take", n, MaxNamedWaiting)
	}

	// A new generation, whose load is held until every anonymous request
	// waits for it in a place of its own.
	loadHold := make(chan struct{})
	h.srv.loading = func(export.Generation) { <-loadHold }
	app := e.Projects[0]
	app.Knots = slices.Clone(app.Knots)
	app.Knots[0].Title = "Readers browse the next plan"
	next := *e
	next.Projects = slices.Clone(e.Projects)
	next.Projects[0] = app
	h.publishExport(exporttest.SignedAt.Add(2*time.Minute), &next, bundles)
	var anonymous []chan *httptest.ResponseRecorder
	for range MaxLoadWaiting - MaxNamedWaiting {
		anonymous = append(anonymous, h.serving(export.Anyone, "/tools/app/knots/oc-a000001", context.Background()))
	}
	eventually(t, "every anonymous request waits for the load", func() bool { return len(h.srv.loadWaiting) == MaxLoadWaiting })
	close(loadHold)
	for _, served := range anonymous {
		if w := <-served; w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Readers browse the next plan") {
			t.Errorf("an anonymous request answered %d, not from the next generation", w.Code)
		}
	}

	close(hold)
	answered := 0
	for _, served := range append(named, waited...) {
		switch w := <-served; w.Code {
		case http.StatusOK:
			answered++
		case http.StatusServiceUnavailable:
		default:
			t.Errorf("a named reader's request answered %d", w.Code)
		}
	}
	if want := MaxNamedWaiting; answered != want {
		t.Errorf("%d named readers' requests were served, not the %d that waited", answered, want)
	}
	if _, views, pins := h.srv.heldViews(); views > MaxViews || pins != 0 {
		t.Errorf("the server holds %d named views, pinned %d times", views, pins)
	}
	if len(h.srv.loadWaiting) != 0 || len(h.srv.namedWaiting) != 0 {
		t.Errorf("%d requests, %d of them named, still hold a place among those that wait", len(h.srv.loadWaiting), len(h.srv.namedWaiting))
	}
}

// A view whose build is stopped, because the last request that waited for
// it went away, keeps its place until its worker ends, and no request
// takes it again. Here the stopped worker does not notice it was stopped
// until it is let go. Meanwhile a request for another view, and one for
// the same view, wait for room, and no worker starts beyond the MaxViews
// places. Once the stopped worker ends, both are served, and at no time
// do more than MaxViews workers build named views.
func TestAStoppedBuildKeepsItsPlace(t *testing.T) {
	h := newHarness(t)
	e, _ := h.privateViews()
	h.page("/-/knots")
	// r1 alone may read p1 and nothing else.
	r1 := projectsKey(e, map[string]bool{"app": true, "p1": true})

	release, rest := make(chan struct{}), make(chan struct{})
	var live, peak, r1Builds atomic.Int32
	h.srv.indexing = func(_ context.Context, key viewKey) {
		if !strings.Contains(key.projects, "\np") {
			return
		}
		n := live.Add(1)
		defer live.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		if key.projects == r1 && r1Builds.Add(1) == 1 {
			<-release
			return
		}
		<-rest
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := h.serving("r1", "/-/knots", ctx)
	var others []chan *httptest.ResponseRecorder
	for i := 2; i <= MaxViews; i++ {
		others = append(others, h.serving(fmt.Sprintf("r%d", i), "/-/knots", context.Background()))
	}
	eventually(t, "every named place holds a view being built", func() bool { return live.Load() == MaxViews })

	// r1 goes away. Its view's build is stopped, though its worker runs on.
	cancel()
	if w := <-first; w.Body.Len() != 0 {
		t.Errorf("a request whose reader went away answered %d", w.Code)
	}
	if n := h.srv.stoppingViews(); n != 1 {
		t.Fatalf("%d stopped views keep their places, not 1", n)
	}

	// Another reader's view and r1's view again find no room.
	another := h.serving("r9", "/-/knots", context.Background())
	again := h.serving("r1", "/-/knots", context.Background())
	eventually(t, "both requests wait for room", func() bool { return len(h.srv.loadWaiting) == MaxViews+1 })
	time.Sleep(20 * time.Millisecond)
	if n := live.Load(); n != MaxViews {
		t.Errorf("%d workers build named views while a stopped one runs", n)
	}

	// The stopped worker ends, and gives back its place.
	close(release)
	eventually(t, "the stopped view gives back its place", func() bool { return h.srv.stoppingViews() == 0 })
	close(rest)
	for _, served := range append(others, another, again) {
		if w := <-served; w.Code != http.StatusOK {
			t.Errorf("a named reader's request answered %d", w.Code)
		}
	}
	if p := peak.Load(); p > MaxViews {
		t.Errorf("%d workers built named views at once, more than the %d places", p, MaxViews)
	}
	if _, views, pins := h.srv.heldViews(); views > MaxViews || pins != 0 || h.srv.stoppingViews() != 0 {
		t.Errorf("the server holds %d named views, pinned %d times", views, pins)
	}
}

// Loads that only named readers wait for take one of the MaxLoads
// workers at most, so a load that an anonymous request waits for always
// has one: while one named reader's load runs and another's waits for a
// worker, an anonymous request joins the waiting load and starts it.
func TestAnonymousRequestsKeepALoader(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	holds := map[int64]chan struct{}{}
	hold := func(seq int64) chan struct{} {
		mu.Lock()
		defer mu.Unlock()
		if holds[seq] == nil {
			holds[seq] = make(chan struct{})
		}
		return holds[seq]
	}
	var loaded sync.Map
	h.srv.loading = func(g export.Generation) {
		loaded.Store(g.Sequence, true)
		<-hold(g.Sequence)
	}
	serve := func(reader string) chan *httptest.ResponseRecorder {
		served := make(chan *httptest.ResponseRecorder, 1)
		go func() { served <- h.as(reader, "GET", "/tools/app/knots/oc-a000001", nil) }()
		return served
	}
	first := h.active()
	alice := serve("alice")
	eventually(t, "alice's load runs", func() bool { _, ok := loaded.Load(first.Sequence); return ok })
	second := h.publish(exporttest.SignedAt.Add(time.Minute), nil, renamed("Readers browse the second plan"))
	bob := serve("bob")
	eventually(t, "bob waits for a load", func() bool { return len(h.srv.loadWaiting) == 2 })
	time.Sleep(20 * time.Millisecond)
	if _, ok := loaded.Load(second.Sequence); ok || h.srv.runningLoads() != 1 {
		t.Fatalf("a second load that only named readers wait for started, with %d workers", h.srv.runningLoads())
	}
	anyone := serve(export.Anyone)
	eventually(t, "the anonymous request starts the waiting load", func() bool { _, ok := loaded.Load(second.Sequence); return ok })
	if len(h.srv.anonLoader) != 1 || len(h.srv.loaders) != 1 {
		t.Errorf("the loads hold %d anonymous and %d shared workers", len(h.srv.anonLoader), len(h.srv.loaders))
	}
	close(hold(second.Sequence))
	close(hold(first.Sequence))
	for _, served := range []chan *httptest.ResponseRecorder{alice, bob, anyone} {
		if w := <-served; w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "second plan") {
			t.Errorf("a request answered %d, not from the second generation", w.Code)
		}
	}
}

// holdFirstReads makes the first read of each generation held with hold
// wait until the function hold returns is called. reads counts each
// generation's reads.
func holdFirstReads(h *harness) (hold func(export.Generation) func(), reads func(export.Generation) int) {
	var mu sync.Mutex
	holds := map[int64]chan struct{}{}
	counts := map[int64]int{}
	h.srv.loading = func(g export.Generation) {
		mu.Lock()
		counts[g.Sequence]++
		held, first := holds[g.Sequence], counts[g.Sequence] == 1
		mu.Unlock()
		if first && held != nil {
			<-held
		}
	}
	hold = func(g export.Generation) func() {
		mu.Lock()
		defer mu.Unlock()
		held := make(chan struct{})
		holds[g.Sequence] = held
		return func() { close(held) }
	}
	reads = func(g export.Generation) int {
		mu.Lock()
		defer mu.Unlock()
		return counts[g.Sequence]
	}
	return hold, reads
}

// When the last anonymous request leaves a load on the kept loader while
// a named reader still waits for it, and the other loader is free, the
// load moves to the other loader and runs on: the kept loader is free for
// the next anonymous request at once, and the export is read only once.
func TestALoadAnonymousRequestsLeaveMovesToTheOtherLoader(t *testing.T) {
	h := newHarness(t)
	hold, reads := holdFirstReads(h)
	const target = "/tools/app/knots/oc-a000001"
	first := h.active()
	letFirstGo := hold(first)
	ctx, cancel := context.WithCancel(context.Background())
	anyone := h.serving(export.Anyone, target, ctx)
	eventually(t, "the anonymous request's load runs", func() bool { return reads(first) == 1 })
	if len(h.srv.anonLoader) != 1 {
		t.Fatal("the anonymous request's load does not run on the kept loader")
	}
	bob := h.serving("bob", target, context.Background())
	eventually(t, "bob joins the load", func() bool { a, n := h.srv.loadWaiters(); return a == 1 && n == 1 })

	cancel()
	if w := <-anyone; w.Body.Len() != 0 {
		t.Errorf("a request whose reader went away answered %d", w.Code)
	}
	if len(h.srv.anonLoader) != 0 || len(h.srv.loaders) != 1 {
		t.Errorf("the load holds %d kept and %d other loaders, not the other alone", len(h.srv.anonLoader), len(h.srv.loaders))
	}
	if a, n := h.srv.loadWaiters(); a != 0 || n != 1 {
		t.Errorf("%d anonymous and %d named requests wait for loads, not bob alone", a, n)
	}

	// While bob's load runs on, an anonymous request for a newer
	// generation is read on the kept loader and served.
	h.publish(exporttest.SignedAt.Add(time.Minute), nil, renamed("Readers browse the second plan"))
	if w := h.as(export.Anyone, "GET", target, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "second plan") {
		t.Errorf("an anonymous request answered %d, not from the second generation", w.Code)
	}
	letFirstGo()
	if w := <-bob; w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "second plan") {
		t.Errorf("bob's request answered %d, not from the second generation", w.Code)
	}
	if n := reads(first); n != 1 {
		t.Errorf("the first generation was read %d times, not once", n)
	}
	eventually(t, "every loader is given back", func() bool { return h.srv.runningLoads() == 0 })
}

// When the last anonymous request leaves a load on the kept loader while
// a named reader still waits for it, and the other loader runs a named
// reader's load, the load is stopped and the kept loader is free at once,
// though the stopped worker has not ended. Here alice's load of the first
// generation holds the other loader. An anonymous request starts the
// second generation's load on the kept loader, bob joins it, and the
// anonymous request goes away. bob waits for a load again. Then either a
// third generation is activated, and an anonymous request for it is
// served while alice's load and the stopped worker still run, or alice's
// load ends, and bob's load takes the other loader. Every request is
// served, and once every worker has ended the loads hold no loader.
func TestALoadAnonymousRequestsLeaveGivesBackTheKeptLoader(t *testing.T) {
	const target = "/tools/app/knots/oc-a000001"
	stopped := func(t *testing.T) (h *harness, letFirstGo, letSecondGo func(), alice, bob chan *httptest.ResponseRecorder, reads func(export.Generation) int, second export.Generation) {
		h = newHarness(t)
		var hold func(export.Generation) func()
		hold, reads = holdFirstReads(h)
		first := h.active()
		letFirstGo = hold(first)
		alice = h.serving("alice", target, context.Background())
		eventually(t, "alice's load runs", func() bool { return reads(first) == 1 })
		if len(h.srv.loaders) != 1 {
			t.Fatal("alice's load does not run on the other loader")
		}

		second = h.publish(exporttest.SignedAt.Add(time.Minute), nil, renamed("Readers browse the second plan"))
		letSecondGo = hold(second)
		ctx, cancel := context.WithCancel(context.Background())
		anyone := h.serving(export.Anyone, target, ctx)
		eventually(t, "the anonymous request's load runs", func() bool { return reads(second) == 1 })
		bob = h.serving("bob", target, context.Background())
		eventually(t, "bob joins the load", func() bool { a, n := h.srv.loadWaiters(); return a == 1 && n == 2 })

		cancel()
		if w := <-anyone; w.Body.Len() != 0 {
			t.Errorf("a request whose reader went away answered %d", w.Code)
		}
		if len(h.srv.anonLoader) != 0 || len(h.srv.loaders) != 1 {
			t.Errorf("the loads hold %d kept and %d other loaders, not the other alone", len(h.srv.anonLoader), len(h.srv.loaders))
		}
		eventually(t, "bob waits for a load again", func() bool { a, n := h.srv.loadWaiters(); return a == 0 && n == 2 })
		return
	}
	expect := func(t *testing.T, title string, served ...chan *httptest.ResponseRecorder) {
		t.Helper()
		for _, w := range served {
			if w := <-w; w.Code != http.StatusOK || !strings.Contains(w.Body.String(), title) {
				t.Errorf("a request answered %d, not with %q", w.Code, title)
			}
		}
	}

	t.Run("an anonymous request for a newer generation", func(t *testing.T) {
		h, letFirstGo, letSecondGo, alice, bob, _, _ := stopped(t)
		h.publish(exporttest.SignedAt.Add(2*time.Minute), nil, renamed("Readers browse the third plan"))
		if w := h.as(export.Anyone, "GET", target, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "third plan") {
			t.Errorf("the anonymous request answered %d, not from the third generation", w.Code)
		}
		letFirstGo()
		letSecondGo()
		expect(t, "Readers browse the third plan", alice, bob)
		eventually(t, "every loader is given back", func() bool { return h.srv.runningLoads() == 0 })
	})

	t.Run("the other loader comes free", func(t *testing.T) {
		h, letFirstGo, letSecondGo, alice, bob, reads, second := stopped(t)
		letFirstGo()
		expect(t, "Readers browse the second plan", alice, bob)
		if n := reads(second); n != 2 {
			t.Errorf("the second generation was read %d times, not by the stopped load and bob's", n)
		}
		letSecondGo()
		eventually(t, "every loader is given back", func() bool { return h.srv.runningLoads() == 0 })
	})
}

// A response a named reader was rendered is written only under the
// authorization it was rendered under. One whose grant is withdrawn while
// it renders, a Markdown page, an archive, a knot or a redirect, is
// rendered again under the new authorization, and answers the one 404.
func TestANamedReadersResponseIsHeldToTheAuthorization(t *testing.T) {
	for _, path := range []string{"/tools/site/source/blob/%s/README.md", "/tools/site/source/archive/%s.tar.gz",
		"/tools/site/knots/oc-b000001", "/site/", "/tools/site/source/tree/%s/"} {
		t.Run(path, func(t *testing.T) {
			h := newHarness(t)
			e := h.edge(exporttest.SignedAt.Add(time.Minute), map[string][]string{"app": {export.Anyone}, "site": {"alice"}})
			siteTip := git(t, h.f.Repos["site"], nil, "rev-parse", export.MainRef)
			target := strings.ReplaceAll(path, "%s", siteTip)
			if w := h.as("alice", "GET", target, nil); w.Code != http.StatusOK && w.Code != http.StatusPermanentRedirect {
				t.Fatalf("GET %s as alice answered %d", target, w.Code)
			}
			missing := h.as("alice", "GET", "/tools/nosuch/", nil)
			renders := 0
			h.srv.checked = func() {
				if renders++; renders == 1 {
					h.authorizeReaders(e, map[string][]string{"app": {export.Anyone}})
				}
			}
			w := h.as("alice", "GET", target, nil)
			if w.Code != http.StatusNotFound || !bytes.Equal(w.Body.Bytes(), missing.Body.Bytes()) || w.Header().Get("Location") != "" {
				t.Errorf("GET %s withdrawn while it rendered answered %d, to %q", target, w.Code, w.Header().Get("Location"))
			}
			if renders != 2 {
				t.Errorf("GET %s rendered %d times, want twice", target, renders)
			}
		})
	}
}
