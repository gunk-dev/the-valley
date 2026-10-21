package derive

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"the-valley/composed"
)

// The tests beside this file hold the Go deriver to bin/valley's, which is
// the deriver every caller runs. This file is what they share: the
// rig that runs both, the scratch repositories they run over, and the
// reading of what bin/valley derived.
//
// bin/valley derives in the C locale whatever its caller's locale is
// (derive_required), and this package matches bytes in every locale. The
// agreement tests run bin/valley under the C locale and under UTF-8 ones
// (locales), as an operator's shell and the integrator's unit may set them,
// so a deriver whose answer changes with the locale disagrees.

// rig is what both derivers need: bin/valley, the schema, cue, and an
// environment of the test's own, with no configuration of the person
// running it.
type rig struct {
	root, script, schema, cue string
	lib                       string   // bin/valley with its last line, which runs main, dropped
	base                      []string // the environment, with no locale in it
	env                       []string // base in the C locale
}

func newRig(t *testing.T) *rig {
	t.Helper()
	for _, tool := range []string{"bash", "git", "cue"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
				t.Fatalf("no %s on PATH, and VALLEY_TEST_TOOLS=required", tool)
			}
			t.Skipf("no %s to run bin/valley's deriver with; the derive-unit check provides one", tool)
		}
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cue, _ := exec.LookPath("cue")
	r := &rig{
		root:   root,
		script: filepath.Join(root, "bin", "valley"),
		schema: filepath.Join(root, "schema", "verification.cue"),
		cue:    cue,
	}
	for _, f := range []string{r.script, r.schema} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("the tests read %s, and it is not there: %v", f, err)
		}
	}
	r.base = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"TMPDIR=" + os.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=derive-test", "GIT_AUTHOR_EMAIL=derive-test@localhost",
		"GIT_COMMITTER_NAME=derive-test", "GIT_COMMITTER_EMAIL=derive-test@localhost",
		"GIT_AUTHOR_DATE=2026-10-21T00:00:00Z", "GIT_COMMITTER_DATE=2026-10-21T00:00:00Z",
	}
	// Where nix's glibc finds the locales other than C and C.UTF-8.
	if archive := os.Getenv("LOCALE_ARCHIVE"); archive != "" {
		r.base = append(r.base, "LOCALE_ARCHIVE="+archive)
	}
	r.env = append(append([]string{}, r.base...), "LC_ALL=C")

	script, err := os.ReadFile(r.script)
	if err != nil {
		t.Fatal(err)
	}
	const last = "\nmain \"$@\"\n"
	if !bytes.HasSuffix(script, []byte(last)) {
		t.Fatalf("bin/valley does not end in %q, so the tests cannot run its verbs one by one", last)
	}
	r.lib = filepath.Join(t.TempDir(), "valley-lib.bash")
	if err := os.WriteFile(r.lib, script[:len(script)-len(last)+1], 0o644); err != nil {
		t.Fatal(err)
	}
	return r
}

// locale is the locale a deriver is run in, and how its environment sets
// it.
type locale struct {
	name string
	env  []string
	utf8 bool // whether bash's regular expressions read characters in it
}

// locales are those both derivers are held to agree under: C, C.UTF-8 set
// by LC_ALL, and en_US.UTF-8 set by LANG alone, as a unit that systemd
// gives the system's locale is run.
var locales = []locale{
	{"C", []string{"LC_ALL=C"}, false},
	{"LC_ALL=C.UTF-8", []string{"LC_ALL=C.UTF-8"}, true},
	{"LANG=en_US.UTF-8", []string{"LANG=en_US.UTF-8"}, true},
}

// locales are the locales this machine has, each shown to take: in a
// UTF-8 one, bash's `?` matches é, two bytes, and in C it does not. A
// UTF-8 locale that does not take would run bin/valley in C again and
// prove nothing, so it is skipped, or fails the test when
// VALLEY_TEST_TOOLS=required.
func (r *rig) locales(t *testing.T) []locale {
	t.Helper()
	var out []locale
	for _, loc := range locales {
		cmd := exec.Command("bash", "-c", `[[ $'\xc3\xa9' =~ ^[^/]$ ]]`)
		cmd.Env = append(append([]string{}, r.base...), loc.env...)
		chars := cmd.Run() == nil
		switch {
		case chars == loc.utf8:
			out = append(out, loc)
		case !loc.utf8:
			t.Fatalf("bash reads characters in %s", loc.name)
		case os.Getenv("VALLEY_TEST_TOOLS") == "required":
			t.Fatalf("%s does not take here, and VALLEY_TEST_TOOLS=required; LOCALE_ARCHIVE is %q", loc.name, os.Getenv("LOCALE_ARCHIVE"))
		default:
			t.Logf("%s does not take here; the derivers are not held to agree under it", loc.name)
		}
	}
	return out
}

// path is a file of the repository the tests run in.
func (r *rig) path(rel string) string { return filepath.Join(r.root, rel) }

// glob is every file of the repository a pattern names, sorted.
func (r *rig) glob(t *testing.T, pattern string) []string {
	t.Helper()
	found, err := filepath.Glob(r.path(pattern))
	if err != nil || len(found) == 0 {
		t.Fatalf("no file matches %s", pattern)
	}
	return found
}

// layers is a policy on disk: the floor's directory and the project
// layer's, as both derivers are given them.
type layers struct{ instance, project string }

// naming is what a policy's layer directories are called, and the decoys
// beside them: directories a reader taking a layer's name as a pattern
// would read instead of it, or as well.
type naming struct {
	instance, project string
	decoys            []string
}

// namings are the layer directories the agreement tests rotate through:
// plain names, and names a glob reads as patterns.
var namings = []naming{
	{"instance", "project", nil},
	{"floor[1]", "proj*", []string{"floor1", "projX"}},
	{`fl\oor?`, "pro ject[a-z]", []string{"floorX", "pro jectq"}},
}

// notCUE is what every decoy and dotfile document holds. A deriver that
// composes one fails, so a deriver that reads the wrong documents cannot
// pass by reading them the same way as the other.
const notCUE = "package verification\nthis is not CUE, and no layer holds it\n"

// newLayers copies the floor's documents and the project's into
// directories of their own, and writes extra, by name, beside the
// project's. A project with no documents is one that adds nothing.
func newLayers(t *testing.T, instance, project []string, extra map[string]string) layers {
	t.Helper()
	return newLayersAt(t, namings[0], instance, project, extra)
}

// newLayersAt is newLayers with the directories named as n names them. Each
// directory also holds a dotfile, .shadow.cue, and each decoy a document,
// decoy.cue, neither of them CUE: neither is a document of the policy.
func newLayersAt(t *testing.T, n naming, instance, project []string, extra map[string]string) layers {
	t.Helper()
	dir := t.TempDir()
	l := layers{instance: filepath.Join(dir, n.instance), project: filepath.Join(dir, n.project)}
	for _, d := range []string{l.instance, l.project} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, ".shadow.cue"), []byte(notCUE), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, decoy := range n.decoys {
		d := filepath.Join(dir, decoy)
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "decoy.cue"), []byte(notCUE), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	copyTo := func(dest string, files []string) {
		for _, f := range files {
			body, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dest, filepath.Base(f)), body, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	copyTo(l.instance, instance)
	copyTo(l.project, project)
	if len(project) == 0 && len(extra) == 0 {
		extra = map[string]string{"adds-nothing.cue": "package verification\n"}
	}
	for name, body := range extra {
		if err := os.WriteFile(filepath.Join(l.project, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

// read is composed.Read of the layers under the rig's schema.
func (r *rig) read(l layers) (*composed.Model, error) {
	return composed.Read(context.Background(), r.cue, r.schema, l.instance, l.project)
}

// valley runs bin/valley's `checks` over two commits of the repository at
// dir, in the C locale, and reads what it derived. A run that fails returns
// what it said, and must print nothing on stdout. env is added to the
// rig's environment.
func (r *rig) valley(t *testing.T, dir, schema string, l layers, base, head string, env ...string) (*report, error) {
	t.Helper()
	return r.valleyIn(t, locales[0], dir, schema, l, base, head, env...)
}

// derivation are the globals bin/valley's derive_required answers with,
// each with its kind: a list, a scalar or a map. Every one is read from
// each run and compared with the Go deriver's answer (agree), and
// TestEveryDerivationGlobal holds this list to the globals bin/valley
// declares.
var derivation = []struct {
	name string
	kind byte // 'L' a list, 'S' a scalar, 'M' a map
}{
	{"POLICY_LAYER", 'L'}, {"DERIVED_PATHS", 'L'}, {"DERIVED_MATCHED", 'L'},
	{"DERIVED_UNCOVERED", 'L'}, {"DERIVED_UNCLASSIFIED", 'S'}, {"DERIVED_CARRY", 'S'},
	{"DERIVED_REQUIRED", 'M'}, {"DERIVED_PINNED", 'M'}, {"CHECK_RUNNER", 'M'},
	{"CHECK_ATTRIBUTE", 'M'}, {"CHECK_COMMAND", 'M'}, {"CHECK_COST", 'M'},
}

// valleyIn is valley in a locale. bin/valley is run as `valley checks`
// runs: its verb, cmd_checks, called as main calls it, whose report is
// what it prints. Then every global of the derivation is written out,
// NUL-separated, so each is read whatever bytes it holds.
func (r *rig) valleyIn(t *testing.T, loc locale, dir, schema string, l layers, base, head string, env ...string) (*report, error) {
	t.Helper()
	var dump strings.Builder
	for _, g := range derivation {
		fmt.Fprintf(&dump, "rig_%c %s\n", g.kind, g.name)
	}
	program := `source "$1"
out="$2"
shift 2
cmd_checks "$@"
rig_L() { local -n rig_l="$1"; printf 'L\0%s\0%d\0' "$1" "${#rig_l[@]}"; [ "${#rig_l[@]}" -eq 0 ] || printf '%s\0' "${rig_l[@]}"; }
rig_S() { printf 'S\0%s\0%s\0' "$1" "${!1}"; }
rig_M() { local -n rig_m="$1"; local k; printf 'M\0%s\0%d\0' "$1" "${#rig_m[@]}"; for k in "${!rig_m[@]}"; do printf '%s\0%s\0' "$k" "${rig_m[$k]}"; done; }
{
` + dump.String() + `} > "$out"
`
	vars := filepath.Join(t.TempDir(), "derivation")
	cmd := exec.Command("bash", "-c", program, "bash", r.lib, vars, "--schema", schema,
		"--instance", l.instance, "--project", l.project, base, head)
	cmd.Dir = dir
	cmd.Env = append(append(append([]string{}, r.base...), loc.env...), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if stdout.Len() > 0 {
			t.Errorf("bin/valley failed and printed to stdout:\n%s", stdout.String())
		}
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	rep, err := parseReport(stdout.String())
	if err != nil {
		t.Fatalf("bin/valley's report does not read: %v\n%s", err, stdout.String())
	}
	out, err := os.ReadFile(vars)
	if err != nil {
		t.Fatal(err)
	}
	if rep.vars, err = parseVars(out); err != nil {
		t.Fatalf("bin/valley's derivation does not read: %v\n%q", err, out)
	}
	return rep, nil
}

// derive is the Go deriver over the same two commits: Diff, then Derive.
func (r *rig) derive(m *composed.Model, dir, base, head string, env ...string) (*Result, error) {
	paths, err := Diff(context.Background(), dir, append(append([]string{}, r.env...), env...), base, head)
	if err != nil {
		return nil, err
	}
	return Derive(m, paths)
}

// report is what bin/valley's `checks` printed, as far as it says what a
// change requires, and the derivation it printed it from.
type report struct {
	paths        int                  // the count on the header line
	classes      []string             // classes matched, unclassified included
	carry        string               // the carry mode
	required     map[string]reportRow // check -> its row
	unclassified []string             // the paths listed as matching no class
	vars         *vars                // the derivation's globals
}

// vars are the globals of bin/valley's derivation, by name.
type vars struct {
	lists   map[string][]string
	scalars map[string]string
	maps    map[string]map[string]string
}

// parseVars reads the globals valleyIn writes: for each, its kind, its
// name, and for a list its length and its items, for a scalar its value,
// and for a map its length and its keys, each followed by its value.
func parseVars(out []byte) (*vars, error) {
	v := &vars{lists: map[string][]string{}, scalars: map[string]string{}, maps: map[string]map[string]string{}}
	fields := strings.Split(string(out), "\x00")
	if len(fields) == 0 || fields[len(fields)-1] != "" {
		return nil, fmt.Errorf("the output does not end in NUL")
	}
	fields = fields[:len(fields)-1]
	next := func() (string, error) {
		if len(fields) == 0 {
			return "", fmt.Errorf("the output ends early")
		}
		f := fields[0]
		fields = fields[1:]
		return f, nil
	}
	count := func() (int, error) {
		f, err := next()
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(f)
	}
	for len(fields) > 0 {
		kind, _ := next()
		name, err := next()
		if err != nil {
			return nil, err
		}
		switch kind {
		case "L":
			n, err := count()
			if err != nil {
				return nil, err
			}
			list := []string{}
			for range n {
				item, err := next()
				if err != nil {
					return nil, err
				}
				list = append(list, item)
			}
			v.lists[name] = list
		case "S":
			if v.scalars[name], err = next(); err != nil {
				return nil, err
			}
		case "M":
			n, err := count()
			if err != nil {
				return nil, err
			}
			m := map[string]string{}
			for range n {
				k, err := next()
				if err != nil {
					return nil, err
				}
				if m[k], err = next(); err != nil {
					return nil, err
				}
			}
			v.maps[name] = m
		default:
			return nil, fmt.Errorf("a global of kind %q", kind)
		}
	}
	for _, g := range derivation {
		_, l := v.lists[g.name]
		_, s := v.scalars[g.name]
		_, m := v.maps[g.name]
		if !l && !s && !m {
			return nil, fmt.Errorf("no %s", g.name)
		}
	}
	return v, nil
}

type reportRow struct {
	pin     string
	classes []string // the classes requiring it, unclassified included
	cost    string   // from the quick: and slow: lines
}

var reportHeader = regexp.MustCompile(`^[0-9a-f]+\.\.[0-9a-f]+: ([0-9]+) changed path\(s\)$`)

func parseReport(out string) (*report, error) {
	rep := &report{required: map[string]reportRow{}}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	m := reportHeader.FindStringSubmatch(lines[0])
	if m == nil {
		return nil, fmt.Errorf("no header line")
	}
	rep.paths, _ = strconv.Atoi(m[1])
	splitList := func(s string) []string {
		if s == "none" {
			return nil
		}
		return strings.Split(s, ", ")
	}
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "classes matched: "):
			rep.classes = splitList(strings.TrimPrefix(line, "classes matched: "))
		case strings.HasPrefix(line, "carry mode: "):
			rep.carry = strings.TrimPrefix(line, "carry mode: ")
		case line == "required checks:":
			for i++; i < len(lines) && lines[i] != ""; i++ {
				f := strings.Fields(lines[i])
				if len(f) < 3 {
					return nil, fmt.Errorf("a required-check row %q", lines[i])
				}
				rep.required[f[0]] = reportRow{pin: f[1], classes: splitList(strings.Join(f[2:], " "))}
			}
		case strings.HasPrefix(line, "unclassified: "):
			for i++; i < len(lines) && lines[i] != ""; i++ {
				if lines[i] == "  the policy requires nothing of them — it is behind the tree" {
					continue
				}
				rep.unclassified = append(rep.unclassified, strings.TrimPrefix(lines[i], "  "))
			}
		case strings.HasPrefix(line, "quick: "), strings.HasPrefix(line, "slow: "):
			cost, list, _ := strings.Cut(line, ": ")
			for _, name := range splitList(list) {
				row, ok := rep.required[name]
				if !ok {
					return nil, fmt.Errorf("%s lists %s, which no row requires", cost, name)
				}
				row.cost = cost
				rep.required[name] = row
			}
		}
	}
	if rep.carry == "" {
		return nil, fmt.Errorf("no carry mode")
	}
	return rep, nil
}

// agree reports every way the Go deriver's answer differs from what
// bin/valley derived: its report, and every global of its derivation, each
// compared whole and in order. bin/valley names the unclassified set as a
// class called unclassified, so that is how the Go answer is read here.
//
// A check's validity is the one field of the Go answer bin/valley does not
// read, so it is not compared.
func agree(rep *report, m *composed.Model, res *Result) []string {
	var diffs []string
	differ := func(what string, bash, gos any) {
		diffs = append(diffs, fmt.Sprintf("%s: bin/valley %q, Go %q", what, bash, gos))
	}
	agreeVars(rep.vars, m, res, differ)
	if rep.paths != len(res.Paths) {
		differ("changed paths", strconv.Itoa(rep.paths), strconv.Itoa(len(res.Paths)))
	}
	classes := append([]string{}, res.Classes...)
	if len(res.Unclassified) > 0 {
		classes = append(classes, "unclassified")
	}
	if !sameSet(rep.classes, classes) {
		differ("classes matched", rep.classes, classes)
	}
	if rep.carry != res.Carry {
		differ("carry mode", rep.carry, res.Carry)
	}
	if !sameSet(rep.unclassified, res.Unclassified) {
		differ("unclassified paths", rep.unclassified, res.Unclassified)
	}
	got := map[string]Required{}
	for _, q := range res.Required {
		got[q.Name] = q
	}
	for name, row := range rep.required {
		q, ok := got[name]
		if !ok {
			differ("required "+name, "required", "not required")
			continue
		}
		by := append([]string{}, q.Classes...)
		if q.Unclassified {
			by = append(by, "unclassified")
		}
		if !sameSet(row.classes, by) {
			differ(name+"'s classes", row.classes, by)
		}
		if row.pin != string(q.Pin) {
			differ(name+"'s pin", row.pin, string(q.Pin))
		}
		if row.cost != q.Cost {
			differ(name+"'s cost", row.cost, q.Cost)
		}
	}
	for name := range got {
		if _, ok := rep.required[name]; !ok {
			differ("required "+name, "not required", "required")
		}
	}
	sort.Strings(diffs)
	return diffs
}

// agreeVars is agree for the globals of bin/valley's derivation.
func agreeVars(v *vars, m *composed.Model, res *Result, differ func(string, any, any)) {
	list := func(name string, want []string) {
		if !slices.Equal(v.lists[name], want) {
			differ(name, v.lists[name], want)
		}
	}
	scalar := func(name, want string) {
		if v.scalars[name] != want {
			differ(name, v.scalars[name], want)
		}
	}
	dict := func(name string, want map[string]string) {
		if !maps.Equal(v.maps[name], want) {
			differ(name, v.maps[name], want)
		}
	}

	list("POLICY_LAYER", m.Documents)
	list("DERIVED_PATHS", res.Paths)
	matched := append([]string{}, res.Classes...)
	if len(res.Unclassified) > 0 {
		matched = append(matched, "unclassified")
	}
	sort.Strings(matched)
	list("DERIVED_MATCHED", matched)
	list("DERIVED_UNCOVERED", res.Unclassified)
	scalar("DERIVED_CARRY", res.Carry)

	unclassified := "0"
	required, pinned := map[string]string{}, map[string]string{}
	runner, attribute, command, cost := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	for _, q := range res.Required {
		by := append([]string{}, q.Classes...)
		if q.Unclassified {
			by = append(by, "unclassified")
			unclassified = "1"
		}
		sort.Strings(by)
		required[q.Name] = strings.Join(by, ", ")
		pinned[q.Name] = string(q.Pin)
		// How each required check is run, as the Go answer gives it.
		if q.Runner != v.maps["CHECK_RUNNER"][q.Name] || q.Attribute != v.maps["CHECK_ATTRIBUTE"][q.Name] ||
			q.Command != v.maps["CHECK_COMMAND"][q.Name] || q.Cost != v.maps["CHECK_COST"][q.Name] {
			differ("how "+q.Name+" is run (runner, attribute, command, cost)",
				[]string{v.maps["CHECK_RUNNER"][q.Name], v.maps["CHECK_ATTRIBUTE"][q.Name], v.maps["CHECK_COMMAND"][q.Name], v.maps["CHECK_COST"][q.Name]},
				[]string{q.Runner, q.Attribute, q.Command, q.Cost})
		}
	}
	scalar("DERIVED_UNCLASSIFIED", unclassified)
	dict("DERIVED_REQUIRED", required)
	dict("DERIVED_PINNED", pinned)

	// The whole catalogue, required or not: an attribute for the nix
	// runner and a command for the command runner, as bin/valley renders
	// them.
	for name, c := range m.Checks {
		runner[name], cost[name] = c.Runner, c.Cost
		switch c.Runner {
		case "nix":
			attribute[name] = c.Attribute
		case "command":
			command[name] = c.Command
		}
	}
	dict("CHECK_RUNNER", runner)
	dict("CHECK_ATTRIBUTE", attribute)
	dict("CHECK_COMMAND", command)
	dict("CHECK_COST", cost)
}

func sameSet(a, b []string) bool {
	a, b = sortedSet(a), sortedSet(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// names are the required checks' names, sorted.
func names(res *Result) []string {
	var out []string
	for _, q := range res.Required {
		out = append(out, q.Name)
	}
	return out
}

// entry is one file of a scratch tree: its mode and its content, which
// for a symbolic link is its target.
type entry struct{ mode, content string }

// tree is a scratch commit's files, by path.
type tree map[string]entry

// repo is a scratch repository whose commits are written straight into
// its object store, so a path may hold any byte a git path may, whatever
// the file system makes of it.
type repo struct {
	t     *testing.T
	dir   string
	env   []string
	blobs map[string]string
	n     int
}

func newRepo(t *testing.T, r *rig) *repo {
	t.Helper()
	rp := &repo{t: t, dir: t.TempDir(), env: r.env, blobs: map[string]string{}}
	rp.git(nil, "init", "--quiet", "--initial-branch=main")
	return rp
}

func (rp *repo) git(stdin []byte, args ...string) string {
	rp.t.Helper()
	return rp.gitEnv(nil, stdin, args...)
}

func (rp *repo) gitEnv(env []string, stdin []byte, args ...string) string {
	rp.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = rp.dir
	cmd.Env = append(append([]string{}, rp.env...), env...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		rp.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// commit writes files as one commit on parent, which may be "", and
// returns it.
func (rp *repo) commit(parent string, files tree) string {
	rp.t.Helper()
	var info bytes.Buffer
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		e := files[p]
		blob, ok := rp.blobs[e.content]
		if !ok {
			blob = rp.git([]byte(e.content), "hash-object", "-w", "--stdin")
			rp.blobs[e.content] = blob
		}
		fmt.Fprintf(&info, "%s %s\t%s\x00", e.mode, blob, p)
	}
	rp.n++
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(rp.dir, ".git", fmt.Sprintf("scratch-index-%d", rp.n))}
	rp.gitEnv(index, info.Bytes(), "update-index", "--add", "-z", "--index-info")
	tr := rp.gitEnv(index, nil, "write-tree")
	args := []string{"commit-tree", tr, "-m", fmt.Sprintf("commit %d", rp.n)}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	return rp.git(nil, args...)
}

// file is a regular file holding content.
func file(content string) entry { return entry{mode: "100644", content: content} }
