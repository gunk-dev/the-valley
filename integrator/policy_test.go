package main

// The policy bridge, where this program is coupled to two others: the
// deriver's output, and the policy cue composes, with the window a check
// writes down and the digest of the whole. Both are pinned here rather than
// assumed.

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDeriverOutputIsRead(t *testing.T) {
	out := `abc1234..def5678: 3 changed path(s)
policy: instance + policy
classes matched: code, prose
carry mode: same-class

required checks:
  code-build      mandatory  code
  prose-format    default    prose, knowledge

unclassified: 1 path(s) matched no class
  odd.txt
`
	d, err := parseDerived(out)
	if err != nil {
		t.Fatal(err)
	}
	if !d.classes["code"] || !d.classes["prose"] || len(d.classes) != 2 {
		t.Fatalf("classes = %v", d.classes)
	}
	if got := strings.Join(d.requires["prose-format"], "+"); got != "prose+knowledge" {
		t.Fatalf("prose-format required by %q", got)
	}
	if got := strings.Join(d.requires["code-build"], "+"); got != "code" {
		t.Fatalf("code-build required by %q", got)
	}
	if len(d.requires) != 2 {
		t.Fatalf("required = %v", d.requires)
	}
	if d.carry != "same-class" {
		t.Fatalf("carry mode = %q", d.carry)
	}
}

// The deriver ends its report with what each required check costs. Those
// lines group the required checks and require nothing more, so a report
// carrying them reads as exactly the report without them — with an
// unclassified block between the table and the summary, and with the
// summary straight after the table.
func TestTheCostSummaryChangesNothingRead(t *testing.T) {
	table := `abc1234..def5678: 2 changed path(s)
policy: instance + policy
classes matched: code, prose
carry mode: same-paths

required checks:
  code-build      mandatory  code
  vm-test         default    code
  prose-format    default    prose, knowledge
`
	unclassified := `
unclassified: 1 path(s) matched no class
  odd.txt
`
	summary := `
quick: code-build, prose-format
slow: vm-test
`
	for _, before := range []string{table, table + unclassified} {
		want, err := parseDerived(before)
		if err != nil {
			t.Fatal(err)
		}
		if len(want.requires) != 3 || len(want.classes) != 2 {
			t.Fatalf("the report without a summary reads as %+v", want)
		}
		got, err := parseDerived(before + summary)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("with the cost summary, the report reads as %+v, not %+v", got, want)
		}
	}
}

// A deriver that fails partway and still exits zero prints something that
// is not a whole report. Read as one, each of these requires nothing, so
// each is refused.
func TestAPartialReportIsRefused(t *testing.T) {
	for _, c := range []struct{ name, out string }{
		{"a required-checks heading with no checks under it", `abc1234..def5678: 2 changed path(s)
policy: instance + policy
classes matched: code
carry mode: same-paths

required checks:

quick: none
slow: none
`},
		{"a required-checks heading that ends the output", `abc1234..def5678: 2 changed path(s)
policy: instance + policy
classes matched: code
carry mode: same-paths

required checks:
`},
		{"no required-checks section", `abc1234..def5678: 2 changed path(s)
policy: instance + policy
classes matched: code
carry mode: same-paths
`},
		{"no header", `policy: instance + policy
classes matched: code
carry mode: same-paths

required checks:
  code-build  mandatory  code
`},
		{"nothing at all", ``},
		{"changed paths and no class", `abc1234..def5678: 2 changed path(s)
policy: instance + policy
classes matched: none
carry mode: same-paths

required checks: none
`},
		{"no changed paths and a class", `abc1234..def5678: 0 changed path(s)
policy: instance + policy
classes matched: code
carry mode: same-paths

required checks:
  code-build  mandatory  code
`},
		{"no carry mode", `abc1234..def5678: 2 changed path(s)
policy: instance + policy
classes matched: code

required checks:
  code-build  mandatory  code
`},
		{"a carry mode that is not one", `abc1234..def5678: 2 changed path(s)
policy: instance + policy
classes matched: code
carry mode: same-file

required checks:
  code-build  mandatory  code
`},
		{"an empty carry mode", `abc1234..def5678: 2 changed path(s)
policy: instance + policy
classes matched: code
carry mode:

required checks:
  code-build  mandatory  code
`},
	} {
		if d, err := parseDerived(c.out); err == nil {
			t.Errorf("%s: read as %+v", c.name, d)
		}
	}
}

// The report of a diff with nothing in it is still a whole report, and it
// requires nothing.
func TestAnEmptyDiffReadsAsRequiringNothing(t *testing.T) {
	d, err := parseDerived(`abc1234..abc1234: 0 changed path(s)
policy: instance + policy
classes matched: none
carry mode: same-paths

required checks: none
`)
	if err != nil {
		t.Fatal(err)
	}
	if d.paths != 0 || len(d.classes) != 0 || len(d.requires) != 0 || d.carry != "same-paths" {
		t.Fatalf("an empty diff reads as %+v", d)
	}
}

// A report of no changed paths is believed only when git agrees. The
// stand-in deriver prints a whole, well-formed report of no changed paths —
// what a deriver whose diff failed silently would print — and the
// derivation is read over two commits with one tree, and refused over two
// commits whose trees differ.
func TestAReportOfNoChangedPathsIsCheckedAgainstGit(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := git(repo, append([]string{"-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	run("init", "--quiet")
	run("commit", "--quiet", "--allow-empty", "-m", "one")
	one := run("rev-parse", "HEAD")
	run("commit", "--quiet", "--allow-empty", "-m", "the same tree")
	same := run("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "changed.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "changed.txt")
	run("commit", "--quiet", "-m", "a change")
	changed := run("rev-parse", "HEAD")

	deriver := filepath.Join(t.TempDir(), "valley")
	report := `printf '%s\n' 'x..y: 0 changed path(s)' 'policy: a + b' 'classes matched: none' 'carry mode: same-paths' '' 'required checks: none'`
	if err := os.WriteFile(deriver, []byte("#!/bin/sh\n"+report+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := policySource{valley: deriver}
	tip := &worktree{dir: repo}
	if _, err := p.derive(tip, one, same); err != nil {
		t.Fatalf("an empty diff reported as empty was refused: %v", err)
	}
	if d, err := p.derive(tip, one, changed); err == nil {
		t.Fatalf("a change reported as touching nothing was read as %+v", d)
	}
}

// The policy digest is of the whole composed policy, so a class's carry
// mode and the restacker's cap are in it as much as a check is. The layers
// are composed by the real cue under the real schema, with the floor fixed
// and the project layer varied.
func TestThePolicyDigestCoversCarryAndTheCap(t *testing.T) {
	cue, err := exec.LookPath("cue")
	if err != nil {
		t.Skip("no cue to compose a policy with; the nix check provides one")
	}
	schema, err := filepath.Abs(filepath.Join("..", "schema", "verification.cue"))
	if err != nil {
		t.Fatal(err)
	}
	const floor = `package verification
floor: {
	checks: "c-build": {runner: "nix", attribute: "c-build"}
	classes: code: {paths: "src/**": true, requires: "c-build": true}
}
`
	digest := func(project string) string {
		t.Helper()
		p := policySource{schema: schema, cue: cue, instance: t.TempDir(), project: t.TempDir()}
		if err := os.WriteFile(filepath.Join(p.instance, "floor.cue"), []byte(floor), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p.project, "policy.cue"), []byte("package verification\n"+project), 0o644); err != nil {
			t.Fatal(err)
		}
		_, sum, err := p.catalogue()
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}

	base := digest("")
	if again := digest(""); again != base {
		t.Fatalf("one policy digested twice gave %s and %s", base, again)
	}
	// The digest is of the composition, not of the text: writing the cap's
	// default composes the same policy.
	if same := digest("project: restack: maxAutomaticVersions: 64\n"); same != base {
		t.Errorf("writing the default cap moved the digest from %s to %s", base, same)
	}
	seen := map[string]string{base: "the floor alone"}
	for _, c := range []struct{ name, project string }{
		{"a class asking for same-class", `project: classes: code: carry: "same-class": true` + "\n"},
		{"a class asking for exact", "project: classes: code: carry: exact: true\n"},
		{"a lower cap", "project: restack: maxAutomaticVersions: 16\n"},
	} {
		sum := digest(c.project)
		if other, ok := seen[sum]; ok {
			t.Errorf("%s digests the same as %s: %s", c.name, other, sum)
		}
		seen[sum] = c.name
	}
}

func TestValidityWindowsAreReadAsThePolicyWritesThem(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"30s", 30 * time.Second},
		{"15m", 15 * time.Minute},
		{"6h", 6 * time.Hour},
		{"2d", 48 * time.Hour},
	} {
		got, err := parseWindow(c.in)
		if err != nil || got != c.want {
			t.Errorf("parseWindow(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
}

// Each layer's absence defaults in the safe direction of that layer's job,
// and the two directions are opposite (dcr-f41f718). Both are pinned here,
// because the value of the rule is that neither is the other.

func TestATargetWithNoProjectLayerIsJudgedUnderTheFloorAlone(t *testing.T) {
	tip := &worktree{dir: t.TempDir()}
	p := policySource{projectDir: "policy/project"}
	done, err := p.openProject(tip)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if p.note == "" {
		t.Fatal("the pass said nothing about composing the floor alone")
	}
	// What the deriver and the composer are handed is a layer that adds
	// nothing, not a directory that is not there.
	docs, err := filepath.Glob(filepath.Join(p.project, "*.cue"))
	if err != nil || len(docs) != 1 {
		t.Fatalf("the resolved project layer holds %v (%v)", docs, err)
	}
	if body, err := os.ReadFile(docs[0]); err != nil || strings.TrimSpace(string(body)) != "package verification" {
		t.Fatalf("the layer that adds nothing reads %q (%v)", body, err)
	}
	done()
	if _, err := os.Stat(p.project); !os.IsNotExist(err) {
		t.Fatalf("the pass left its scratch layer behind: %v", err)
	}
}

func TestATargetsOwnProjectLayerIsUsedWhereItIs(t *testing.T) {
	tip := &worktree{dir: t.TempDir()}
	own := filepath.Join(tip.dir, "policy/project")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "policy.cue"), []byte("package verification\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := policySource{projectDir: "policy/project"}
	done, err := p.openProject(tip)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if p.project != own {
		t.Fatalf("the project layer resolved to %s, not to %s", p.project, own)
	}
	if p.note != "" {
		t.Fatalf("an ordinary composition said %q", p.note)
	}
}

func TestAFloorThatIsNotThereIsRefused(t *testing.T) {
	// The materialized source, which is the one a test can drive without a
	// repository. The repository source refuses the same way, and the e2e
	// observes it there.
	_, done, err := instanceLayer{path: t.TempDir()}.open()
	defer done()
	if err == nil {
		t.Fatal("a floor directory with no documents in it was accepted")
	}
	if !strings.Contains(err.Error(), floorless) {
		t.Fatalf("the refusal reads %q", err)
	}
}

func TestARefusalIsReadAsTheSentenceItIs(t *testing.T) {
	// attest reports what it checked, then refuses, then elaborates. The
	// sentence is the refusal; the elaboration is not.
	out := `note      /tmp/x.note
text      ok (411 bytes signed)
attest: tree digest mismatch: the statement is about a different tree
  recorded aaaa
  found    bbbb
`
	if got := refusalLine(out); got != "tree digest mismatch: the statement is about a different tree" {
		t.Fatalf("refusalLine = %q", got)
	}
	if got := refusalLine("something with no prefix at all\n"); got != "something with no prefix at all" {
		t.Fatalf("refusalLine fallback = %q", got)
	}
}
