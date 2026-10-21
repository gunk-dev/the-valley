package derive

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"the-valley/composed"
)

// fixture is one policy both derivers are run under, and how many cases
// are drawn for it.
type fixture struct {
	name              string
	instance, project []string
	cases             int
}

// fixtures are the policies this repository carries: its own project
// layer, under the example floor and template own-policy composes it with
// because the owning valley's floor is not in this tree; the scratch policy
// policy-deriver drives; the worked example; the policy self-update's
// smoke test ships; and each carry case cue-vet vets, as a floor under a
// project that adds nothing.
func fixtures(t *testing.T, r *rig) []fixture {
	fs := []fixture{
		{"own", []string{r.path("examples/policy/instance/floor-format.cue"), r.path("examples/policy/instance/template-docs.cue")}, r.glob(t, "policy/project/*.cue"), 16},
		{"deriver", r.glob(t, "examples/policy/deriver/[ft]*.cue"), []string{r.path("examples/policy/deriver/project.cue")}, 30},
		{"example", r.glob(t, "examples/policy/instance/*.cue"), r.glob(t, "examples/policy/project/*.cue"), 16},
		{"smoke", r.glob(t, "examples/policy/smoke/instance/*.cue"), r.glob(t, "examples/policy/smoke/project/*.cue"), 10},
	}
	for _, f := range r.glob(t, "examples/policy/carry/*.cue") {
		fs = append(fs, fixture{"carry-" + strings.TrimSuffix(filepath.Base(f), ".cue"), []string{f}, nil, 6})
	}
	return fs
}

// TestDeriversAgree runs both derivers over every fixture policy and cases
// drawn from a fixed seed, and requires the same answer from each: the
// same documents composed, the same changed paths in the same order, the
// same classes, carry mode and unclassified paths, and the same required
// checks with the same classes, pins, runners, attributes, commands and
// costs. bin/valley is run under every locale (locales), and the fixtures'
// layer directories are named in turn as each of namings names them.
//
// A case is a change from one base tree: files modified, added, deleted,
// renamed and turned into symbolic links, at paths drawn from instances of
// every glob the policy names, near misses of each, and names chosen to be
// awkward. A newline in a path is the one thing no case holds, because
// bin/valley splits such a path in two (TestKnownDivergences).
func TestDeriversAgree(t *testing.T) {
	r := newRig(t)
	locs := r.locales(t)
	for i, f := range fixtures(t, r) {
		t.Run(f.name, func(t *testing.T) {
			l := newLayersAt(t, namings[i%len(namings)], f.instance, f.project, nil)
			m, err := r.read(l)
			if err != nil {
				t.Fatalf("the Go deriver cannot read the policy: %v", err)
			}
			rng := rand.New(rand.NewPCG(20261021, uint64(i)))
			pool := pathPool(m, rng)
			rp := newRepo(t, r)
			base := baseTree(rng, pool)
			baseCommit := rp.commit("", base)
			heads := []string{baseCommit, rp.commit(baseCommit, tree{})}
			for range f.cases {
				heads = append(heads, rp.commit(baseCommit, change(rng, base, pool)))
			}
			for n, head := range heads {
				t.Run(fmt.Sprintf("case-%02d", n), func(t *testing.T) {
					t.Parallel()
					res, err := r.derive(m, rp.dir, baseCommit, head)
					if err != nil {
						t.Fatalf("the Go deriver refused: %v", err)
					}
					for _, loc := range locs {
						rep, err := r.valleyIn(t, loc, rp.dir, r.schema, l, baseCommit, head)
						if err != nil {
							t.Fatalf("bin/valley refused in %s: %v", loc.name, err)
						}
						if diffs := agree(rep, m, res); len(diffs) > 0 {
							t.Errorf("the derivers disagree in %s over %q:\n  %s", loc.name, res.Paths, strings.Join(diffs, "\n  "))
						}
					}
				})
			}
		})
	}
}

// TestReplacementRefsChangeNothing holds both derivers to reading the
// commits they are given, with replacement refs on in git's configuration
// and a replacement standing the uncovered case's head in for another.
func TestReplacementRefsChangeNothing(t *testing.T) {
	r := newRig(t)
	l := newLayers(t, r.glob(t, "examples/policy/deriver/[ft]*.cue"), []string{r.path("examples/policy/deriver/project.cue")}, nil)
	m, err := r.read(l)
	if err != nil {
		t.Fatal(err)
	}
	rp := newRepo(t, r)
	base := rp.commit("", tree{"Makefile": file("seed\n"), "src/one.rs": file("seed\n")})
	uncovered := rp.commit(base, tree{"Makefile": file("edit\n"), "src/one.rs": file("seed\n")})
	flat := rp.commit(base, tree{"Makefile": file("seed\n"), "src/one.rs": file("edit\n")})
	rp.git(nil, "update-ref", "refs/replace/"+uncovered, flat)
	on := "GIT_CONFIG_PARAMETERS='core.usereplacerefs'='true'"
	if got := rp.gitEnv([]string{on}, nil, "diff", "--name-only", base, uncovered); got != "src/one.rs" {
		t.Fatalf("the replacement did not take, so this case proves nothing: %q", got)
	}
	rep, err := r.valley(t, rp.dir, r.schema, l, base, uncovered, on)
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.derive(m, rp.dir, base, uncovered, on)
	if err != nil {
		t.Fatal(err)
	}
	if diffs := agree(rep, m, res); len(diffs) > 0 {
		t.Errorf("the derivers disagree:\n  %s", strings.Join(diffs, "\n  "))
	}
	if got := names(res); !sameSet(got, []string{"c-uncovered"}) {
		t.Errorf("a replacement ref changed what is required: %q", got)
	}
}

// TestLocaleChangesNothing holds both derivers to one answer in every
// locale, for the paths a locale reads differently, and holds that answer
// to bytes. A glob matches bytes: `?` is one byte, so `?.md` covers x.md
// and not é.md, whose é is two, and `**` and `*` cover bytes that are not
// UTF-8, which a UTF-8 locale's regular expressions match nothing of. Two
// paths that differ in one such byte are two paths, which sort -u under
// en_US.UTF-8 would merge.
func TestLocaleChangesNothing(t *testing.T) {
	r := newRig(t)
	l := newLayers(t, r.glob(t, "examples/policy/deriver/[ft]*.cue"), []string{r.path("examples/policy/deriver/project.cue")},
		map[string]string{"bytes.cue": "package verification\n" +
			`project: classes: one: {paths: "?.md": true, requires: "c-notes": true}` + "\n" +
			`project: classes: raw: {paths: "raw/**": true, requires: "c-nested": true}` + "\n"})
	m, err := r.read(l)
	if err != nil {
		t.Fatal(err)
	}
	rp := newRepo(t, r)
	base := rp.commit("", tree{"Makefile": file("seed\n")})
	head := rp.commit(base, tree{
		"Makefile": file("seed\n"), "x.md": file("x\n"), "\xc3\xa9.md": file("e\n"),
		"raw/\xff.bin": file("ff\n"), "src/caf\xe9.rs": file("latin-1\n"),
		"a\xffb": file("ff\n"), "a\xfeb": file("fe\n"),
	})
	res, err := r.derive(m, rp.dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"deep", "flat", "one", "raw"}; !slices.Equal(res.Classes, want) {
		t.Errorf("the Go deriver matched %q; bytes match %q", res.Classes, want)
	}
	if want := []string{"a\xfeb", "a\xffb", "\xc3\xa9.md"}; !slices.Equal(res.Unclassified, want) {
		t.Errorf("the Go deriver left %q unclassified; bytes leave %q", res.Unclassified, want)
	}
	locs := r.locales(t)
	if len(locs) < 2 {
		t.Skip("no UTF-8 locale takes here, so this case proves nothing")
	}
	for _, loc := range locs {
		rep, err := r.valleyIn(t, loc, rp.dir, r.schema, l, base, head)
		if err != nil {
			t.Fatalf("bin/valley refused in %s: %v", loc.name, err)
		}
		if diffs := agree(rep, m, res); len(diffs) > 0 {
			t.Errorf("the derivers disagree in %s:\n  %s", loc.name, strings.Join(diffs, "\n  "))
		}
	}
}

// TestLayerDiscovery holds both derivers to reading a layer as bin/valley's
// `"$dir"/*.cue` does. The directory is a name, so one named like a pattern
// is read and the directories the pattern names are not. A name beginning
// with a dot is not a document. And a layer whose first document is not a
// file holds none, so both refuse it.
func TestLayerDiscovery(t *testing.T) {
	r := newRig(t)
	floor := r.glob(t, "examples/policy/deriver/[ft]*.cue")
	project := []string{r.path("examples/policy/deriver/project.cue")}
	rp := newRepo(t, r)
	base := rp.commit("", tree{"docs/a.md": file("seed\n")})
	head := rp.commit(base, tree{"docs/a.md": file("edit\n"), "src/b.rs": file("new\n")})

	for _, n := range namings {
		t.Run(n.instance+" and "+n.project, func(t *testing.T) {
			l := newLayersAt(t, n, floor, project, nil)
			m, err := r.read(l)
			if err != nil {
				t.Fatalf("the Go deriver cannot read the policy: %v", err)
			}
			want := []string{r.schema, filepath.Join(l.instance, "floor.cue"), filepath.Join(l.instance, "template.cue"), filepath.Join(l.project, "project.cue")}
			if !slices.Equal(m.Documents, want) {
				t.Errorf("the Go deriver composed %q; the layers' documents are %q", m.Documents, want)
			}
			rep, err := r.valley(t, rp.dir, r.schema, l, base, head)
			if err != nil {
				t.Fatalf("bin/valley refused: %v", err)
			}
			res, err := r.derive(m, rp.dir, base, head)
			if err != nil {
				t.Fatal(err)
			}
			if diffs := agree(rep, m, res); len(diffs) > 0 {
				t.Errorf("the derivers disagree:\n  %s", strings.Join(diffs, "\n  "))
			}
		})
	}

	refuse := func(t *testing.T, l layers) {
		t.Helper()
		if rep, err := r.valley(t, rp.dir, r.schema, l, base, head); err == nil || !strings.Contains(err.Error(), "no project policy layer") {
			t.Errorf("bin/valley did not refuse the project layer: %v, %+v", err, rep)
		}
		if m, err := r.read(l); err == nil || !strings.Contains(err.Error(), "no policy layer at "+l.project) {
			t.Errorf("the Go deriver did not refuse the project layer: %v, %q", err, m.Documents)
		}
	}
	t.Run("a dotfile alone", func(t *testing.T) {
		l := newLayers(t, floor, nil, map[string]string{".only.cue": "package verification\n"})
		refuse(t, l)
	})
	t.Run("a directory first", func(t *testing.T) {
		l := newLayers(t, floor, project, nil)
		if err := os.Mkdir(filepath.Join(l.project, "a.cue"), 0o755); err != nil {
			t.Fatal(err)
		}
		refuse(t, l)
	})
}

// TestEveryDerivationGlobal holds the globals agree compares to the ones
// bin/valley declares for its derivation, so a global added there is one
// these tests compare.
func TestEveryDerivationGlobal(t *testing.T) {
	r := newRig(t)
	script, err := os.ReadFile(r.script)
	if err != nil {
		t.Fatal(err)
	}
	declared := []string{"POLICY_LAYER"}
	for _, m := range regexp.MustCompile(`(?m)^(?:declare -A )?((?:DERIVED|CHECK)_[A-Z_]+)=`).FindAllSubmatch(script, -1) {
		declared = append(declared, string(m[1]))
	}
	var compared []string
	for _, g := range derivation {
		compared = append(compared, g.name)
	}
	if !sameSet(declared, compared) {
		t.Errorf("bin/valley declares %q, and the tests compare %q", sortedSet(declared), sortedSet(compared))
	}
}

// TestKnownDivergences records each place bin/valley's deriver is wrong
// (bug bd-11e0e4d) and the Go deriver is not: what each one answers. When
// bin/valley's answer changes, so must this test.
func TestKnownDivergences(t *testing.T) {
	r := newRig(t)
	floor := r.glob(t, "examples/policy/deriver/[ft]*.cue")
	project := []string{r.path("examples/policy/deriver/project.cue")}
	rp := newRepo(t, r)
	seed := tree{"docs/kept.md": file("seed\n"), "Makefile": file("seed\n")}
	base := rp.commit("", seed)
	with := func(path string) string {
		files := tree{path: file("added\n")}
		for p, e := range seed {
			files[p] = e
		}
		return rp.commit(base, files)
	}
	bash := func(t *testing.T, l layers, head string) (*report, error) {
		t.Helper()
		return r.valley(t, rp.dir, r.schema, l, base, head)
	}
	gos := func(t *testing.T, l layers, head string) *Result {
		t.Helper()
		m, err := r.read(l)
		if err != nil {
			t.Fatalf("the Go deriver cannot read the policy: %v", err)
		}
		res, err := r.derive(m, rp.dir, base, head)
		if err != nil {
			t.Fatalf("the Go deriver refused: %v", err)
		}
		return res
	}
	requires := func(t *testing.T, who string, got, want []string) {
		t.Helper()
		if !sameSet(got, want) {
			t.Errorf("%s requires %q, and this test records %q", who, got, want)
		}
	}
	reportNames := func(rep *report) []string {
		var out []string
		for name := range rep.required {
			out = append(out, name)
		}
		return out
	}

	// diff_paths prints each path on a line of its own, so a path holding a
	// newline is read as two. src/a<newline>b.rs matches the flat and deep
	// classes whole; its two halves match no class.
	t.Run("a newline in a path", func(t *testing.T) {
		l := newLayers(t, floor, project, nil)
		head := with("src/a\nb.rs")
		rep, err := bash(t, l, head)
		if err != nil {
			t.Fatal(err)
		}
		if rep.paths != 2 || !sameSet(rep.unclassified, []string{"src/a", "b.rs"}) {
			t.Errorf("bin/valley read %d paths, %q unclassified; this test records 2, src/a and b.rs", rep.paths, rep.unclassified)
		}
		requires(t, "bin/valley", reportNames(rep), []string{"c-uncovered"})
		res := gos(t, l, head)
		if !sameSet(res.Paths, []string{"src/a\nb.rs"}) || len(res.Unclassified) != 0 {
			t.Errorf("the Go deriver read %q, %q unclassified", res.Paths, res.Unclassified)
		}
		requires(t, "the Go deriver", names(res), []string{"c-deep", "c-flat"})
	})

	// The schema's cross-check reads the layers' composition, not `policy`,
	// so a class added by refining `policy` may require a check nobody
	// defines, and cue vet passes. bin/valley derives as usual until a change
	// touches that class, and then fails on an unbound variable while it
	// groups the checks by cost. The Go deriver refuses the policy.
	t.Run("a class added at policy requires an undefined check", func(t *testing.T) {
		l := newLayers(t, floor, project, map[string]string{"ghost.cue": "package verification\n" +
			`policy: classes: sneaky: {paths: "sneak/**": true, requires: ghost: true}` + "\n"})
		if _, err := r.read(l); err == nil || !strings.Contains(err.Error(), "class sneaky requires check ghost, which the composed policy does not define") {
			t.Errorf("the Go deriver read the policy: %v", err)
		}
		_, err := bash(t, l, with("sneak/x"))
		if err == nil || !strings.Contains(err.Error(), "unbound variable") {
			t.Errorf("bin/valley did not fail on an unbound variable for a change touching the class: %v", err)
		}
		rep, err := bash(t, l, with("docs/new.md"))
		if err != nil {
			t.Fatalf("bin/valley refused a change outside the class: %v", err)
		}
		requires(t, "bin/valley", reportNames(rep), []string{"c-docs", "c-tmpl"})
	})

	// bin/valley renders the unclassified set's requirements as a class
	// named unclassified, so a real class of that name shares them. Each
	// requires the other's checks, and the class's requirement is pinned by
	// probing the set. When both require one check, the render states it
	// twice, and bin/valley refuses every derivation.
	t.Run("a class named unclassified", func(t *testing.T) {
		l := newLayers(t, floor, project, map[string]string{"odd.cue": "package verification\n" +
			`project: classes: unclassified: {paths: "odd/**": true, requires: "c-notes": true}` + "\n"})
		odd, uncovered := with("odd/x"), with("Makefile.new")

		rep, err := bash(t, l, odd)
		if err != nil {
			t.Fatal(err)
		}
		requires(t, "bin/valley, for a path in the class,", reportNames(rep), []string{"c-notes", "c-uncovered"})
		if pin := rep.required["c-notes"].pin; pin != "default" {
			t.Errorf("bin/valley pins the class's c-notes %s; this test records default", pin)
		}
		res := gos(t, l, odd)
		requires(t, "the Go deriver, for a path in the class,", names(res), []string{"c-notes"})
		if q := res.Required[0]; q.Pin != composed.Mandatory || !sameSet(q.Classes, []string{"unclassified"}) || q.Unclassified {
			t.Errorf("the Go deriver requires c-notes as %+v", q)
		}

		rep, err = bash(t, l, uncovered)
		if err != nil {
			t.Fatal(err)
		}
		requires(t, "bin/valley, for an uncovered path,", reportNames(rep), []string{"c-notes", "c-uncovered"})
		res = gos(t, l, uncovered)
		requires(t, "the Go deriver, for an uncovered path,", names(res), []string{"c-uncovered"})
		if len(res.Classes) != 0 || !res.Required[0].Unclassified {
			t.Errorf("the Go deriver matched %q and required %+v", res.Classes, res.Required)
		}

		shared := newLayers(t, floor, project, map[string]string{"odd.cue": "package verification\n" +
			`project: classes: unclassified: {paths: "odd/**": true, requires: "c-uncovered": true}` + "\n"})
		if _, err := bash(t, shared, odd); err == nil || !strings.Contains(err.Error(), "rendered 'requires unclassified c-uncovered' twice") {
			t.Errorf("bin/valley did not refuse a class sharing the unclassified set's check: %v", err)
		}
		res = gos(t, shared, odd)
		requires(t, "the Go deriver, for a class sharing the set's check,", names(res), []string{"c-uncovered"})
		if q := res.Required[0]; !sameSet(q.Classes, []string{"unclassified"}) || q.Unclassified {
			t.Errorf("the Go deriver requires c-uncovered as %+v", q)
		}
	})
}

// TestBothRefuse holds both derivers to refusing what neither may derive
// from: a policy cue-vet rejects, and a schema older than either deriver,
// with no cost or no carry modes to read. Read as a policy, either would
// be one whose checks cost nothing or whose classes carry everywhere.
func TestBothRefuse(t *testing.T) {
	r := newRig(t)
	rp := newRepo(t, r)
	base := rp.commit("", tree{"src/one.rs": file("seed\n")})
	head := rp.commit(base, tree{"src/one.rs": file("edit\n"), "a/b": file("new\n")})
	refuse := func(t *testing.T, schema string, l layers) {
		t.Helper()
		if rep, err := r.valley(t, rp.dir, schema, l, base, head); err == nil {
			t.Errorf("bin/valley derived %+v", rep)
		}
		if m, err := composed.Read(t.Context(), r.cue, schema, l.instance, l.project); err == nil {
			t.Errorf("the Go deriver read %+v", m)
		}
	}
	for _, f := range r.glob(t, "examples/policy/carry/rejected/*.cue") {
		t.Run(filepath.Base(f), func(t *testing.T) { refuse(t, r.schema, newLayers(t, []string{f}, nil, nil)) })
	}
	deriver := newLayers(t, r.glob(t, "examples/policy/deriver/[ft]*.cue"), []string{r.path("examples/policy/deriver/project.cue")}, nil)
	body, err := os.ReadFile(r.schema)
	if err != nil {
		t.Fatal(err)
	}
	for name, line := range map[string]string{"costless": "\tcost: *\"quick\" | \"slow\"\n", "modeless": "\n_mode: "} {
		t.Run(name, func(t *testing.T) {
			at := bytes.Index(body, []byte(line))
			if at < 0 {
				t.Fatalf("the schema has no %q, so this case proves nothing", line)
			}
			end := at + 1 + bytes.IndexByte(body[at+1:], '\n')
			older := filepath.Join(t.TempDir(), "verification.cue")
			if err := os.WriteFile(older, append(append([]byte{}, body[:at+1]...), body[end+1:]...), 0o644); err != nil {
				t.Fatal(err)
			}
			refuse(t, older, deriver)
		})
	}
}

// TestGlobsAgree holds Match to bin/valley's glob_ere, run by bash in the
// C locale that derive_required derives in, over every glob the fixture
// policies name, globs drawn from the characters a glob or a regular
// expression treats specially, and paths drawn for each.
func TestGlobsAgree(t *testing.T) {
	r := newRig(t)
	rng := rand.New(rand.NewPCG(20261021, 1000))
	globs := map[string]bool{"docs/": true, "docs//": true, "": true, "**": true, "a**b": true, "**/**/x": true}
	for _, f := range fixtures(t, r) {
		m, err := r.read(newLayers(t, f.instance, f.project, nil))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range m.Classes {
			for _, g := range c.Paths {
				globs[g] = true
			}
		}
	}
	pieces := []string{"a", "b", ".", "*", "?", "/", "**", "\\", "(", ")", "[", "]", "{", "}", "+", "^", "$", "|", "é", " ", "x"}
	for range 150 {
		var g strings.Builder
		for range 1 + rng.IntN(6) {
			g.WriteString(pieces[rng.IntN(len(pieces))])
		}
		globs[g.String()] = true
	}
	sorted := make([]string, 0, len(globs))
	for g := range globs {
		sorted = append(sorted, g)
	}
	sort.Strings(sorted)

	chars := []string{"a", "b", "x", ".", "/", "é", "\xff", "\n", " ", "*", "?", "\\", "md", "("}
	type pair struct{ glob, path string }
	var pairs []pair
	var input bytes.Buffer
	var want strings.Builder
	for _, g := range sorted {
		fmt.Fprintf(&input, "G\x00%s\x00", g)
		paths := map[string]bool{}
		for range 8 {
			paths[instance(g, rng)] = true
		}
		for range 24 {
			var p strings.Builder
			for range rng.IntN(9) {
				p.WriteString(chars[rng.IntN(len(chars))])
			}
			paths[p.String()] = true
		}
		for _, p := range sortedKeys(paths) {
			fmt.Fprintf(&input, "P\x00%s\x00", p)
			pairs = append(pairs, pair{g, p})
			if Match(g, p) {
				want.WriteByte('0')
			} else {
				want.WriteByte('1')
			}
		}
	}
	program := r.functions(t, "segment_ere", "glob_ere") + `
while IFS= read -r -d '' kind && IFS= read -r -d '' value; do
  case "$kind" in
    G) ere="$(glob_ere "$value")" ;;
    P) if [[ $value =~ $ere ]]; then printf 0; else printf '%d' "$?"; fi ;;
  esac
done
`
	got := r.bash(t, program, input.Bytes())
	if len(got) != len(pairs) {
		t.Fatalf("glob_ere answered %d pairs of %d", len(got), len(pairs))
	}
	failed := 0
	for i, p := range pairs {
		if got[i] == want.String()[i] {
			continue
		}
		if failed++; failed <= 10 {
			t.Errorf("glob %q, path %q: glob_ere answers %c, Match %c (0 is a match)", p.glob, p.path, got[i], want.String()[i])
		}
	}
	t.Logf("%d globs, %d pairs, %d disagree", len(sorted), len(pairs), failed)
}

// TestDiffReadersAgree holds Changed to bin/valley's diff_paths over the
// output git diff -z --name-status could give, and over every cut of some
// of it. Each must read the same paths in the same order, or both must
// refuse. diff_paths writes a path on a line of its own, so where a path
// holds a newline, its lines are the path split at each, and that is the
// divergence this records. Changed also refuses two records git never
// writes, which diff_paths reads: an unknown status, and an empty path.
func TestDiffReadersAgree(t *testing.T) {
	r := newRig(t)
	rng := rand.New(rand.NewPCG(20261021, 2000))
	statuses := []string{"A", "M", "D", "T", "R100", "R075", "C050"}
	chars := []string{"a", "b/", "x.md", "é", "\xff", " ", "\t", "*", "\\", "-"}
	pathOf := func(newline bool) string {
		var p strings.Builder
		for range 1 + rng.IntN(5) {
			p.WriteString(chars[rng.IntN(len(chars))])
		}
		if newline {
			p.WriteString("\nmore")
		}
		return p.String()
	}
	var streams [][]byte
	for n := range 200 {
		var s bytes.Buffer
		for range 1 + rng.IntN(4) {
			status := statuses[rng.IntN(len(statuses))]
			fmt.Fprintf(&s, "%s\x00%s\x00", status, pathOf(n%10 == 0))
			if status[0] == 'R' || status[0] == 'C' {
				fmt.Fprintf(&s, "%s\x00", pathOf(false))
			}
		}
		streams = append(streams, s.Bytes())
	}
	for _, s := range streams[:6] {
		for cut := range len(s) {
			streams = append(streams, s[:cut])
		}
	}
	unwritten := [][]byte{[]byte("Z\x00a\x00"), []byte("M\x00\x00")}
	streams = append(streams, unwritten...)

	dir := t.TempDir()
	for i, s := range streams {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%05d.in", i)), s, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	program := r.functions(t, "diff_paths") + `
for f in "$1"/*.in; do
  if diff_paths < "$f" > "${f%.in}.out"; then echo ok; else echo refused; fi
done
`
	verdicts := strings.Fields(r.bash(t, program, nil, dir))
	if len(verdicts) != len(streams) {
		t.Fatalf("diff_paths read %d streams of %d", len(verdicts), len(streams))
	}
	agreed, divergent := 0, 0
	for i, s := range streams {
		paths, err := Changed(s)
		out, rerr := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%05d.out", i)))
		if rerr != nil {
			t.Fatal(rerr)
		}
		isUnwritten := i >= len(streams)-len(unwritten)
		switch {
		case isUnwritten:
			if err == nil || verdicts[i] != "ok" {
				t.Errorf("stream %q: Changed %v, diff_paths %s; this test records a refusal and a reading", s, err, verdicts[i])
			}
			divergent++
		case (err == nil) != (verdicts[i] == "ok"):
			t.Errorf("stream %q: Changed says %v, diff_paths %s", s, err, verdicts[i])
		case err != nil:
			agreed++
		default:
			lines := string(out)
			want := ""
			if len(paths) > 0 {
				want = strings.Join(paths, "\n") + "\n"
			}
			if lines != want {
				t.Errorf("stream %q: Changed read %q, diff_paths wrote %q", s, paths, lines)
			}
			if strings.Count(want, "\n") > len(paths) {
				divergent++
			} else {
				agreed++
			}
		}
	}
	t.Logf("%d streams agree; %d hold a newline path or a record git never writes", agreed, divergent)
}

// functions are bin/valley's definitions of the named functions, to be run
// on their own.
func (r *rig) functions(t *testing.T, names ...string) string {
	t.Helper()
	script, err := os.ReadFile(r.script)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, name := range names {
		start := bytes.Index(script, []byte("\n"+name+"() {\n"))
		if start < 0 {
			t.Fatalf("bin/valley defines no %s", name)
		}
		body := script[start+1:]
		end := bytes.Index(body, []byte("\n}\n"))
		out.Write(body[:end+3])
	}
	return out.String()
}

// bash runs a program with stdin, in the rig's environment, and returns
// what it printed.
func (r *rig) bash(t *testing.T, program string, stdin []byte, args ...string) string {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"-c", program, "bash"}, args...)...)
	cmd.Env = r.env
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bash: %v\n%s", err, stderr.String())
	}
	return stdout.String()
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// instance is a path made from a glob, which mostly matches it: each `**`
// segment made none, one or two segments, each `*` some bytes, and each `?`
// one character. That character is two bytes when it is é, so `?` meets a
// character that is not one byte. A `*` or a `?` is sometimes made a `/`,
// which neither may match.
func instance(glob string, rng *rand.Rand) string {
	segs := strings.Split(glob, "/")
	if segs[len(segs)-1] == "" {
		segs = segs[:len(segs)-1]
	}
	stars := []string{"", "x", "x.y", "ü", "\xff", " ", "*", "q.md", "", "x", "x/y"}
	ones := []string{"q", "é", "\xfe", "?", "q", "/"}
	var out []string
	for _, seg := range segs {
		if seg == "**" {
			out = append(out, []string{"d", "e.md", "ü"}[:rng.IntN(3)]...)
			continue
		}
		var s strings.Builder
		for i := 0; i < len(seg); i++ {
			switch seg[i] {
			case '*':
				s.WriteString(stars[rng.IntN(len(stars))])
			case '?':
				s.WriteString(ones[rng.IntN(len(ones))])
			default:
				s.WriteByte(seg[i])
			}
		}
		out = append(out, s.String())
	}
	return strings.Join(out, "/")
}

// awkward are names chosen for what a reader of paths might get wrong.
var awkward = []string{
	"README.md", "a b.md", "-dash.md", "naïve.md", "caf\xe9.md", "tab\there.md",
	"quote\"d.md", "back\\slash.md", "star*.md", "q?.md", "[br].md", "{x}.md",
	"deep/a/b/c/d/e.txt", ".hidden", "dir.md/inner", "\xff\xfe/x", "research/notes.txt",
	"src/x.rs.bak", "docs", "bin/valley", "flake.nix", "nix/checks/derive-unit.sh",
}

// validPath reports whether git would hold p as a path.
func validPath(p string) bool {
	if p == "" || strings.ContainsAny(p, "\x00\n") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.EqualFold(seg, ".git") {
			return false
		}
	}
	return true
}

// pathPool is what a fixture's cases are drawn from: instances of every
// glob the policy names, near misses of each, and the awkward names.
func pathPool(m *composed.Model, rng *rand.Rand) []string {
	set := map[string]bool{}
	add := func(p string) {
		if validPath(p) {
			set[p] = true
		}
	}
	classes := make([]string, 0, len(m.Classes))
	for name := range m.Classes {
		classes = append(classes, name)
	}
	sort.Strings(classes)
	for _, name := range classes {
		for _, g := range m.Classes[name].Paths {
			for range 3 {
				p := instance(g, rng)
				add(p)
				add("zz/" + p)
				add(p + "x")
				if i := strings.LastIndexByte(p, '/'); i > 0 {
					add(p[:i] + "/zz" + p[i:])
				}
			}
		}
	}
	for _, p := range awkward {
		add(p)
	}
	return sortedKeys(set)
}

// fits reports whether p can join t: no path of t is a directory of p's,
// and p is no directory of a path of t.
func fits(t tree, p string) bool {
	if _, ok := t[p]; ok {
		return false
	}
	for q := range t {
		if strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
			return false
		}
	}
	return true
}

// baseTree is about half the pool.
func baseTree(rng *rand.Rand, pool []string) tree {
	t := tree{}
	for _, p := range pool {
		if rng.IntN(2) == 0 && fits(t, p) {
			t[p] = file("base " + p + "\n")
		}
	}
	return t
}

// change is base with one to six changes made to it.
func change(rng *rand.Rand, base tree, pool []string) tree {
	t := tree{}
	for p, e := range base {
		t[p] = e
	}
	existing := func() string {
		paths := make([]string, 0, len(t))
		for p := range t {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		if len(paths) == 0 {
			return ""
		}
		return paths[rng.IntN(len(paths))]
	}
	fresh := func() string {
		for range 20 {
			if p := pool[rng.IntN(len(pool))]; fits(t, p) {
				return p
			}
		}
		return ""
	}
	for n := range 1 + rng.IntN(6) {
		switch rng.IntN(5) {
		case 0: // modify
			if p := existing(); p != "" {
				t[p] = file(fmt.Sprintf("edit %d of %s\n", n, p))
			}
		case 1: // delete
			if p := existing(); p != "" {
				delete(t, p)
			}
		case 2: // add
			if p := fresh(); p != "" {
				t[p] = file("added " + p + "\n")
			}
		case 3: // rename
			if from := existing(); from != "" {
				e := t[from]
				delete(t, from)
				if to := fresh(); to != "" {
					t[to] = e
				} else {
					t[from] = e
				}
			}
		case 4: // turn into a symbolic link
			if p := existing(); p != "" {
				t[p] = entry{mode: "120000", content: "target-of-" + p}
			}
		}
	}
	return t
}
