package export_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"the-valley/webui/export"
	"the-valley/webui/export/exporttest"
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
// shown as free to start.
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

// A reference to another exported project's knot keeps what the plan
// says of it.
func TestBuildKeepsEdgesBetweenExportedProjects(t *testing.T) {
	f := exporttest.New(t)
	in := f.Inputs
	in.Visibility = map[string]export.Visibility{"app": export.Public, "site": export.Public}
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
// not map shows as the generic role.
func TestBuildMapsPrincipals(t *testing.T) {
	f := exporttest.New(t)
	claimed := knotOf(t, f.Export, "oc-a000002")
	if claimed.CreatedBy != "operator" || claimed.Claim.By != "agent" || claimed.Comments[1].Author != "agent" {
		t.Errorf("the claimed knot shows %q, %q, %q", claimed.CreatedBy, claimed.Claim.By, claimed.Comments[1].Author)
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
		}, "could not read public project app"},
		{"a public project the pending listing could not read", func(in *export.Inputs) {
			in.Pending = bytes.Replace(in.Pending, []byte(`"url": "git@valley-host:app.git",
      "error": null`), []byte(`"url": "git@valley-host:app.git",
      "error": "unreadable"`), 1)
		}, "pending listing could not read public project app"},
		{"a public project the plan does not list", func(in *export.Inputs) {
			in.Visibility = map[string]export.Visibility{"app": export.Public, "other": export.Public}
		}, "not in the plan"},
		{"a visibility v1 does not export", func(in *export.Inputs) {
			in.Visibility = map[string]export.Visibility{"app": "private"}
		}, "public projects alone"},
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := f.Inputs
			in.Details = slices.Clone(in.Details)
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

// With no public project, the export is empty and valid.
func TestBuildExportsNothingWhenNothingIsPublic(t *testing.T) {
	f := exporttest.New(t)
	in := f.Inputs
	in.Visibility = nil
	e, err := export.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Projects) != 0 {
		t.Errorf("exported %v", e.Projects)
	}
}

// export.json is read strictly: a field the schema does not name, or a
// project name that is not safe as a file name, fails it.
func TestDecodeIsStrict(t *testing.T) {
	f := exporttest.New(t)
	if _, err := export.Decode(f.ExportJSON); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"an unknown field":  bytes.Replace(f.ExportJSON, []byte(`"collected"`), []byte(`"extra":1,"collected"`), 1),
		"an unsafe name":    bytes.Replace(f.ExportJSON, []byte(`"project":"app"`), []byte(`"project":"../app"`), 1),
		"a second document": append(append([]byte{}, f.ExportJSON...), f.ExportJSON...),
		"another schema":    bytes.Replace(f.ExportJSON, []byte(export.Schema), []byte("valley-web-export/v0"), 1),
	} {
		if _, err := export.Decode(data); err == nil {
			t.Errorf("Decode took %s", name)
		}
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
