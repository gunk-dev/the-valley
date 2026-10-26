package derive

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"the-valley/composed"
)

// TestReadReadsTheWholePolicy holds composed.Read to what a policy says:
// each check's runner, attribute or command, validity and cost; each
// class's paths, requirements, pins and carry mode, with what a project
// turned off left out; and the digest Compose gives the same layers.
func TestReadReadsTheWholePolicy(t *testing.T) {
	r := newRig(t)
	l := newLayers(t, r.glob(t, "examples/policy/instance/*.cue"), r.glob(t, "examples/policy/project/*.cue"), map[string]string{
		"live.cue": `package verification
project: checks: "smoke-live": {runner: "command", command: "curl -fsS https://example.org/health", validity: "15m", cost: "slow"}
project: classes: live: {paths: "deploy/**": true, requires: "smoke-live": true, carry: exact: true}
`})
	m, err := r.read(l)
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]composed.Check{
		"prose-format":   {Runner: "nix", Attribute: "prose-format", Cost: "quick"},
		"link-check":     {Runner: "nix", Attribute: "link-check", Cost: "quick"},
		"knowledge-lint": {Runner: "nix", Attribute: "knowledge-lint", Cost: "quick"},
		"shellcheck":     {Runner: "nix", Attribute: "shellcheck", Cost: "quick"},
		"install-vm":     {Runner: "nix", Attribute: "install-vm", Cost: "slow"},
		"smoke-live":     {Runner: "command", Command: "curl -fsS https://example.org/health", Validity: "15m", Cost: "slow"},
	}
	if !reflect.DeepEqual(m.Checks, checks) {
		t.Errorf("checks = %+v", m.Checks)
	}
	classes := map[string]composed.Class{
		"prose": {Paths: []string{"**/*.md"}, Requires: map[string]composed.Pin{"prose-format": composed.Mandatory, "link-check": composed.Mandatory}, Carry: "same-paths"},
		// The template offers the lint as a default and the project turns
		// it off, so the class requires nothing.
		"knowledge": {Paths: []string{".the-valley/**"}, Requires: map[string]composed.Pin{}, Carry: "same-paths"},
		"scripts":   {Paths: []string{"bin/**"}, Requires: map[string]composed.Pin{"shellcheck": composed.Mandatory, "install-vm": composed.Mandatory}, Carry: "same-paths"},
		"live":      {Paths: []string{"deploy/**"}, Requires: map[string]composed.Pin{"smoke-live": composed.Mandatory}, Carry: "exact"},
	}
	if !reflect.DeepEqual(m.Classes, classes) {
		t.Errorf("classes = %+v", m.Classes)
	}
	if want := map[string]composed.Pin{"prose-format": composed.Mandatory}; !reflect.DeepEqual(m.Unclassified, want) {
		t.Errorf("unclassified = %+v", m.Unclassified)
	}
	pol, err := composed.Compose(t.Context(), r.cue, r.schema, l.instance, l.project)
	if err != nil {
		t.Fatal(err)
	}
	if m.Digest != pol.Digest || len(m.Digest) != 64 {
		t.Errorf("Read's digest %q is not Compose's %q", m.Digest, pol.Digest)
	}

	// The template's default, left alone, is a default.
	tmpl := newLayers(t, r.glob(t, "examples/policy/deriver/[ft]*.cue"), []string{r.path("examples/policy/deriver/project.cue")}, nil)
	if m, err = r.read(tmpl); err != nil {
		t.Fatal(err)
	}
	if got := m.Classes["docs"].Requires; got["c-tmpl"] != composed.Default || got["c-docs"] != composed.Mandatory {
		t.Errorf("docs requires %v", got)
	}
}

// TestReadRefuses holds composed.Read to refusing what it cannot read
// whole: no schema, a layer with no documents, and a layer that does not
// compose.
func TestReadRefuses(t *testing.T) {
	r := newRig(t)
	good := newLayers(t, r.glob(t, "examples/policy/smoke/instance/*.cue"), nil, nil)
	empty := layers{instance: t.TempDir(), project: good.project}
	broken := newLayers(t, r.glob(t, "examples/policy/smoke/instance/*.cue"), nil, map[string]string{"broken.cue": "package verification\nproject: classes: x: requires: nothing: true\n"})
	for _, c := range []struct {
		name, schema string
		l            layers
		says         string
	}{
		{"no schema", r.path("schema/absent.cue"), good, "no policy schema at"},
		{"an empty floor", r.schema, empty, "no policy layer at"},
		{"a layer that does not compose", r.schema, broken, "do not compose"},
	} {
		if _, err := composed.Read(t.Context(), r.cue, c.schema, c.l.instance, c.l.project); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, not %q", c.name, err, c.says)
		}
	}
}

// TestTheHardwareRequirementIsRead holds Compose and Read to
// approval.hardware as cue composes it, over the fixtures under
// examples/policy/approval/. A floor's `true` and a project's `true` both
// require a hardware key. A template default the project turned off and a
// policy that never mentions approval do not. A project's `false` against
// the floor's `true` is refused, rather than read as either.
func TestTheHardwareRequirementIsRead(t *testing.T) {
	r := newRig(t)
	fixture := func(name string) layers {
		return newLayers(t, []string{r.path("examples/policy/approval/" + name + ".cue")}, nil, nil)
	}
	for _, c := range []struct {
		fixture string
		want    bool
	}{
		{"floor-requires-hardware", true},
		{"project-requires-hardware", true},
		{"template-default-declined", false},
		{"no-requirement", false},
	} {
		l := fixture(c.fixture)
		pol, err := composed.Compose(t.Context(), r.cue, r.schema, l.instance, l.project)
		if err != nil {
			t.Fatalf("%s: %v", c.fixture, err)
		}
		m, err := r.read(l)
		if err != nil {
			t.Fatalf("%s: %v", c.fixture, err)
		}
		if pol.HardwareApproval != c.want || m.HardwareApproval != c.want {
			t.Errorf("%s: Compose reads HardwareApproval %v and Read %v, not %v", c.fixture, pol.HardwareApproval, m.HardwareApproval, c.want)
		}
	}

	l := fixture("rejected/project-drops-floor-hardware")
	if pol, err := composed.Compose(t.Context(), r.cue, r.schema, l.instance, l.project); err == nil || !strings.Contains(err.Error(), "policy.approval.hardware") {
		t.Errorf("a project dropping the floor's hardware requirement composed to %+v, %v", pol, err)
	}
	if m, err := r.read(l); err == nil || !strings.Contains(err.Error(), "policy.approval.hardware") {
		t.Errorf("a project dropping the floor's hardware requirement was read as %+v, %v", m, err)
	}
}

// TestAReopenedDefinitionIsRefused holds Compose and Read to refusing an
// export with a field the schema does not give. Every document of a policy
// is in one package, so any of them may reopen a schema definition and add
// a field to it. In the cross-family review's case, the floor writes
// `hardware: true`, and a project adds `Hardware` to #Policy's approval and
// writes it false. cue exports both keys, and a reader matching names
// without regard to case would read whichever comes last.
//
// cue unifies the documents in any order, and cannot tell which layer a
// field came from. The order changes only the order of the keys in the
// export. So the three documents are composed in every order: the first
// as the floor layer's document, the other two as the project layer's.
func TestAReopenedDefinitionIsRefused(t *testing.T) {
	r := newRig(t)
	const floor = `package verification
floor: checks: "c-build": {runner: "nix", attribute: "c-build"}
floor: classes: code: {paths: "src/**": true, requires: "c-build": true}
floor: approval: hardware: true
`
	docs := []string{floor, "package verification\n#Policy: approval: {Hardware?: bool}\n",
		"package verification\nproject: approval: {hardware: true, Hardware: false}\n"}
	layered := func(first string, rest ...string) layers {
		extra := map[string]string{}
		for i, doc := range rest {
			extra[string(rune('a'+i))+".cue"] = doc
		}
		l := newLayers(t, nil, nil, extra)
		if err := os.WriteFile(filepath.Join(l.instance, "a.cue"), []byte(first), 0o644); err != nil {
			t.Fatal(err)
		}
		return l
	}
	refused := func(name string, l layers, says string) {
		t.Helper()
		if pol, err := composed.Compose(t.Context(), r.cue, r.schema, l.instance, l.project); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: Compose read %+v, %v, not %q", name, pol, err, says)
		}
		if m, err := r.read(l); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: Read read %+v, %v, not %q", name, m, err, says)
		}
	}
	for _, o := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		refused("documents in order "+fmt.Sprint(o), layered(docs[o[0]], docs[o[1]], docs[o[2]]),
			`policy.approval has a field "Hardware" the schema does not give it`)
	}

	// The same trick at the other objects the reader reads into a struct.
	for _, c := range []struct{ name, doc, says string }{
		{"an Approval beside approval", "#Policy: Approval?: {hardware?: bool}\nproject: Approval: hardware: false\n", `policy has a field "Approval"`},
		{"a check's Runner", "#Check: Runner?: string\nproject: checks: \"c-build\": {runner: \"nix\", attribute: \"c-build\", Runner: \"command\"}\n", `policy.checks.c-build has a field "Runner"`},
		{"a class's Requires", "#Class: Requires?: [string]: bool\nproject: classes: code: Requires: \"c-build\": false\n", `policy.classes.code has a field "Requires"`},
	} {
		refused(c.name, layered(floor, "package verification\n"+c.doc), c.says)
	}

	// cue cannot export a value of the wrong kind under the schema, or a
	// key twice in one object, so a stand-in for cue prints each.
	// encoding/json would read the cap written as a string as a number, and
	// keep the second of two equal keys.
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	l := layered(floor)
	for _, c := range []struct{ name, export, says string }{
		{"a requirement written as a string", `"approval":{"hardware":"false"}`, "policy.approval.hardware is not a bool"},
		{"a cap written as a string", `"restack":{"maxAutomaticVersions":"64"}`, "policy.restack.maxAutomaticVersions is not a number"},
		{"approval written twice", `"approval":{"hardware":true},"approval":{"hardware":false}`, `policy holds the key "approval" twice`},
		{"a requirement written twice", `"approval":{"hardware":true,"hardware":false}`, `policy.approval holds the key "hardware" twice`},
	} {
		cue := filepath.Join(t.TempDir(), "cue")
		script := "#!" + bash + "\nprintf '%s' '{\"checks\":{},\"classes\":{},\"unclassified\":{}," + c.export + "}'\n"
		if err := os.WriteFile(cue, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if pol, err := composed.Compose(t.Context(), cue, r.schema, l.instance, l.project); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: Compose read %+v, %v, not %q", c.name, pol, err, c.says)
		}
	}
}

// TestDeriveOverAModel holds Derive's own rules, over a policy written
// here rather than composed: the strictest carry mode among the matched
// classes, a pin that is mandatory where any requirement of it is, a
// class named unclassified kept apart from the unclassified set, and a
// requirement of a check the policy does not define refused.
func TestDeriveOverAModel(t *testing.T) {
	nix := composed.Check{Runner: "nix", Cost: "quick"}
	m := &composed.Model{
		Policy: composed.Policy{Checks: map[string]composed.Check{"a": nix, "b": nix, "u": nix}},
		Classes: map[string]composed.Class{
			"loose":        {Paths: []string{"loose/**"}, Requires: map[string]composed.Pin{"a": composed.Default}, Carry: "same-paths"},
			"strict":       {Paths: []string{"strict/*"}, Requires: map[string]composed.Pin{"a": composed.Mandatory, "b": composed.Default}, Carry: "same-class"},
			"unclassified": {Paths: []string{"odd"}, Requires: map[string]composed.Pin{"b": composed.Default}, Carry: "exact"},
		},
		Unclassified: map[string]composed.Pin{"u": composed.Mandatory},
	}
	res, err := Derive(m, []string{"strict/x", "loose/y/z", "strict/x", "elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	want := &Result{
		Paths:        []string{"elsewhere", "loose/y/z", "strict/x"},
		Classes:      []string{"loose", "strict"},
		Unclassified: []string{"elsewhere"},
		Carry:        "same-class",
		Required: []Required{
			{Name: "a", Classes: []string{"loose", "strict"}, Pin: composed.Mandatory, Check: nix},
			{Name: "b", Classes: []string{"strict"}, Pin: composed.Default, Check: nix},
			{Name: "u", Classes: []string{}, Unclassified: true, Pin: composed.Mandatory, Check: nix},
		},
	}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("Derive = %+v\nwant     %+v", res, want)
	}
	if res, err = Derive(m, []string{"odd"}); err != nil || res.Carry != "exact" || len(res.Required) != 1 || res.Required[0].Name != "b" || res.Required[0].Unclassified {
		t.Errorf("the class named unclassified derives %+v, %v", res, err)
	}
	if res, err = Derive(m, nil); err != nil || res.Carry != "same-paths" || len(res.Required) != 0 {
		t.Errorf("no paths derive %+v, %v", res, err)
	}
	m.Unclassified["ghost"] = composed.Mandatory
	if _, err := Derive(m, []string{"elsewhere"}); err == nil || !strings.Contains(err.Error(), "defines no check") {
		t.Errorf("a requirement of an undefined check derived: %v", err)
	}
}

// TestChangedRefusesWhatGitDoesNotWrite holds Changed to a record cut
// anywhere, and to the records git never writes.
func TestChangedRefusesWhatGitDoesNotWrite(t *testing.T) {
	got, err := Changed([]byte("R087\x00old\x00new\x00D\x00gone\nwith a newline\x00M\x00caf\xe9\x00"))
	if want := []string{"old", "new", "gone\nwith a newline", "caf\xe9"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Changed = %q, %v", got, err)
	}
	for _, out := range []string{"M", "M\x00", "M\x00a", "R100\x00old\x00", "M\x00a\x00R1", "Z\x00a\x00", "M\x00\x00", "\x00a\x00", "R1x\x00a\x00b\x00"} {
		if got, err := Changed([]byte(out)); err == nil {
			t.Errorf("Changed(%q) = %q", out, got)
		}
	}
}

// TestMatch is the glob language by example.
func TestMatch(t *testing.T) {
	for _, c := range []struct {
		glob, path string
		want       bool
	}{
		{"src/*.rs", "src/one.rs", true},
		{"src/*.rs", "src/deep/two.rs", false},
		{"src/**/*.rs", "src/one.rs", true},
		{"src/**/*.rs", "src/deep/two.rs", true},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "ab", false},
		{"docs/**", "docs/x/y", true},
		{"docs/**", "docs", false},
		{"**/*.md", "README.md", true},
		{"**/*.md", "a/b/c.md", true},
		{"**", "", true},
		{"a**b", "a/b", false},
		{"a**b", "axxb", true},
		{"?", "é", false}, // two bytes
		{"??", "é", true},
		{"?", "/", false},
		{"*", "a\nb", true},
		{"c.(x)", "c.(x)", true},
		{"c.(x)", "cx(x)", false},
		{"docs/", "docs", true}, // one trailing slash is dropped
		{"docs//", "docs/", true},
	} {
		if got := Match(c.glob, c.path); got != c.want {
			t.Errorf("Match(%q, %q) = %v", c.glob, c.path, got)
		}
	}
}
