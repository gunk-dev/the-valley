package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"the-valley/note"
	"the-valley/web/export"
	"the-valley/web/export/exporttest"
)

// publishedReader is the request context's key under which
// TestThePublishersOutputServesEachReader names the reader a request is
// read as.
type publishedReader struct{}

// The real publisher's output (web-published in nix/packages.nix), in the
// directory VALLEY_WEB_PUBLISHED names. Anyone may read app. alice alone
// may read site, and the operator alone docs. notes has no effective
// reader. The store admits the authorization and the manifest under the
// export key's verifier line, and takes the bodies, as a receiver does.
//
// The export holds app, docs and site, and nothing notes' knots say.
// Three of app's knots are each blocked by an open, an abandoned and a
// missing knot of site, docs or notes. A blocker in an exported project
// keeps every derived field and its edges in the export; one in notes
// leaves only waiting_on_hidden and hidden edges. projectForReader then
// takes out, for each reader, what those fields and edges say of an
// exported project the reader may not see.
//
// The server serves each reader the union of what anyone and they may
// read. For anyone, bob, alice and the operator, no page it renders, a
// knot, the knots page, a source page, a redirect or an error, holds the
// name, alias, former name, README, knot titles, comment or ready change
// of a private project the reader may not see.
func TestThePublishersOutputServesEachReader(t *testing.T) {
	dir := os.Getenv("VALLEY_WEB_PUBLISHED")
	if dir == "" {
		if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
			t.Fatal("VALLEY_WEB_PUBLISHED names no publisher output")
		}
		t.Skip("VALLEY_WEB_PUBLISHED names no publisher output")
	}
	ctx := context.Background()
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	verifiers, err := note.ParseVerifierKeys(read("verifier"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := export.OpenStore(exporttest.StorePath(t), func(err error) { t.Errorf("the store reported: %v", err) })
	if err != nil {
		t.Fatal(err)
	}

	signed := read("published/authorization")
	a, err := export.VerifyAuthorization(bytes.NewReader(signed), export.AuthorizationAdmission{
		Verifiers: verifiers, Instance: exporttest.Instance, Destination: exporttest.Destination})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Authorize(ctx, a, signed); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		reader, project string
		allowed         bool
	}{
		{export.Anyone, "app", true}, {export.Anyone, "site", false}, {export.Anyone, "docs", false},
		{"alice", "site", true}, {"alice", "docs", false}, {"operator", "docs", true}, {"operator", "site", false},
		{"bob", "site", false}, {"bob", "docs", false},
	} {
		p, _ := a.Project(c.project)
		if got := a.Allows(c.reader, c.project, p.Identity); got != c.allowed {
			t.Errorf("the authorization lets %s read %s: %v, want %v", c.reader, c.project, got, c.allowed)
		}
	}
	if _, named := a.Project("notes"); named {
		t.Errorf("the authorization names notes, which has no effective reader")
	}

	// The manifest was signed when the publisher ran; it is admitted a
	// second after its signing time, as a receiver admits it then.
	signedManifest := read("published/manifest")
	text, _, err := note.Open(signedManifest, verifiers)
	if err != nil {
		t.Fatal(err)
	}
	unverified, err := export.ParseManifest(text)
	if err != nil {
		t.Fatal(err)
	}
	m, err := export.VerifyManifest(bytes.NewReader(signedManifest), export.Admission{
		Verifiers: verifiers, Instance: exporttest.Instance, Destination: exporttest.Destination,
		Now: time.Unix(0, unverified.Sequence).Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Stage(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.Inventory(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, need := range inv.Needs {
		path := filepath.Join("published", "bundles", need.Name+".bundle")
		if need.Name == export.ExportFile {
			path = filepath.Join("published", export.ExportFile)
		}
		req := httptest.NewRequest("PUT", export.BodyPath(m.Sequence, need.SHA256), bytes.NewReader(read(path)))
		if _, err := export.StageBody(st.Bodies(), export.Body{Name: need.Name, SHA256: need.SHA256, Size: need.Size}, 64<<20, req); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Activate(ctx, st); err != nil {
		t.Fatal(err)
	}

	data := read(filepath.Join("published", export.ExportFile))
	e, err := export.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	projects := map[string]export.Project{}
	for _, p := range e.Projects {
		names = append(names, p.Name)
		projects[p.Name] = p
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"app", "docs", "site"}) {
		t.Fatalf("the export holds %v, want app, docs and site", names)
	}
	for _, text := range []string{"notes-readme-text", "notes-knot-text", "notes-abandoned-text", "notes-comment-text"} {
		if bytes.Contains(data, []byte(text)) {
			t.Errorf("export.json holds %s, which only notes says", text)
		}
	}
	knotOf := func(e *export.Export, project, title string) *export.Knot {
		t.Helper()
		for i := range e.Projects {
			if e.Projects[i].Name != project {
				continue
			}
			for j := range e.Projects[i].Knots {
				if k := &e.Projects[i].Knots[j]; k.Title == title {
					return k
				}
			}
		}
		t.Fatalf("%s has no knot %q", project, title)
		return nil
	}
	waits := map[string]string{"site": "Waits on the first private project", "docs": "Waits on the second private project",
		"notes": "Waits on the third private project"}

	// What the export says of each blocker app's knots name: everything
	// the plan derived of one in site or docs, and of one in notes, which
	// is not exported, that the knot waits on something hidden.
	for _, project := range []string{"site", "docs"} {
		k := knotOf(e, "app", waits[project])
		open := project + ":" + knotOf(e, project, project+"-knot-text").ID
		ended := project + ":" + knotOf(e, project, project+"-abandoned-text").ID
		missing := project + ":oc-0000000"
		for _, c := range []struct {
			field     string
			got, want []string
		}{
			{"waiting_on", k.WaitingOn, []string{missing, open}}, {"cleared_by_ending", k.ClearedByEnding, []string{ended}},
			{"dangling", k.Dangling, []string{missing}}, {"unresolved", k.Unresolved, []string{}},
		} {
			if !sameSet(c.got, c.want) {
				t.Errorf("the export's %s of app's knot blocked by %s is %q, want %q", c.field, project, c.got, c.want)
			}
		}
		if k.WaitingOnHidden {
			t.Errorf("the export says app's knot blocked by %s waits on a hidden knot", project)
		}
		edges := map[string]export.Edge{}
		for _, edge := range k.Edges {
			edges[edge.Ref] = edge
		}
		for ref, want := range map[string][3]string{
			open: {export.EdgeResolved, "open", project + "-knot-text"}, ended: {export.EdgeResolved, "abandoned", project + "-abandoned-text"},
			missing: {export.EdgeDangling, "", ""},
		} {
			edge, ok := edges[ref]
			if !ok || edge.State != want[0] || deref(edge.Status) != want[1] || deref(edge.Title) != want[2] {
				t.Errorf("the export's edge to %s is %+v, want %s with status %q and title %q", ref, edge, want[0], want[1], want[2])
			}
		}
		if len(edges) != 3 {
			t.Errorf("app's knot blocked by %s has %d edges, want 3", project, len(edges))
		}
	}
	unexported := knotOf(e, "app", waits["notes"])
	if !unexported.WaitingOnHidden || len(unexported.WaitingOn)+len(unexported.ClearedByEnding)+len(unexported.Dangling)+len(unexported.Unresolved) > 0 {
		t.Errorf("the export says of app's knot blocked by notes more than that it waits on a hidden knot: %+v", unexported)
	}
	if len(unexported.Edges) != 3 {
		t.Errorf("app's knot blocked by notes has %d edges, want 3", len(unexported.Edges))
	}
	for _, edge := range unexported.Edges {
		if want := (export.Edge{Field: "blocked_by", Ref: edge.Ref, Project: "notes", ID: edge.ID, State: export.EdgeHidden}); !reflect.DeepEqual(edge, want) {
			t.Errorf("the export's edge into notes is %+v, want %+v", edge, want)
		}
	}

	// Each reader's projection keeps what app's knots say of the
	// projects that reader may see, and takes out the rest: a blocker in
	// another private project leaves waiting_on_hidden and hidden edges
	// alone, as one in notes does in the export. The real publisher
	// writes no unresolved reference into a project it exports, so a
	// second copy of the export is given one into site and one into docs.
	shownTo := map[string][]string{
		export.Anyone: {"app"}, "bob": {"app"}, "alice": {"app", "site"}, "operator": {"app", "docs"},
	}
	readers := []string{export.Anyone, "bob", "alice", "operator"}
	for _, reader := range readers {
		shown := map[string]bool{}
		for _, p := range shownTo[reader] {
			shown[p] = true
		}
		out, err := projectForReader(ctx, e, shown)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, p := range out.Projects {
			got = append(got, p.Name)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, shownTo[reader]) {
			t.Errorf("%s's projection holds %v, want %v", reader, got, shownTo[reader])
		}
		for _, project := range []string{"site", "docs", "notes"} {
			k, orig := knotOf(out, "app", waits[project]), knotOf(e, "app", waits[project])
			if shown[project] || project == "notes" {
				if !reflect.DeepEqual(k, orig) {
					t.Errorf("%s's projection changed app's knot blocked by %s:\n%+v\nwant\n%+v", reader, project, k, orig)
				}
				continue
			}
			if !k.WaitingOnHidden || len(k.WaitingOn)+len(k.ClearedByEnding)+len(k.Dangling)+len(k.Unresolved) > 0 {
				t.Errorf("%s's projection says of app's knot blocked by %s more than that it waits on a hidden knot: %+v", reader, project, k)
			}
			if len(k.Edges) != len(orig.Edges) {
				t.Fatalf("%s's projection has %d edges of app's knot blocked by %s, want %d", reader, len(k.Edges), project, len(orig.Edges))
			}
			for i, edge := range k.Edges {
				o := orig.Edges[i]
				if want := (export.Edge{Field: o.Field, Ref: o.Ref, Project: o.Project, ID: o.ID, State: export.EdgeHidden}); !reflect.DeepEqual(edge, want) {
					t.Errorf("%s's projection keeps the edge %+v, want %+v", reader, edge, want)
				}
			}
		}
	}
	withUnresolved, err := export.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	unresolved := []string{"site:oc-1111111", "docs:oc-1111111"}
	knotOf(withUnresolved, "app", waits["site"]).Unresolved = unresolved
	for _, reader := range readers {
		shown := map[string]bool{}
		var want []string
		for _, p := range shownTo[reader] {
			shown[p] = true
			if p != "app" {
				want = append(want, p+":oc-1111111")
			}
		}
		out, err := projectForReader(ctx, withUnresolved, shown)
		if err != nil {
			t.Fatal(err)
		}
		if got := knotOf(out, "app", waits["site"]).Unresolved; !sameSet(got, want) {
			t.Errorf("%s's projection keeps the unresolved references %q, want %q", reader, got, want)
		}
	}

	srv := New(Config{Store: store, Now: time.Now, Log: func(err error) { t.Errorf("the server logged: %v", err) }})
	srv.reader = func(r *http.Request) string {
		if name, ok := r.Context().Value(publishedReader{}).(string); ok {
			return name
		}
		return export.Anyone
	}
	get := func(reader, target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil)
		r = r.WithContext(context.WithValue(r.Context(), publishedReader{}, reader))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}

	// Every page of a project: its home, source pages and knots. The
	// archive is checked for its status alone, as it is compressed.
	pages := func(project string) []string {
		p := projects[project]
		base := "/tools/" + project
		out := []string{base + "/", base + "/source/tree/" + p.SourceTip + "/", base + "/source/blob/" + p.SourceTip + "/README.md",
			base + "/source/log/" + p.SourceTip}
		for _, k := range p.Knots {
			out = append(out, base+"/knots/"+k.ID)
		}
		return out
	}
	archive := func(project string) string {
		return "/tools/" + project + "/source/archive/" + projects[project].SourceTip + ".tar.gz"
	}
	private := map[string]struct{ alias, former string }{"site": {"portal", "homepage"}, "docs": {"handbook", "manual"}}
	// What only a private project says: its name, alias, former name,
	// README, knot titles, comment and ready change.
	secrets := func(project string) []string {
		out := []string{project, project + "-readme-text", project + "-comment-text", project + "-change-subject"}
		if p, ok := private[project]; ok {
			out = append(out, p.alias, p.former)
			for _, k := range projects[project].Knots {
				out = append(out, k.Title)
			}
			for _, c := range projects[project].Changes {
				out = append(out, c.ChangeID)
			}
		} else {
			out = append(out, project+"-knot-text", project+"-abandoned-text")
		}
		return out
	}
	for _, reader := range readers {
		shown := map[string]bool{}
		for _, p := range shownTo[reader] {
			shown[p] = true
		}
		var hidden []string
		answers := map[string]int{"/": http.StatusFound, knotsURL: http.StatusOK, "/nowhere/": http.StatusNotFound,
			"/tools/nowhere/": http.StatusNotFound, archive("app"): http.StatusOK}
		for _, project := range []string{"app", "site", "docs"} {
			want := http.StatusNotFound
			if shown[project] {
				want = http.StatusOK
			} else {
				hidden = append(hidden, secrets(project)...)
			}
			for _, target := range pages(project) {
				answers[target] = want
			}
			if shown[project] {
				answers["/tools/"+project+"/"] = http.StatusFound
			}
			if p, ok := private[project]; ok {
				moved := http.StatusPermanentRedirect
				if !shown[project] {
					moved = http.StatusNotFound
				}
				answers["/"+p.alias+"/"] = moved
				answers["/tools/"+p.former+"/source/tree/"+projects[project].SourceTip+"/"] = moved
			}
		}
		hidden = append(hidden, secrets("notes")...)
		answers["/tools/notes/"] = http.StatusNotFound
		for target, want := range answers {
			w := get(reader, target)
			if w.Code != want {
				t.Errorf("GET %s as %s answered %d, want %d", target, reader, w.Code, want)
			}
			if strings.HasSuffix(target, ".tar.gz") {
				continue
			}
			// The layout's navigation is of class site, in its markup and
			// its stylesheet, which names no project.
			page := w.Header().Get("Location") + "\n" + layoutSite.Replace(w.Body.String())
			for _, secret := range hidden {
				if strings.Contains(page, secret) {
					t.Errorf("GET %s as %s answered with %q, which only a project %s may not read says", target, reader, secret, reader)
				}
			}
		}
		for project, title := range waits {
			page := get(reader, "/tools/app/knots/"+knotOf(e, "app", title).ID).Body.String()
			if names := strings.Contains(page, project+"-knot-text"); names != shown[project] {
				t.Errorf("app's knot blocked by %s names its blocker to %s: %v, want %v", project, reader, names, shown[project])
			}
		}
	}
}

// layoutSite takes out of a page the layout's class site.
var layoutSite = strings.NewReplacer(`<nav class="site"`, "", "nav.site", "")

// sameSet reports whether a and b hold the same strings, in any order.
func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	sort.Strings(a)
	sort.Strings(b)
	return slices.Equal(a, b)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
