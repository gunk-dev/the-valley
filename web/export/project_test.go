package export_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"the-valley/web/export"
	"the-valley/web/export/exporttest"
)

// Nothing the anonymous reader may not see reaches export.json: not the
// hidden project's knots or changes, not the plan's operator sections,
// not drafts, branches, older versions, repository URLs or request refs,
// not a knot's storage path, and not one principal's own name.
func TestBuildLeavesOutWhatIsNotTheReaders(t *testing.T) {
	f := exporttest.New(t)
	for _, leak := range []string{
		`"path"`, "outcomes/", "waiting_on_you", `"operator":`, "changes_error",
		"The site deploys the app", "The site serves the docs", "Hidden.", "site-operator",
		"lmnoplmnop", "deploy: a hidden change",
		"zyxwvutsrq", "wip: a draft", "app-agent-draft", "scratch",
		"plan: list milestones", "4c4c4c4c", "app-agent-session",
		"git@", "valley-host", "integration-requests", `"record"`, `"seq"`, `"restack"`,
		"app-operator", "app-agent", "drive-by",
	} {
		if bytes.Contains(f.ExportJSON, []byte(leak)) {
			t.Errorf("export.json holds %q", leak)
		}
	}
	if len(f.Export.Projects) != 1 || f.Export.Projects[0].Name != "app" {
		t.Fatalf("exported %v, want app alone", f.Export.Projects)
	}
}

func knotOf(t *testing.T, e *export.Export, id string) export.Knot {
	t.Helper()
	for _, k := range e.Projects[0].Knots {
		if k.ID == id {
			return k
		}
	}
	t.Fatalf("the export holds no knot %s", id)
	return export.Knot{}
}

// A reference to the hidden project's knot keeps only the reference. It
// still holds its knot up: a knot it blocks stays blocked, and is never
// shown as free to start. A knot that waits on it says so with
// waiting_on_hidden alone, and no other knot carries the field.
func TestBuildHidesForeignEdges(t *testing.T) {
	f := exporttest.New(t)
	k := knotOf(t, f.Export, "oc-a000003")
	if len(k.Edges) != 1 {
		t.Fatalf("edges %v", k.Edges)
	}
	e := k.Edges[0]
	if e.State != export.EdgeHidden || e.Status != nil || e.Title != nil || e.Why != nil || e.Ref != "site:oc-b000001" {
		t.Errorf("the hidden edge is %+v", e)
	}
	if slices.Contains(k.WaitingOn, "site:oc-b000001") || !slices.Contains(k.WaitingOn, "oc-a000002") {
		t.Errorf("waiting_on is %v; it keeps the local blocker and drops the hidden one", k.WaitingOn)
	}
	if !k.WaitingOnHidden {
		t.Error("waiting_on_hidden is not set on the knot that waits on a hidden knot")
	}
	for _, other := range f.Export.Projects[0].Knots {
		if other.ID != k.ID && other.WaitingOnHidden {
			t.Errorf("%s carries waiting_on_hidden, and waits on no hidden knot", other.ID)
		}
	}
	if n := strings.Count(string(f.ExportJSON), `"waiting_on_hidden"`); n != 1 {
		t.Errorf("export.json holds waiting_on_hidden %d times, not once", n)
	}
	if k.State != "blocked" || k.Ready {
		t.Errorf("the knot is %s, ready %v; it stays blocked", k.State, k.Ready)
	}
	if !slices.Equal(k.BlockedBy, []string{"oc-a000002", "site:oc-b000001"}) {
		t.Errorf("blocked_by is %v; it is the knot's own field, as written", k.BlockedBy)
	}
	if target := knotOf(t, f.Export, "bd-a000004").Edges; len(target) != 1 || target[0].State != export.EdgeHidden || target[0].Field != "target" {
		t.Errorf("the target edge is %+v", target)
	}
}

// The export keeps each hidden reference's ID, in blocked_by and edges,
// and waiting_on_hidden says whether the knot still waits on a hidden
// knot. So for a knot that names one hidden blocker, as oc-a000003 names
// site:oc-b000001, the field says whether that hidden knot still blocks.
// The export permits this inference: the field is set while
// site:oc-b000001 is open, and gone once it is done, though oc-a000003
// stays blocked by its published blocker and still names site:oc-b000001.
func TestWaitingOnHiddenFollowsAHiddenBlocker(t *testing.T) {
	f := exporttest.New(t)
	if !knotOf(t, f.Export, "oc-a000003").WaitingOnHidden {
		t.Fatal("waiting_on_hidden is not set while the hidden blocker is open")
	}

	in := f.Inputs
	var plan map[string]any
	if err := json.Unmarshal(in.Plan, &plan); err != nil {
		t.Fatal(err)
	}
	knot := func(project, id string) map[string]any {
		for _, p := range plan["projects"].([]any) {
			p := p.(map[string]any)
			for _, k := range p["knots"].([]any) {
				if k := k.(map[string]any); p["project"] == project && k["id"] == id {
					return k
				}
			}
		}
		t.Fatalf("the plan holds no knot %s:%s", project, id)
		return nil
	}
	hidden := knot("site", "oc-b000001")
	hidden["status"], hidden["active"], hidden["state"], hidden["ready"] = "done", false, "done", false
	k := knot("app", "oc-a000003")
	k["waiting_on"] = []string{"oc-a000002"}
	k["edges"].([]any)[0].(map[string]any)["status"] = "done"
	var err error
	if in.Plan, err = json.Marshal(plan); err != nil {
		t.Fatal(err)
	}
	e, err := export.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	x := knotOf(t, e, "oc-a000003")
	if x.WaitingOnHidden {
		t.Error("waiting_on_hidden is still set once the hidden blocker is done")
	}
	if x.State != "blocked" || !slices.Equal(x.WaitingOn, []string{"oc-a000002"}) {
		t.Errorf("the knot is %s, waiting on %v; its published blocker still holds it", x.State, x.WaitingOn)
	}
	if !slices.Contains(x.BlockedBy, "site:oc-b000001") || len(x.Edges) != 1 || x.Edges[0].Ref != "site:oc-b000001" {
		t.Errorf("blocked_by is %v and edges %+v; both still name the hidden blocker", x.BlockedBy, x.Edges)
	}
}

// A reference to another exported project's knot keeps what the plan
// says of it.
func TestBuildKeepsEdgesBetweenExportedProjects(t *testing.T) {
	f := exporttest.New(t)
	in := f.Inputs
	in.Identities = map[string]string{"app": exporttest.Identities["app"], "site": "273d5668702918ea7ae079de374db96b"}
	in.SourceTips = map[string]string{"app": in.SourceTips["app"], "site": strings.Repeat("e", 40)}
	in.Details = append(in.Details,
		detail("site", "8c2e4a6b0d1f3e5a7c9b1d3f5e7a9c0b2d4f6e81", "oc-b000001"),
		detail("site", "8c2e4a6b0d1f3e5a7c9b1d3f5e7a9c0b2d4f6e81", "oc-b000002"))
	e, err := export.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	edge := knotOf(t, e, "oc-a000003").Edges[0]
	if edge.State != export.EdgeResolved || edge.Title == nil || *edge.Title != "The site deploys the app" {
		t.Errorf("the edge is %+v", edge)
	}
	if !slices.Contains(knotOf(t, e, "oc-a000003").WaitingOn, "site:oc-b000001") {
		t.Error("waiting_on dropped an exported project's blocker")
	}
	if knotOf(t, e, "oc-a000003").WaitingOnHidden {
		t.Error("waiting_on_hidden is set for a blocker in an exported project")
	}
}

func detail(project, tip, id string) []byte {
	b, _ := json.Marshal(map[string]any{"schema": "valley-knot/v1", "project": project, "tip": tip,
		"knot": map[string]any{"id": id, "body": "", "comments": []any{}}})
	return b
}

// In flight is the plan's claimed knots, oldest claim first, and then the
// knots explicitly in progress that nobody claims.
func TestBuildInFlight(t *testing.T) {
	f := exporttest.New(t)
	if got := f.Export.Projects[0].InFlight; !slices.Equal(got, []string{"oc-a000002", "oc-a000006"}) {
		t.Errorf("in_flight is %v", got)
	}
}

// Principal names pass through the display mapping, and a name it does
// not map shows as the generic role. An edited comment's editor is a
// principal too, and a comment never edited names no editor.
func TestBuildMapsPrincipals(t *testing.T) {
	f := exporttest.New(t)
	claimed := knotOf(t, f.Export, "oc-a000002")
	if claimed.CreatedBy != "operator" || claimed.Claim.By != "agent" || claimed.Comments[1].Author != "agent" {
		t.Errorf("the claimed knot shows %q, %q, %q", claimed.CreatedBy, claimed.Claim.By, claimed.Comments[1].Author)
	}
	if c := claimed.Comments[1]; c.Edited != "2026-10-25T10:30:00Z" || c.EditedBy != "agent" {
		t.Errorf("the edited comment shows edited %q by %q", c.Edited, c.EditedBy)
	}
	if c := claimed.Comments[0]; c.Edited != "" || c.EditedBy != "" {
		t.Errorf("a comment never edited shows edited %q by %q", c.Edited, c.EditedBy)
	}
	bug := knotOf(t, f.Export, "bd-a000004")
	if bug.CreatedBy != export.Unmapped || bug.Comments[0].Author != export.Unmapped {
		t.Errorf("an unmapped principal shows as %q and %q", bug.CreatedBy, bug.Comments[0].Author)
	}
	c := f.Export.Projects[0].Changes[0]
	if c.Owner != "agent" || c.Version.Uploader != "agent" {
		t.Errorf("the change shows %q and %q", c.Owner, c.Version.Uploader)
	}
}

// A change keeps its active version alone, with its request and outcome,
// and includes only changes the export carries.
func TestBuildKeepsTheActiveVersion(t *testing.T) {
	f := exporttest.New(t)
	changes := f.Export.Projects[0].Changes
	if len(changes) != 1 {
		t.Fatalf("changes %v", changes)
	}
	v := changes[0].Version
	if v.N != 2 || v.Request == nil || v.Outcome == nil || v.Outcome.Decision != "stale" {
		t.Errorf("the version is %+v", v)
	}
	if len(changes[0].Includes) != 0 {
		t.Errorf("includes %v names a draft", changes[0].Includes)
	}
}

// A field the valley adds is not exported until the projection names it.
func TestBuildReadsNamedFieldsAlone(t *testing.T) {
	f := exporttest.New(t)
	in := f.Inputs
	in.Plan = bytes.Replace(in.Plan, []byte(`"id": "oc-a000001",`), []byte(`"id": "oc-a000001", "secret_field": "do not export",`), 1)
	e, err := export.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	data, err := e.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("do not export")) {
		t.Error("an unnamed field reached the export")
	}
}

// What the projection refuses, so that a partial or inconsistent export
// is never published.
func TestBuildRefuses(t *testing.T) {
	f := exporttest.New(t)
	cases := []struct {
		name   string
		change func(in *export.Inputs)
		want   string
	}{
		{"a detail read at another graph tip", func(in *export.Inputs) {
			in.Details[0] = bytes.Replace(in.Details[0], []byte("3f6a0c1d"), []byte("3f6a0c1e"), 1)
		}, "different graph tips"},
		{"a knot without its detail", func(in *export.Inputs) { in.Details = in.Details[1:] }, "has no detail"},
		{"a detail twice", func(in *export.Inputs) { in.Details = append(in.Details, in.Details[0]) }, "twice"},
		{"a public project the plan could not read", func(in *export.Inputs) {
			in.Plan = bytes.Replace(in.Plan, []byte(`"project": "app",
      "error": null`), []byte(`"project": "app",
      "error": "unreadable"`), 1)
		}, "could not read exported project app"},
		{"a public project the pending listing could not read", func(in *export.Inputs) {
			in.Pending = bytes.Replace(in.Pending, []byte(`"url": "git@valley-host:app.git",
      "error": null`), []byte(`"url": "git@valley-host:app.git",
      "error": "unreadable"`), 1)
		}, "pending listing could not read exported project app"},
		{"a public project the plan does not list", func(in *export.Inputs) {
			in.Identities = map[string]string{"app": exporttest.Identities["app"], "other": "0123456789abcdef0123456789abcdef"}
		}, "not in the plan"},
		{"an identity that is not one", func(in *export.Inputs) {
			in.Identities = map[string]string{"app": "private"}
		}, "not 32 lowercase hex digits"},
		{"a public project with no source tip", func(in *export.Inputs) { in.SourceTips = nil }, "no source tip"},
		{"a display name with a control character", func(in *export.Inputs) {
			in.Principals = export.Principals{"app-agent": "agent\x1b"}
		}, "display mapping"},
		{"a plan of another schema", func(in *export.Inputs) {
			in.Plan = bytes.Replace(in.Plan, []byte("valley-plan/v1"), []byte("valley-plan/v2"), 1)
		}, "schema"},
		{"knot text with a control character", func(in *export.Inputs) {
			for i, d := range in.Details {
				in.Details[i] = bytes.Replace(d, []byte(`"body": "`), []byte(`"body": "\u0007`), 1)
			}
		}, "control character"},
		{"a public project in no group", func(in *export.Inputs) {
			in.Placements["app"] = export.Placement{}
		}, "exported project app belongs to no group"},
		{"group names that differ in case", func(in *export.Inputs) {
			in.Groups = append(in.Groups, export.Group{ID: "web", Name: "Tools"})
		}, `the name of group core and the name of group web are both "Tools", ignoring case`},
		{"a private project's alias that is a group's alias", func(in *export.Inputs) {
			in.Placements["site"] = export.Placement{Group: "core", Name: "site", Aliases: []string{"T"}}
		}, `alias 0 of group core and alias 0 of project site are both "T"`},
		{"a group's alias that is its name", func(in *export.Inputs) {
			in.Groups = []export.Group{{ID: "core", Name: "tools", Aliases: []string{"tools"}}}
		}, "the name of group core and alias 0 of group core"},
		{"a project's alias given twice", func(in *export.Inputs) {
			in.Placements["app"] = export.Placement{Group: "core", Name: "app", Aliases: []string{"app", "app"}}
		}, "alias 0 of project app and alias 1 of project app"},
		{"a project in a group that is not declared", func(in *export.Inputs) {
			in.Placements["site"] = export.Placement{Group: "web", Name: "site"}
		}, `site belongs to the group "web", which is not declared`},
		{"aliases without a group", func(in *export.Inputs) {
			in.Placements["site"] = export.Placement{Aliases: []string{"s"}}
		}, "site has a web name, former names or aliases and no group"},
		{"former names without a group", func(in *export.Inputs) {
			in.Placements["site"] = export.Placement{FormerNames: []string{"s"}}
		}, "site has a web name, former names or aliases and no group"},
		{"a group's name that is not a name", func(in *export.Inputs) {
			in.Groups = []export.Group{{ID: "core", Name: "-"}}
		}, `the name of group core is "-", which is not a name`},
		{"groups out of order", func(in *export.Inputs) {
			in.Groups = []export.Group{{ID: "web", Name: "docs"}, in.Groups[0]}
			in.Placements["site"] = export.Placement{Group: "web", Name: "site"}
		}, "the group core is listed out of order or twice"},
		{"a project in a group with no web name", func(in *export.Inputs) {
			in.Placements["app"] = export.Placement{Group: "core"}
		}, `the web name of project app is "", which is not a name`},
		{"two web names in one group that differ in case", func(in *export.Inputs) {
			in.Placements["site"] = export.Placement{Group: "core", Name: "App"}
		}, `the web name of project app and the web name of project site are both "App", ignoring case: web names and former names must be unique in group core`},
		{"a former name that is another project's web name", func(in *export.Inputs) {
			in.Placements["site"] = export.Placement{Group: "core", Name: "site", FormerNames: []string{"app"}}
		}, `the web name of project app and former name 0 of project site are both "app"`},
		{"a former name that is the project's own web name", func(in *export.Inputs) {
			in.Placements["app"] = export.Placement{Group: "core", Name: "app", FormerNames: []string{"APP"}}
		}, `the web name of project app and former name 0 of project app are both "APP"`},
		{"a project's identifier that is not a name", func(in *export.Inputs) {
			in.Placements["../app"] = export.Placement{}
		}, `a project's identifier is "../app", which is not a name`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := f.Inputs
			in.Details = slices.Clone(in.Details)
			in.Groups = slices.Clone(in.Groups)
			in.Placements = maps.Clone(in.Placements)
			c.change(&in)
			_, err := export.Build(in)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Build gave %v, want an error saying %q", err, c.want)
			}
		})
	}
	in := f.Inputs
	in.Details = slices.Clone(in.Details)
	in.Details[0] = bytes.Replace(in.Details[0], []byte("3f6a0c1d"), []byte("3f6a0c1e"), 1)
	if _, err := export.Build(in); !errors.Is(err, export.ErrInconsistent) {
		t.Errorf("a detail at another tip gave %v, want ErrInconsistent", err)
	}
}

// With no public project, the export is empty and valid, and names no
// group.
func TestBuildExportsNothingWhenNothingIsPublic(t *testing.T) {
	f := exporttest.New(t)
	in := f.Inputs
	in.Identities = nil
	e, err := export.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Projects) != 0 || len(e.Groups) != 0 {
		t.Errorf("exported %v and %v", e.Groups, e.Projects)
	}
}

// The export carries each public project's group and short aliases, and
// the groups public projects belong to. A group that only private
// projects belong to is not exported, nor are a private project's aliases.
func TestBuildExportsThePublicProjectsGroups(t *testing.T) {
	f := exporttest.New(t)
	in := f.Inputs
	in.Groups = append(slices.Clone(in.Groups), export.Group{ID: "web", Name: "docs", Aliases: []string{"d"}})
	in.Placements = map[string]export.Placement{
		"app":  {Group: "core", Name: "main", FormerNames: []string{"app"}, Aliases: []string{"app"}},
		"site": {Group: "web", Name: "pages", FormerNames: []string{"site-old"}, Aliases: []string{"site-short"}},
	}
	e, err := export.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := exporttest.Groups; !reflect.DeepEqual(e.Groups, want) {
		t.Errorf("the export's groups are %v, want %v", e.Groups, want)
	}
	if p := e.Projects[0]; p.Group != "core" || p.WebName != "main" || !slices.Equal(p.FormerNames, []string{"app"}) || !slices.Equal(p.Aliases, []string{"app"}) {
		t.Errorf("app is exported in group %q as %q, with former names %v and aliases %v", p.Group, p.WebName, p.FormerNames, p.Aliases)
	}
	data, err := e.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{`"web"`, `"docs"`, `"d"`, `"pages"`, `"site-old"`, `"site-short"`} {
		if bytes.Contains(data, []byte(private)) {
			t.Errorf("export.json holds %s, which only site is named by:\n%s", private, data)
		}
	}
}

// export.json is read strictly: a key that is not a field's name spelled
// exactly, in any case or Unicode case-fold variant, a key twice in one
// object, an array where the field holds none, or a project name that is
// not safe as a file name, fails it. Each case is refused for the reason
// it names. Brackets nested a million deep are refused at the first one
// the field cannot hold.
func TestDecodeIsStrict(t *testing.T) {
	f := exporttest.New(t)
	if _, err := export.Decode(f.ExportJSON); err != nil {
		t.Fatal(err)
	}
	const deep = 1 << 20
	brackets := strings.Repeat("[", deep) + "null" + strings.Repeat("]", deep)
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"an unknown field", bytes.Replace(f.ExportJSON, []byte(`"collected"`), []byte(`"extra":1,"collected"`), 1),
			`holds the key "extra", which is none of its fields`},
		{"an unsafe name", bytes.Replace(f.ExportJSON, []byte(`"project":"app"`), []byte(`"project":"../app"`), 1),
			`names a project "../app", which is not a project name`},
		{"a second document", append(append([]byte{}, f.ExportJSON...), f.ExportJSON...),
			"holds more than its one document"},
		{"another schema", bytes.Replace(f.ExportJSON, []byte(export.Schema), []byte("valley-web-export/v0"), 1),
			`has schema "valley-web-export/v0"`},
		{"a field spelled in another case beside it", bytes.Replace(f.ExportJSON, []byte(`"aliases":["app"]`),
			[]byte(`"aliases":["app"],"Aliases":["other"]`), 1),
			`projects[0] holds the key "Aliases", which is its field "aliases" spelled another way`},
		{"a field spelled with a Unicode case-fold variant", bytes.Replace(f.ExportJSON, []byte(`"aliases":["app"]`),
			[]byte(`"alia`+"ſ"+`es":["other"]`), 1),
			`projects[0] holds the key "alia` + "ſ" + `es", which is its field "aliases" spelled another way`},
		{"a field given twice", bytes.Replace(f.ExportJSON, []byte(`"aliases":["app"]`),
			[]byte(`"aliases":["app"],"aliases":["other"]`), 1),
			`projects[0] holds the key "aliases" twice`},
		{"a field given twice, once escaped", bytes.Replace(f.ExportJSON, []byte(`"aliases":["app"]`),
			[]byte(`"aliases":["app"],"\u0061liases":["other"]`), 1),
			`projects[0] holds the key "aliases" twice`},
		{"a project alias that is a group's alias", bytes.Replace(f.ExportJSON, []byte(`"aliases":["app"]`),
			[]byte(`"aliases":["T"]`), 1),
			`alias 0 of group core and alias 0 of project app are both "T"`},
		{"a group name that is reserved", bytes.Replace(f.ExportJSON, []byte(`"name":"tools"`), []byte(`"name":"-"`), 1),
			`the name of group core is "-", which is not a name`},
		{"a former name that is the project's web name", bytes.Replace(f.ExportJSON, []byte(`"former_names":["old-app"]`),
			[]byte(`"former_names":["App"]`), 1),
			`the web name of project app and former name 0 of project app are both "App"`},
		{"brackets in place of the document", []byte(brackets),
			`export.json: the document is an array, and export.Export holds no array`},
		{"brackets in place of an alias", bytes.Replace(f.ExportJSON, []byte(`"aliases":["app"]`),
			[]byte(`"aliases":[`+brackets+`]`), 1),
			`export.json: projects[0].aliases[0] is an array, and string holds no array`},
	} {
		if _, err := export.Decode(c.data); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Decode of %s gave %v, want an error saying %q", c.name, err, c.want)
		}
	}
}

// An export's groups are each some project's, in identifier order, and
// each group a project names is listed. Every case keeps every listed
// group some project's but the one that tests it, so each is refused for
// the reason it names alone, and a check that went missing would let its
// case through.
func TestDecodeHoldsGroupsToTheirProjects(t *testing.T) {
	f := exporttest.New(t)
	// two is the fixture's export with site public too, as a copy of
	// app: groups lists core and web, app is in core, and site in web.
	two := func(change func(e *export.Export)) []byte {
		t.Helper()
		e, err := export.Decode(f.ExportJSON)
		if err != nil {
			t.Fatal(err)
		}
		site := e.Projects[0]
		site.Name, site.Group, site.WebName, site.FormerNames, site.Aliases = "site", "web", "app", nil, nil
		e.Projects = append(e.Projects, site)
		e.Groups = append(e.Groups, export.Group{ID: "web", Name: "docs"})
		change(e)
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if e, err := export.Decode(two(func(*export.Export) {})); err != nil {
		t.Fatalf("an export with app and site each named app in its own group is refused: %v", err)
	} else if e.Projects[0].WebName != "app" || e.Projects[1].WebName != "app" {
		t.Fatalf("the two projects decode as %q and %q", e.Projects[0].WebName, e.Projects[1].WebName)
	}
	for _, c := range []struct {
		name   string
		change func(e *export.Export)
		want   string
	}{
		{"groups out of order", func(e *export.Export) {
			e.Groups[0], e.Groups[1] = e.Groups[1], e.Groups[0]
		}, "export.json: the group core is listed out of order or twice"},
		{"a group listed twice", func(e *export.Export) {
			e.Groups[1] = e.Groups[0]
			e.Projects[1].Group = "core"
			e.Projects[1].WebName = "site"
		}, "export.json: the group core is listed out of order or twice"},
		{"a group no project belongs to", func(e *export.Export) {
			e.Projects[1].Group = "core"
			e.Projects[1].WebName = "site"
		}, "export.json lists group web, which none of its projects belongs to"},
		{"a project in a group it does not list", func(e *export.Export) {
			e.Groups = e.Groups[:1]
			e.Projects[1].Group = "web"
		}, `export.json: the project site belongs to the group "web", which is not declared`},
		{"two projects of one web name in one group", func(e *export.Export) {
			e.Groups = e.Groups[:1]
			e.Projects[1].Group = "core"
		}, `export.json: the web name of project app and the web name of project site are both "app"`},
	} {
		if _, err := export.Decode(two(c.change)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Decode of %s gave %v, want an error saying %q", c.name, err, c.want)
		}
	}
}

// A v1 export from before groups decodes: it has no groups, and its
// projects no group and no aliases.
func TestDecodeTakesAnExportWithoutGroups(t *testing.T) {
	f := exporttest.New(t)
	data := bytes.Replace(f.ExportJSON, []byte(`"groups":[{"group":"core","name":"tools","aliases":["t"]}],`), nil, 1)
	data = bytes.Replace(data, []byte(`"group":"core","name":"app","former_names":["old-app"],"aliases":["app"],`), nil, 1)
	if bytes.Contains(data, []byte(`"group"`)) {
		t.Fatalf("the fixture's export no longer reads as the test expects:\n%s", data)
	}
	e, err := export.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if p := e.Projects[0]; len(e.Groups) != 0 || p.Group != "" || p.WebName != "" || p.FormerNames != nil || p.Aliases != nil {
		t.Errorf("an export without groups decodes with %v, and app in %q as %q with %v and %v", e.Groups, p.Group, p.WebName, p.FormerNames, p.Aliases)
	}
}

// Nothing but white space may follow export.json's document: not a
// closing bracket or brace, which ends a document without starting
// another, and not a malformed suffix. A receiver refuses such an
// export.json as bad-export even when the manifest declares its bytes.
func TestDecodeRefusesTrailingInput(t *testing.T) {
	f := exporttest.New(t)
	for _, suffix := range []string{"]", "}", "]garbage", "}}", "],", "garbage", ",", "{", `"`, "nul", "\x00", "{}", "[]", "1"} {
		data := append(append([]byte{}, f.ExportJSON...), suffix...)
		if _, err := export.Decode(data); err == nil {
			t.Errorf("Decode took export.json followed by %q", suffix)
		}
		m := *f.Manifest
		m.File = export.BodyOf(export.ExportFile, data)
		if _, err := m.CheckExport(data); export.CodeOf(err) != export.CodeBadExport {
			t.Errorf("CheckExport gave %v for export.json followed by %q, want bad-export", err, suffix)
		}
	}
	data := append(append([]byte{}, f.ExportJSON...), " \t\r\n\n"...)
	if _, err := export.Decode(data); err != nil {
		t.Errorf("Decode refused export.json followed by white space: %v", err)
	}
}

// The content digest ignores display-time fields and nothing else.
func TestDigest(t *testing.T) {
	f := exporttest.New(t)
	base, err := f.Export.Digest()
	if err != nil {
		t.Fatal(err)
	}
	reread := func() *export.Export {
		e, err := export.Decode(f.ExportJSON)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	same := map[string]func(e *export.Export){
		"collected": func(e *export.Export) { e.Collected = "2026-10-26T00:00:00Z" },
		"a claim's age": func(e *export.Export) {
			for i := range e.Projects[0].Knots {
				if c := e.Projects[0].Knots[i].Claim; c != nil {
					c.AgeSeconds, c.Stale = 999999, true
				}
			}
		},
	}
	for name, change := range same {
		e := reread()
		change(e)
		if got, _ := e.Digest(); got != base {
			t.Errorf("changing %s changed the digest", name)
		}
	}
	differs := map[string]func(e *export.Export){
		"a title":      func(e *export.Export) { e.Projects[0].Knots[0].Title += "!" },
		"a source tip": func(e *export.Export) { e.Projects[0].SourceTip = strings.Repeat("a", 40) },
		"a claim's at": func(e *export.Export) { e.Projects[0].Knots[1].Claim.At = "2026-10-25T11:00:00Z" },
		"a comment":    func(e *export.Export) { e.Projects[0].Knots[1].Comments[0].Body += "." },
		"waiting on a hidden knot": func(e *export.Export) {
			e.Projects[0].Knots[0].WaitingOnHidden = !e.Projects[0].Knots[0].WaitingOnHidden
		},
	}
	for name, change := range differs {
		e := reread()
		change(e)
		if got, _ := e.Digest(); got == base {
			t.Errorf("changing %s left the digest as it was", name)
		}
	}
	// Digest copies what it clears: the export it was given is unchanged.
	if f.Export.Collected == "" || knotOf(t, f.Export, "oc-a000002").Claim.AgeSeconds == 0 {
		t.Error("Digest changed the export it read")
	}
}

// Incremental decoding preserves the export's values and rejects malformed
// fields, including nested fields and integer overflow.
func TestDecodeContextValues(t *testing.T) {
	f := exporttest.New(t)
	for _, data := range [][]byte{
		f.ExportJSON,
		[]byte(`{"schema":"valley-web-export/v2","projects":null}`),
		[]byte(`{"schema":"valley-web-export/v2","projects":[]}`),
	} {
		var want export.Export
		if err := json.Unmarshal(data, &want); err != nil {
			t.Fatal(err)
		}
		got, err := export.DecodeContext(context.Background(), bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, &want) {
			t.Fatal("incremental decoding changed export values")
		}
	}
	for _, field := range []string{
		`"knots":{}`, `"roots":[1]`, `"knots":[{"id":"oc-a000001","active":1}]`,
		`"knots":[{"id":"oc-a000001","comment_count":9223372036854775808}]`,
		`"knots":[{"id":"oc-a000001","comment_count":1.5}]`, `"knots":[{"id":"oc-a000001","unknown":true}]`,
	} {
		data := []byte(`{"schema":"valley-web-export/v2","projects":[{"project":"app","identity":"9da6809f038df92138c131cd16bed76d","source_tip":"` + strings.Repeat("a", 40) + `",` + field + `}]}`)
		if _, err := export.DecodeContext(context.Background(), bytes.NewReader(data)); err == nil {
			t.Errorf("accepted %s", field)
		}
	}
}

// A token that spans many reads observes cancellation before consuming its
// remaining bytes.
func TestDecodeContextDuringToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &cancelDecodeReader{Reader: strings.NewReader(`{"schema":"` + strings.Repeat("x", 1<<20) + `"}`), cancel: cancel}
	if _, err := export.DecodeContext(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if r.n > 8192 {
		t.Fatalf("read %d bytes after cancellation", r.n)
	}
}

type cancelDecodeReader struct {
	io.Reader
	cancel context.CancelFunc
	n      int
}

func (r *cancelDecodeReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += n
	if r.n >= 4096 {
		r.cancel()
	}
	return n, err
}

// The final input read arms cancellation. The string scanner must observe
// it while decoding bytes already in its buffer, without another source read.
func TestDecodeContextFinalStringChunk(t *testing.T) {
	for _, key := range []bool{false, true} {
		t.Run(fmt.Sprint(key), func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &finalStringContext{Context: base, cancel: cancel}
			value := strings.Repeat(`\u0061`, 1<<17)
			data := `{"schema":"valley-web-export/v2","collected":"` + value + `"}`
			if key {
				data = `{"` + value + `":null}`
			}
			r := &finalStringReader{Reader: strings.NewReader(data), ctx: ctx}
			started := time.Now()
			got, err := export.DecodeContext(ctx, r)
			if got != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("DecodeContext = %v, %v", got, err)
			}
			if !ctx.inString {
				t.Fatal("cancellation was not observed inside string decoding")
			}
			if r.extraRead {
				t.Fatal("decoder requested more input after the final chunk")
			}
			if time.Since(started) > 2*time.Second {
				t.Fatal("string decoding did not stop promptly")
			}
		})
	}
}

type finalStringContext struct {
	context.Context
	cancel   context.CancelFunc
	armed    bool
	checks   int
	inString bool
}

func (c *finalStringContext) Err() error {
	if c.armed {
		c.checks++
		if c.checks == 3 {
			var pcs [16]uintptr
			n := runtime.Callers(2, pcs[:])
			frames := runtime.CallersFrames(pcs[:n])
			for {
				f, more := frames.Next()
				if strings.HasSuffix(f.Function, ".(*stringInput).readString") {
					c.inString = true
				}
				if !more {
					break
				}
			}
			c.cancel()
		}
	}
	return c.Context.Err()
}

type finalStringReader struct {
	*strings.Reader
	ctx       *finalStringContext
	extraRead bool
}

func (r *finalStringReader) Read(p []byte) (int, error) {
	if r.ctx.armed {
		r.extraRead = true
	}
	n, err := r.Reader.Read(p)
	if r.Len() == 0 {
		r.ctx.armed = true
	}
	return n, err
}

// String decoding matches encoding/json for escapes, invalid UTF-8 and
// surrogate pairs, including sequences split across input chunks.
func TestDecodeStringValues(t *testing.T) {
	values := []string{
		`"plain"`, `"\"\\\/\b\f\n\r\t"`, `"\uD834\uDD1E"`,
		`"\uD834x"`, `"\uDD1E"`, `"\uD834\u0061"`, `"\uD834\uD834\uDD1E"`,
		"\"\xff\"", `"é界𝄞"`,
	}
	prefixes := []string{""}
	for offset := 0; offset < 12; offset++ {
		prefixes = append(prefixes, strings.Repeat("a", 4096-len(`{"schema":"valley-web-export/v2","collected":"`)-offset))
	}
	for _, prefix := range prefixes {
		for _, value := range values {
			data := []byte(`{"schema":"valley-web-export/v2","collected":"` + prefix + value[1:] + `}`)
			var want export.Export
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			got, err := export.DecodeContext(context.Background(), bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, &want) {
				t.Errorf("string %q differs from encoding/json", value)
			}
		}
	}
	for _, value := range []string{`"\x"`, `"\uZZZZ"`, `"\u123"`, `"unterminated`, "\"\n\"", `"\uD834\uZZZZ"`} {
		data := []byte(`{"schema":"valley-web-export/v2","collected":` + value + `}`)
		if _, err := export.Decode(data); err == nil {
			t.Errorf("accepted invalid string %q", value)
		}
	}
}

// More must not consume and hide a string error before a closing delimiter.
func TestDecodeRetainsStringError(t *testing.T) {
	for _, data := range []string{
		`{"schema":"valley-web-export/v2","projects":["\x]}`,
		`{"schema":"valley-web-export/v2","\x}`,
	} {
		if _, err := export.Decode([]byte(data)); err == nil {
			t.Errorf("accepted malformed document %q", data)
		}
	}
}
