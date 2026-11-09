package main

// The policy bridge, where this program is coupled to cue and to the
// deriver: the policy cue composes, with the window a check writes down
// and the digest of the whole, and what the deriver makes of a diff under
// it. Both are pinned here rather than assumed.

import (
	"the-valley/composed"
	"the-valley/integrator/verdict"

	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// derivationFloor is a floor of two classes and an `unclassified` set. A
// code path requires c-build and sets c-vm due without requiring it; a
// docs path requires c-docs; a path neither covers requires c-smoke, an
// effectful check.
const derivationFloor = `package verification
floor: {
	checks: {
		"c-build": {runner: "nix", attribute: "c-build"}
		"c-docs": {runner: "nix", attribute: "c-docs"}
		"c-vm": {runner: "nix", attribute: "c-vm"}
		"c-smoke": {runner: "command", command: "true", validity: "1h"}
	}
	classes: {
		code: {paths: "src/**": true, requires: "c-build": true, follows: "c-vm": true, carry: exact: true}
		docs: {paths: "docs/**": true, requires: "c-docs": true, carry: "same-class": true}
	}
	unclassified: "c-smoke": true
}
`

// policyRig is a target repository whose policy is derivationFloor and,
// when given, a project layer of its own, composed by real cue under the
// real schema.
type policyRig struct {
	*scratchRepo
	source policySource
}

func newPolicyRig(t *testing.T, project string) *policyRig {
	t.Helper()
	cue, err := exec.LookPath("cue")
	if err != nil {
		t.Skip("no cue to compose a policy with; the nix check provides one")
	}
	schema, err := filepath.Abs(filepath.Join("..", "schema", "verification.cue"))
	if err != nil {
		t.Fatal(err)
	}
	floor := t.TempDir()
	if err := os.WriteFile(filepath.Join(floor, "floor.cue"), []byte(derivationFloor), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &policyRig{scratchRepo: newScratchRepo(t), source: policySource{
		schema:     schema,
		cue:        cue,
		layer:      composed.Floor{Path: floor},
		projectDir: "policy/project",
	}}
	files := map[string]string{"seed": "seed\n"}
	if project != "" {
		files["policy/project/policy.cue"] = "package verification\n" + project
	}
	r.commitFiles(files)
	return r
}

// commitFiles commits files onto the current branch and returns the commit.
func (r *policyRig) commitFiles(files map[string]string) string {
	r.t.Helper()
	for path, body := range files {
		full := filepath.Join(r.repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
	r.git("add", "--all")
	r.git("commit", "--quiet", "-m", "a commit")
	return r.git("rev-parse", "HEAD")
}

// open opens the policy at rev as a request's evaluation does, and ends it
// with the test.
func (r *policyRig) open(rev string) (policySource, *worktree) {
	r.t.Helper()
	tip := &worktree{repo: r.repo, dir: r.repo, rev: rev}
	p, done, err := r.source.open(tip)
	r.t.Cleanup(done)
	if err != nil {
		r.t.Fatal(err)
	}
	return p, tip
}

// derive derives from..to under the policy at from.
func (r *policyRig) derive(from, to string) derived {
	r.t.Helper()
	p, tip := r.open(from)
	d, err := p.derive(tip, from, to)
	if err != nil {
		r.t.Fatal(err)
	}
	return d
}

func classSet(names ...string) map[string]bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return set
}

// A diff is derived as the policy says: the classes its paths match, the
// checks those classes require with each class as its reason, the
// strictest carry mode among them, and no check a class only follows.
func TestADiffRequiresWhatItsClassesRequire(t *testing.T) {
	r := newPolicyRig(t, "")
	base := r.git("rev-parse", "HEAD")
	head := r.commitFiles(map[string]string{"src/a.go": "package a\n", "src/b.go": "package b\n"})
	d := r.derive(base, head)
	want := derived{paths: 2, classes: classSet("code"), requires: map[string][]string{"c-build": {"code"}}, carry: "exact"}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("a code change derives %+v, want %+v", d, want)
	}
}

// A path no class covers is unclassified: the name is among the classes
// matched, and it is the reason for each check the unclassified set
// requires.
func TestAPathNoClassCoversIsUnclassified(t *testing.T) {
	r := newPolicyRig(t, "")
	base := r.git("rev-parse", "HEAD")
	head := r.commitFiles(map[string]string{"docs/a.md": "a\n", "README": "r\n"})
	d := r.derive(base, head)
	want := derived{
		paths:    2,
		classes:  classSet("docs", "unclassified"),
		requires: map[string][]string{"c-docs": {"docs"}, "c-smoke": {"unclassified"}},
		carry:    "same-class",
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("a docs and unclassified change derives %+v, want %+v", d, want)
	}
}

// What an intervening landing touched is the classes the deriver matches
// over base..tip, and an effectful observation of a check one of those
// classes requires does not transfer. An unclassified path is touched as
// the class `unclassified`, and a class the policy itself names
// `unclassified` folds into that one name, so each blocks an observation
// the other requires. An intervening docs change blocks neither.
func TestAnInterveningChangeBlocksWhatItsClassesRequire(t *testing.T) {
	const odd = `project: checks: "c-odd": {runner: "command", command: "true", validity: "1h"}
project: classes: unclassified: {paths: "odd/**": true, requires: "c-odd": true}
`
	for _, c := range []struct {
		name, check     string
		head, interlude string
		blocked         bool
	}{
		{"an unclassified path blocks the class named unclassified", "c-odd", "odd/a", "README", true},
		{"the class named unclassified blocks the unclassified set", "c-smoke", "README", "odd/b", true},
		{"a docs path blocks neither", "c-odd", "odd/a", "docs/a.md", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newPolicyRig(t, odd)
			base := r.git("rev-parse", "HEAD")
			head := r.commitFiles(map[string]string{c.head: "head\n"})
			r.git("checkout", "--quiet", "-b", "target", base)
			tip := r.commitFiles(map[string]string{c.interlude: "interlude\n"})

			p, wt := r.open(tip)
			candidate, err := p.derive(wt, base, head)
			if err != nil {
				t.Fatal(err)
			}
			required, err := p.required(candidate)
			if err != nil {
				t.Fatal(err)
			}
			intervening, err := p.derive(wt, base, tip)
			if err != nil {
				t.Fatal(err)
			}
			if !intervening.classes["unclassified"] && c.blocked {
				t.Fatalf("base..tip touched %v, without unclassified", intervening.classes)
			}
			observed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			v := verdict.Decide(
				verdict.Change{ID: "c1", Target: "refs/heads/main", Base: base, Head: head, Attested: "attested", Evidence: map[string]verdict.Evidence{
					c.check: {Check: c.check, Kind: verdict.Effectful, Runner: "command", Admissible: verdict.Admissible, Result: "passed", ObservedAt: observed},
				}},
				verdict.Snapshot{
					Apply:    verdict.Applied{Clean: true, Tree: "moved"},
					Required: required,
					Touched:  intervening.classes,
					Now:      observed.Add(time.Minute),
				})
			if len(v.Checks) != 1 || v.Checks[0].Name != c.check {
				t.Fatalf("the change requires %+v, want %s alone", v.Checks, c.check)
			}
			if v.Checks[0].Transferred == c.blocked {
				t.Fatalf("with %s intervening, %s transferred %v: %s", c.interlude, c.check, v.Checks[0].Transferred, v.Checks[0].Reason)
			}
		})
	}
}

// A policy that does not compose refuses the request, and leaves no
// materialized layer behind.
func TestAModelThatDoesNotReadIsRefused(t *testing.T) {
	r := newPolicyRig(t, `project: checks: "c-build": runner: "telepathy"`+"\n")
	tip := &worktree{repo: r.repo, dir: r.repo, rev: r.git("rev-parse", "HEAD")}
	p, done, err := r.source.open(tip)
	defer done()
	if err == nil {
		t.Fatal("a policy that does not compose was opened")
	}
	if p.model != nil {
		t.Fatalf("a policy that does not compose was read as %+v", p.model)
	}
	if _, err := os.Stat(p.project); !os.IsNotExist(err) {
		t.Fatalf("the refused request left its project layer behind: %v", err)
	}
}

// A diff that does not read refuses the derivation, from git and from the
// path reader alike.
func TestADiffThatDoesNotReadIsRefused(t *testing.T) {
	r := newPolicyRig(t, "")
	base := r.git("rev-parse", "HEAD")
	p, tip := r.open(base)
	if d, err := p.derive(tip, base, strings.Repeat("0", 40)); err == nil {
		t.Fatalf("a diff to a commit that is not there derived %+v", d)
	}
	p.diff = func(context.Context, string, []string, string, string) ([]string, error) {
		return nil, errors.New("truncated diff output")
	}
	if d, err := p.derive(tip, base, base); err == nil || !strings.Contains(err.Error(), "deriving required checks for") {
		t.Fatalf("a diff that does not read derived %+v, %v", d, err)
	}
}

// A diff with nothing in it requires nothing.
func TestAnEmptyDiffRequiresNothing(t *testing.T) {
	repo := newScratchRepo(t)
	one := repo.git("rev-parse", "HEAD")
	repo.git("commit", "--quiet", "--allow-empty", "-m", "the same tree")
	same := repo.git("rev-parse", "HEAD")
	p := policySource{model: &composed.Model{}}
	d, err := p.derive(&worktree{dir: repo.repo}, one, same)
	if err != nil {
		t.Fatal(err)
	}
	if d.paths != 0 || len(d.classes) != 0 || len(d.requires) != 0 || d.carry != "same-paths" {
		t.Fatalf("an empty diff derives %+v", d)
	}
}

// No changed paths are believed only when git agrees. The path reader
// reads none, as one that failed silently would, and the derivation is
// read over two commits with one tree, and refused over two commits whose
// trees differ.
func TestNoChangedPathsAreCheckedAgainstGit(t *testing.T) {
	repo := newScratchRepo(t)
	one := repo.git("rev-parse", "HEAD")
	repo.git("commit", "--quiet", "--allow-empty", "-m", "the same tree")
	same := repo.git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo.repo, "changed.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo.git("add", "changed.txt")
	repo.git("commit", "--quiet", "-m", "a change")
	changed := repo.git("rev-parse", "HEAD")

	p := policySource{model: &composed.Model{}, diff: func(context.Context, string, []string, string, string) ([]string, error) {
		return nil, nil
	}}
	tip := &worktree{dir: repo.repo}
	if _, err := p.derive(tip, one, same); err != nil {
		t.Fatalf("an empty diff read as empty was refused: %v", err)
	}
	if d, err := p.derive(tip, one, changed); err == nil {
		t.Fatalf("a change read as touching nothing derived %+v", d)
	}
}

// The landing records the digest of the model the request was derived
// under. attest is a stand-in here: it digests the head's tree, which this
// test does not read.
func TestTheLandingRecordsTheModelsDigest(t *testing.T) {
	r := newPolicyRig(t, "")
	tip := r.git("rev-parse", "HEAD")
	head := r.commitFiles(map[string]string{"src/a.go": "package a\n"})
	attest := filepath.Join(t.TempDir(), "attest")
	if err := os.WriteFile(attest, []byte("#!/bin/sh\necho valley-tree-v1:"+strings.Repeat("ab", 32)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	in := &integrator{repo: r.repo, attest: attest, signerName: "integrator", policy: r.source}
	_, snap, land, err := in.materialize(request{change: "c1", target: "refs/heads/main", head: head}, tip, r.source.layer.Path, newStopwatch())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := r.open(tip)
	if land.policy == "" || land.policy != p.model.Digest {
		t.Fatalf("the landing records policy %q, and the model's digest is %q", land.policy, p.model.Digest)
	}
	if !slices.ContainsFunc(snap.Required, func(q verdict.Required) bool { return q.Name == "c-build" }) {
		t.Fatalf("the landing was judged over %+v", snap.Required)
	}
}

// The policy digest is of the whole composed policy, so a class's carry
// mode, the restacker's cap and the hardware approval requirement are in it
// as much as a check is. The layers are composed by the real cue under the
// real schema, with the floor fixed and the project layer varied.
func TestThePolicyDigestCoversCarryTheCapAndHardware(t *testing.T) {
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
		if err := p.read(); err != nil {
			t.Fatal(err)
		}
		return p.model.Digest
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
		// The schema puts no upper bound on the cap. The integrator does
		// not read the cap, so it composes and digests one no Go int holds.
		{"a cap no int holds", "project: restack: maxAutomaticVersions: 9223372036854775808\n"},
		{"a hardware approval requirement", "project: approval: hardware: true\n"},
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

// targetTip commits files onto a scratch repository's main and returns
// the tip as a pass checks it out.
func targetTip(t *testing.T, files map[string]string) (*scratchRepo, *worktree) {
	t.Helper()
	s := newScratchRepo(t)
	for path, body := range files {
		full := filepath.Join(s.repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.git("add", "--all")
	s.git("commit", "--quiet", "--allow-empty", "-m", "the target")
	return s, &worktree{repo: s.repo, dir: s.repo, rev: s.git("rev-parse", "HEAD")}
}

func TestATargetWithNoProjectLayerIsJudgedUnderTheFloorAlone(t *testing.T) {
	_, tip := targetTip(t, map[string]string{"policy/project/notes.md": "not a document\n"})
	p := policySource{projectDir: "policy/project"}
	done, err := p.openProject(tip)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if p.note == "" {
		t.Fatal("the pass said nothing about composing the floor alone")
	}
	// What the composer is handed is a layer that adds nothing, not a directory that is not there.
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

// The project layer is read from the tip's commit, and its directory is a
// name, never a pattern: --project-policy policy/proj* reads that
// directory, and not policy/projX beside it.
func TestATargetsOwnProjectLayerIsReadAtTheTip(t *testing.T) {
	for _, dir := range []string{"policy/project", "policy/proj*"} {
		t.Run(dir, func(t *testing.T) {
			_, tip := targetTip(t, map[string]string{
				dir + "/policy.cue":      "package verification // own\n",
				"policy/projX/decoy.cue": "package verification // decoy\n",
			})
			p := policySource{projectDir: dir}
			done, err := p.openProject(tip)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			if p.note != "" {
				t.Fatalf("an ordinary composition said %q", p.note)
			}
			entries, err := os.ReadDir(p.project)
			if err != nil || len(entries) != 1 || entries[0].Name() != "policy.cue" {
				t.Fatalf("the project layer resolved to %s, holding %v (%v)", p.project, entries, err)
			}
			if body, _ := os.ReadFile(filepath.Join(p.project, "policy.cue")); string(body) != "package verification // own\n" {
				t.Fatalf("the project layer reads %q", body)
			}
		})
	}
}

// A project layer that cannot be read refuses the pass. Read as absent, it
// would judge the change under the floor alone, requiring fewer checks
// from a read that never happened.
func TestAProjectLayerThatCannotBeReadIsRefused(t *testing.T) {
	for _, lost := range []string{"policy/project", "policy/project/policy.cue"} {
		t.Run(lost, func(t *testing.T) {
			s, tip := targetTip(t, map[string]string{"policy/project/policy.cue": "package verification\n"})
			oid := s.git("rev-parse", tip.rev+":"+lost)
			if err := os.Remove(filepath.Join(s.repo, ".git", "objects", oid[:2], oid[2:])); err != nil {
				t.Fatal(err)
			}
			p := policySource{projectDir: "policy/project"}
			done, err := p.openProject(tip)
			defer done()
			if err == nil {
				t.Fatalf("an unreadable project layer resolved to %s, noting %q", p.project, p.note)
			}
			if !strings.Contains(err.Error(), "policy/project") {
				t.Fatalf("the refusal reads %q", err)
			}
		})
	}
}

func TestAFloorThatIsNotThereIsRefused(t *testing.T) {
	// The materialized source, which is the one a test can drive without a
	// repository. The repository source refuses the same way, and the e2e
	// observes it there.
	_, done, err := composed.Floor{Path: t.TempDir()}.Open(context.Background(), "valley-integrator-floor")
	defer done()
	if err == nil {
		t.Fatal("a floor directory with no documents in it was accepted")
	}
	if !strings.Contains(err.Error(), composed.Floorless) {
		t.Fatalf("the refusal reads %q", err)
	}
}

// The floor is every *.cue document directly in its directory, whatever
// its name. Listed one name a line, git quotes a name with non-ASCII
// characters in it, and the quoted name does not end in .cue, so a floor
// read that way skipped naïve.cue. The repository sets core.quotePath to
// the default that quotes, and the document is composed into the model
// like any other.
func TestAFloorDocumentWithANonASCIINameIsRead(t *testing.T) {
	cue, err := exec.LookPath("cue")
	if err != nil {
		t.Skip("no cue to compose a policy with; the nix check provides one")
	}
	schema, err := filepath.Abs(filepath.Join("..", "schema", "verification.cue"))
	if err != nil {
		t.Fatal(err)
	}
	s := newScratchRepo(t)
	s.git("config", "core.quotePath", "true")
	dir := filepath.Join(s.repo, "policy", "instance")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"floor.cue": `package verification
floor: {
	checks: "c-build": {runner: "nix", attribute: "c-build"}
	classes: code: {paths: "src/**": true, requires: "c-build": true}
}
`,
		"naïve.cue": `package verification
floor: checks: "c-lint": {runner: "nix", attribute: "c-lint"}
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.git("add", "policy")
	s.git("commit", "--quiet", "-m", "the floor")

	p := policySource{
		schema:     schema,
		cue:        cue,
		layer:      composed.Floor{Repo: s.repo, Ref: "refs/heads/main", Dir: "policy/instance"},
		projectDir: "policy/project",
	}
	p, done, err := p.open(&worktree{repo: s.repo, dir: s.repo, rev: s.git("rev-parse", "HEAD")})
	defer done()
	if err != nil {
		t.Fatal(err)
	}
	cat := p.model.Checks
	if _, ok := cat["c-lint"]; !ok {
		t.Fatalf("the floor's naïve.cue was not read: the model defines %v", cat)
	}
	if _, ok := cat["c-build"]; !ok {
		t.Fatalf("the floor's floor.cue was not read: the model defines %v", cat)
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
