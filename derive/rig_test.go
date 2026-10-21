package derive

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"the-valley/composed"
)

// The tests beside this file hold the Go deriver to bin/valley's, which is
// the deriver every caller runs. This file is what they share: the
// rig that runs both, the scratch repositories they run over, and the
// reading of bin/valley's report.
//
// Both derivers run in the C locale. bin/valley matches a glob with bash's
// regular expressions, which read bytes in that locale, and this package
// matches bytes in every locale. bin/valley under another locale is not
// held here.

// rig is what both derivers need: bin/valley, the schema, cue, and an
// environment of the test's own, with no configuration of the person
// running it.
type rig struct {
	root, script, schema, cue string
	env                       []string
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
	r.env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"TMPDIR=" + os.TempDir(),
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=derive-test", "GIT_AUTHOR_EMAIL=derive-test@localhost",
		"GIT_COMMITTER_NAME=derive-test", "GIT_COMMITTER_EMAIL=derive-test@localhost",
		"GIT_AUTHOR_DATE=2026-10-21T00:00:00Z", "GIT_COMMITTER_DATE=2026-10-21T00:00:00Z",
	}
	return r
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

// newLayers copies the floor's documents and the project's into
// directories of their own, and writes extra, by name, beside the
// project's. A project with no documents is one that adds nothing.
func newLayers(t *testing.T, instance, project []string, extra map[string]string) layers {
	t.Helper()
	dir := t.TempDir()
	l := layers{instance: filepath.Join(dir, "instance"), project: filepath.Join(dir, "project")}
	for _, d := range []string{l.instance, l.project} {
		if err := os.Mkdir(d, 0o755); err != nil {
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
// dir and reads its report. A run that fails returns what it said, and must
// print nothing on stdout. env is added to the rig's environment.
func (r *rig) valley(t *testing.T, dir, schema string, l layers, base, head string, env ...string) (*report, error) {
	t.Helper()
	cmd := exec.Command("bash", r.script, "checks", "--schema", schema,
		"--instance", l.instance, "--project", l.project, base, head)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, r.env...), env...)
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
// change requires.
type report struct {
	paths        int                  // the count on the header line
	classes      []string             // classes matched, unclassified included
	carry        string               // the carry mode
	required     map[string]reportRow // check -> its row
	unclassified []string             // the paths listed as matching no class
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

// agree reports every way the Go deriver's answer differs from bin/valley's
// report. bin/valley names the unclassified set as a class called
// unclassified, so that is how the Go answer is read here.
func agree(rep *report, res *Result) []string {
	var diffs []string
	differ := func(what string, bash, gos any) {
		diffs = append(diffs, fmt.Sprintf("%s: bin/valley %q, Go %q", what, bash, gos))
	}
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
