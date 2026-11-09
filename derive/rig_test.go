package derive

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"the-valley/composed"
)

// The tests beside this file share this: the rig the deriver runs in, the
// policies on disk it reads, and the scratch repositories it derives over.

// rig is what the deriver needs: the schema, cue, and an environment of the
// test's own for git, with no configuration of the person running it.
type rig struct {
	root, schema, cue string
	env               []string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	for _, tool := range []string{"git", "cue"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
				t.Fatalf("no %s on PATH, and VALLEY_TEST_TOOLS=required", tool)
			}
			t.Skipf("no %s to derive with; the derive-unit check provides one", tool)
		}
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cue, _ := exec.LookPath("cue")
	r := &rig{root: root, schema: filepath.Join(root, "schema", "verification.cue"), cue: cue}
	if _, err := os.Stat(r.schema); err != nil {
		t.Fatalf("the tests read %s, and it is not there: %v", r.schema, err)
	}
	r.env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"TMPDIR=" + os.TempDir(),
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
// layer's.
type layers struct{ instance, project string }

// naming is what a policy's layer directories are called, and the decoys
// beside them: directories a reader taking a layer's name as a pattern
// would read instead of it, or as well.
type naming struct {
	instance, project string
	decoys            []string
}

// namings are the layer directories the tests rotate through: plain names,
// and names a glob reads as patterns.
var namings = []naming{
	{"instance", "project", nil},
	{"floor[1]", "proj*", []string{"floor1", "projX"}},
	{`fl\oor?`, "pro ject[a-z]", []string{"floorX", "pro jectq"}},
}

// notCUE is what every decoy and dotfile document holds, so a reader that
// composes one of them fails.
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

// derive is the deriver over two commits of the repository at dir: Diff,
// then Derive. env is added to the rig's environment.
func (r *rig) derive(m *composed.Model, dir, base, head string, env ...string) (*Result, error) {
	paths, err := Diff(context.Background(), dir, append(append([]string{}, r.env...), env...), base, head)
	if err != nil {
		return nil, err
	}
	return Derive(m, paths)
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
