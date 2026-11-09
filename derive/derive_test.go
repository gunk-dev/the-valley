package derive

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"the-valley/composed"
)

// fixture is one policy the deriver is run under, and how many cases are
// drawn for it.
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

// deriverLayers is the scratch policy of examples/policy/deriver, whose
// floor is one glob boundary per class, with extra documents beside the
// project's.
func (r *rig) deriverLayers(t *testing.T, extra map[string]string) layers {
	t.Helper()
	return newLayers(t, r.glob(t, "examples/policy/deriver/[ft]*.cue"), []string{r.path("examples/policy/deriver/project.cue")}, extra)
}

// TestDrawnChanges derives every fixture policy over changes drawn from a
// fixed seed, and holds each answer to what the test knows of the change
// and the policy: the changed paths are those whose entry differs between
// the two trees; a path is unclassified when no glob of any class matches
// it; the classes are those a path matches; and the required checks are
// what those classes require, with what the unclassified set requires when
// a path is unclassified, each as the policy defines it. The fixtures'
// layer directories are named in turn as each of namings names them.
//
// A case is a change from one base tree: files modified, added, deleted,
// renamed and turned into symbolic links, at paths drawn from instances of
// every glob the policy names, near misses of each, and names chosen to be
// awkward. The change that keeps the base tree and the one that deletes it
// are cases too.
func TestDrawnChanges(t *testing.T) {
	r := newRig(t)
	for i, f := range fixtures(t, r) {
		t.Run(f.name, func(t *testing.T) {
			m, err := r.read(newLayersAt(t, namings[i%len(namings)], f.instance, f.project, nil))
			if err != nil {
				t.Fatalf("the policy does not read: %v", err)
			}
			rng := rand.New(rand.NewPCG(20261021, uint64(i)))
			pool := pathPool(m, rng)
			rp := newRepo(t, r)
			base := baseTree(rng, pool)
			baseCommit := rp.commit("", base)
			heads := []tree{base, {}}
			for range f.cases {
				heads = append(heads, change(rng, base, pool))
			}
			for n, head := range heads {
				res, err := r.derive(m, rp.dir, baseCommit, rp.commit(baseCommit, head))
				if err != nil {
					t.Fatalf("case %d: the deriver refused: %v", n, err)
				}
				for _, miss := range misses(m, base, head, res) {
					t.Errorf("case %d: %s", n, miss)
				}
			}
		})
	}
}

// misses are the ways res differs from what the policy m requires of the
// change from base to head.
func misses(m *composed.Model, base, head tree, res *Result) []string {
	var out []string
	differ := func(what string, got, want []string) {
		if !slices.Equal(got, want) {
			out = append(out, fmt.Sprintf("%s are %q, not %q", what, got, want))
		}
	}
	changed := map[string]bool{}
	for p, e := range base {
		if head[p] != e {
			changed[p] = true
		}
	}
	for p := range head {
		if _, ok := base[p]; !ok {
			changed[p] = true
		}
	}
	paths := sortedKeys(changed)
	differ("the paths", res.Paths, paths)

	matched, required := map[string]bool{}, map[string]bool{}
	var unclassified []string
	for _, p := range paths {
		covered := false
		for name, c := range m.Classes {
			if slices.ContainsFunc(c.Paths, func(g string) bool { return Match(g, p) }) {
				matched[name], covered = true, true
				for k := range c.Requires {
					required[k] = true
				}
			}
		}
		if !covered {
			unclassified = append(unclassified, p)
			for k := range m.Unclassified {
				required[k] = true
			}
		}
	}
	differ("the unclassified paths", res.Unclassified, unclassified)
	differ("the classes", res.Classes, sortedKeys(matched))
	differ("the required checks", names(res), sortedKeys(required))
	for _, q := range res.Required {
		if !reflect.DeepEqual(q.Check, m.Checks[q.Name]) {
			out = append(out, fmt.Sprintf("%s is required as %+v, and the policy defines %+v", q.Name, q.Check, m.Checks[q.Name]))
		}
	}
	return out
}

// TestReplacementRefsChangeNothing holds the deriver to reading the
// commits it is given, with replacement refs on in git's configuration
// and a replacement standing the uncovered case's head in for another.
func TestReplacementRefsChangeNothing(t *testing.T) {
	r := newRig(t)
	m, err := r.read(r.deriverLayers(t, nil))
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
	res, err := r.derive(m, rp.dir, base, uncovered, on)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(res); !slices.Equal(got, []string{"c-uncovered"}) {
		t.Errorf("a replacement ref changed what is required: %q", got)
	}
}

// TestPathsAreBytes holds the deriver to matching bytes. `?` is one byte,
// so `?.md` covers x.md and not é.md, whose é is two, and `**` and `*`
// cover bytes that are not UTF-8. Two paths that differ in one such byte
// are two paths.
func TestPathsAreBytes(t *testing.T) {
	r := newRig(t)
	m, err := r.read(r.deriverLayers(t, map[string]string{"bytes.cue": "package verification\n" +
		`project: classes: one: {paths: "?.md": true, requires: "c-notes": true}` + "\n" +
		`project: classes: raw: {paths: "raw/**": true, requires: "c-nested": true}` + "\n"}))
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
		t.Errorf("the deriver matched %q; bytes match %q", res.Classes, want)
	}
	if want := []string{"a\xfeb", "a\xffb", "\xc3\xa9.md"}; !slices.Equal(res.Unclassified, want) {
		t.Errorf("the deriver left %q unclassified; bytes leave %q", res.Unclassified, want)
	}
}

// TestLayerDiscovery holds composed.Read to reading a layer as the *.cue
// documents in its directory. The directory is a name, so one named like a
// pattern is read and the directories the pattern names are not. A name
// beginning with a dot is not a document. And a layer whose first document
// is not a file holds none, so it is refused.
func TestLayerDiscovery(t *testing.T) {
	r := newRig(t)
	floor := r.glob(t, "examples/policy/deriver/[ft]*.cue")
	project := []string{r.path("examples/policy/deriver/project.cue")}
	for _, n := range namings {
		t.Run(n.instance+" and "+n.project, func(t *testing.T) {
			l := newLayersAt(t, n, floor, project, nil)
			m, err := r.read(l)
			if err != nil {
				t.Fatalf("the policy does not read: %v", err)
			}
			want := []string{r.schema, filepath.Join(l.instance, "floor.cue"), filepath.Join(l.instance, "template.cue"), filepath.Join(l.project, "project.cue")}
			if !slices.Equal(m.Documents, want) {
				t.Errorf("composed %q; the layers' documents are %q", m.Documents, want)
			}
		})
	}

	refuse := func(t *testing.T, l layers) {
		t.Helper()
		if m, err := r.read(l); err == nil || !strings.Contains(err.Error(), "no policy layer at "+l.project) {
			t.Errorf("the project layer was not refused: %v, %q", err, m.Documents)
		}
	}
	t.Run("a dotfile alone", func(t *testing.T) {
		refuse(t, newLayers(t, floor, nil, map[string]string{".only.cue": "package verification\n"}))
	})
	t.Run("a directory first", func(t *testing.T) {
		l := newLayers(t, floor, project, nil)
		if err := os.Mkdir(filepath.Join(l.project, "a.cue"), 0o755); err != nil {
			t.Fatal(err)
		}
		refuse(t, l)
	})
}

// edgeRepo is a scratch repository with one base commit, and with makes a
// head that adds path to it.
func edgeRepo(t *testing.T, r *rig) (rp *repo, base string, with func(path string) string) {
	t.Helper()
	seed := tree{"docs/kept.md": file("seed\n"), "Makefile": file("seed\n")}
	rp = newRepo(t, r)
	base = rp.commit("", seed)
	return rp, base, func(path string) string {
		files := tree{path: file("added\n")}
		for p, e := range seed {
			files[p] = e
		}
		return rp.commit(base, files)
	}
}

// TestANewlineInAPathIsOnePath holds Diff and Derive to reading a path
// that holds a newline whole. src/a<newline>b.rs matches the flat and deep
// classes; a reader splitting it at the newline would find two paths that
// match no class.
func TestANewlineInAPathIsOnePath(t *testing.T) {
	r := newRig(t)
	m, err := r.read(r.deriverLayers(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	rp, base, with := edgeRepo(t, r)
	res, err := r.derive(m, rp.dir, base, with("src/a\nb.rs"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Paths, []string{"src/a\nb.rs"}) || len(res.Unclassified) != 0 {
		t.Errorf("the deriver read %q, %q unclassified", res.Paths, res.Unclassified)
	}
	if got := names(res); !slices.Equal(got, []string{"c-deep", "c-flat"}) {
		t.Errorf("the deriver requires %q", got)
	}
}

// TestAClassAddedAtPolicyRequiringAnUndefinedCheckIsRefused holds
// composed.Read to refusing a class, added by refining `policy`, that
// requires a check the composed policy does not define. The schema's
// cross-check reads the layers' composition, not `policy`, so cue vet
// passes such a policy, and the reader is what refuses a requirement that
// nothing could run.
func TestAClassAddedAtPolicyRequiringAnUndefinedCheckIsRefused(t *testing.T) {
	r := newRig(t)
	l := r.deriverLayers(t, map[string]string{"ghost.cue": "package verification\n" +
		`policy: classes: sneaky: {paths: "sneak/**": true, requires: ghost: true}` + "\n"})
	if _, err := r.read(l); err == nil || !strings.Contains(err.Error(), "class sneaky requires check ghost, which the composed policy does not define") {
		t.Errorf("the policy was read: %v", err)
	}
}

// TestAClassNamedUnclassifiedIsKeptApart holds composed.Read and Derive to
// keeping a class named unclassified apart from the unclassified set: a
// path in the class requires the class's checks, a path no class covers
// requires the set's, and a check both require is required of each for
// its own reason.
func TestAClassNamedUnclassifiedIsKeptApart(t *testing.T) {
	r := newRig(t)
	rp, base, with := edgeRepo(t, r)
	odd, uncovered := with("odd/x"), with("Makefile.new")
	derive := func(t *testing.T, requires, head string) *Result {
		t.Helper()
		m, err := r.read(r.deriverLayers(t, map[string]string{"odd.cue": "package verification\n" +
			`project: classes: unclassified: {paths: "odd/**": true, requires: "` + requires + `": true}` + "\n"}))
		if err != nil {
			t.Fatal(err)
		}
		res, err := r.derive(m, rp.dir, base, head)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	res := derive(t, "c-notes", odd)
	if len(res.Required) != 1 || res.Required[0].Name != "c-notes" {
		t.Fatalf("a path in the class requires %q", names(res))
	}
	if q := res.Required[0]; q.Pin != composed.Mandatory || !slices.Equal(q.Classes, []string{"unclassified"}) || q.Unclassified {
		t.Errorf("a path in the class requires c-notes as %+v", q)
	}

	res = derive(t, "c-notes", uncovered)
	if len(res.Required) != 1 || res.Required[0].Name != "c-uncovered" {
		t.Fatalf("an uncovered path requires %q", names(res))
	}
	if len(res.Classes) != 0 || !res.Required[0].Unclassified {
		t.Errorf("an uncovered path matched %q and required %+v", res.Classes, res.Required)
	}

	res = derive(t, "c-uncovered", odd)
	if len(res.Required) != 1 || res.Required[0].Name != "c-uncovered" {
		t.Fatalf("a class sharing the set's check requires %q", names(res))
	}
	if q := res.Required[0]; !slices.Equal(q.Classes, []string{"unclassified"}) || q.Unclassified {
		t.Errorf("a class sharing the set's check requires c-uncovered as %+v", q)
	}
}

// TestRejectedPoliciesAndOlderSchemasAreRefused holds composed.Read to
// refusing what the deriver may not derive from: a policy cue-vet rejects,
// and a schema older than the deriver, with no cost or no carry modes to
// read. Read as a policy, either would be one whose checks cost nothing or
// whose classes carry everywhere.
func TestRejectedPoliciesAndOlderSchemasAreRefused(t *testing.T) {
	r := newRig(t)
	refuse := func(t *testing.T, schema string, l layers) {
		t.Helper()
		if m, err := composed.Read(t.Context(), r.cue, schema, l.instance, l.project); err == nil {
			t.Errorf("the policy was read: %+v", m)
		}
	}
	for _, f := range r.glob(t, "examples/policy/carry/rejected/*.cue") {
		t.Run(filepath.Base(f), func(t *testing.T) { refuse(t, r.schema, newLayers(t, []string{f}, nil, nil)) })
	}
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
			refuse(t, older, r.deriverLayers(t, nil))
		})
	}
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
