package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"the-valley/change"
	"the-valley/rebase"
)

// roleEnv, set to "main", makes the test binary valley-restacker itself,
// so a test can run the program as a process of its own.
const roleEnv = "RESTACK_TEST_ROLE"

func TestMain(m *testing.M) {
	if os.Getenv(roleEnv) == "main" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// decoy is a repository, and files around it, that an inherited variable
// would point git at instead of the scratch repository.
type decoy struct {
	dir, repo string
}

func newDecoy(t *testing.T, f *fixture) *decoy {
	t.Helper()
	d := &decoy{dir: t.TempDir()}
	d.repo = filepath.Join(d.dir, "decoy.git")
	f.run(d.dir, "init", "--quiet", "--bare", "--initial-branch=main", d.repo)
	for _, sub := range []string{"wt", "objects/info", "objects/pack", "exec", "template/hooks"} {
		if err := os.MkdirAll(filepath.Join(d.dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		// Configuration that sends every fetch from origin to the decoy.
		"gitconfig": "[url \"" + d.repo + "\"]\n\tinsteadOf = " + f.origin + "\n",
		// A program git would run from its exec path to serve a fetch.
		"exec/git-upload-pack": "#!/bin/sh\ntouch '" + filepath.Join(d.dir, "ran") + "'\nexit 1\n",
		// A template git init would copy into a new repository.
		"template/marker": "from the decoy\n",
		"grafts":          "",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(d.dir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// digest is every file under the decoy, by path and content.
func (d *decoy) digest(t *testing.T) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(d.dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		h.Write([]byte(path + "\n"))
		if !e.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			h.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// shape is what a plan says, less what differs from pass to pass: the
// operation and the commits built.
func shape(t *testing.T, out []byte) string {
	t.Helper()
	var p Plan
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatalf("the plan does not read: %v\n%s", err, out)
	}
	p.Operation = ""
	for i := range p.Families {
		if w := p.Families[i].Writes; w != nil {
			for j := range w.Branches {
				w.Branches[j].New = ""
			}
			for j := range w.Versions {
				w.Versions[j].Head, w.Versions[j].Record = "", ""
			}
			for j := range w.Reports {
				w.Reports[j].Commit = ""
			}
		}
	}
	data, _ := json.Marshal(p)
	return string(data)
}

// No variable that points git at another repository, other programs,
// other attributes or another format, or that gives it configuration,
// reaches what a pass runs. With each inherited, pointing at a decoy, the
// pass plans and builds what it does without, the decoy is untouched, and
// nothing the pass prints names it.
func TestNoInheritedGitVariableReachesAPass(t *testing.T) {
	f := newFixture(t, "")
	f.readyAs(idA, "alice", "a", "main", "A's title")
	f.readyAs(idB, "bob", "b", "a", "B's title")
	f.moveMain()
	d := newDecoy(t, f)
	pass := func(t *testing.T) (string, string) {
		t.Helper()
		r := newRestacker(t, f)
		r.c.state = t.TempDir()
		var out, errOut bytes.Buffer
		if err := once(context.Background(), reportOnly(t, r.c, "--json"), &out, &errOut); err != nil {
			return "failed: " + err.Error(), errOut.String()
		}
		if _, err := os.Stat(filepath.Join(r.c.state, "scratch", ".git", "marker")); err == nil {
			return "the decoy's template reached the scratch repository", errOut.String()
		}
		return shape(t, out.Bytes()), errOut.String()
	}
	want, _ := pass(t)
	if strings.HasPrefix(want, "failed") {
		t.Fatal(want)
	}
	before := d.digest(t)
	head := f.at("refs/heads/a")
	for _, tc := range []struct {
		name string
		vars map[string]string
	}{
		{"GIT_DIR", map[string]string{"GIT_DIR": d.repo}},
		{"GIT_WORK_TREE", map[string]string{"GIT_WORK_TREE": filepath.Join(d.dir, "wt")}},
		{"GIT_INDEX_FILE", map[string]string{"GIT_INDEX_FILE": filepath.Join(d.dir, "index")}},
		{"GIT_OBJECT_DIRECTORY", map[string]string{"GIT_OBJECT_DIRECTORY": filepath.Join(d.dir, "objects")}},
		{"GIT_ALTERNATE_OBJECT_DIRECTORIES", map[string]string{"GIT_ALTERNATE_OBJECT_DIRECTORIES": filepath.Join(d.dir, "no-such-objects")}},
		{"GIT_COMMON_DIR", map[string]string{"GIT_COMMON_DIR": d.repo}},
		{"GIT_NAMESPACE", map[string]string{"GIT_NAMESPACE": "decoy"}},
		{"GIT_CONFIG_GLOBAL", map[string]string{"GIT_CONFIG_GLOBAL": filepath.Join(d.dir, "gitconfig")}},
		{"GIT_CONFIG_SYSTEM", map[string]string{"GIT_CONFIG_SYSTEM": filepath.Join(d.dir, "gitconfig"), "GIT_CONFIG_NOSYSTEM": "0"}},
		{"GIT_CONFIG", map[string]string{"GIT_CONFIG": filepath.Join(d.dir, "gitconfig")}},
		{"GIT_CONFIG_COUNT", map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "url." + d.repo + ".insteadOf", "GIT_CONFIG_VALUE_0": f.origin}},
		{"GIT_CONFIG_PARAMETERS", map[string]string{"GIT_CONFIG_PARAMETERS": "'url." + d.repo + ".insteadof'='" + f.origin + "'"}},
		{"GIT_EXEC_PATH", map[string]string{"GIT_EXEC_PATH": filepath.Join(d.dir, "exec")}},
		{"GIT_TEMPLATE_DIR", map[string]string{"GIT_TEMPLATE_DIR": filepath.Join(d.dir, "template")}},
		{"GIT_DEFAULT_HASH", map[string]string{"GIT_DEFAULT_HASH": "sha256"}},
		{"GIT_ATTR_SOURCE", map[string]string{"GIT_ATTR_SOURCE": "no-such-attribute-source"}},
		{"GIT_GRAFT_FILE", map[string]string{"GIT_GRAFT_FILE": filepath.Join(d.dir, "grafts")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "GIT_GRAFT_FILE" {
				// A graft that would cut a's commit from its history.
				if err := os.WriteFile(filepath.Join(d.dir, "grafts"), []byte(head+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				defer os.WriteFile(filepath.Join(d.dir, "grafts"), nil, 0o755)
				before = d.digest(t)
			}
			for name, value := range tc.vars {
				t.Setenv(name, value)
			}
			got, stderr := pass(t)
			if got != want {
				t.Errorf("with %s inherited, the pass says\n%s\nand without\n%s", tc.name, got, want)
			}
			if after := d.digest(t); after != before {
				t.Errorf("with %s inherited, the pass changed the decoy", tc.name)
			}
			if strings.Contains(stderr, d.dir) {
				t.Errorf("with %s inherited, the pass printed\n%s", tc.name, stderr)
			}
		})
	}
}

// The user's own attributes file, which git reads from XDG_CONFIG_HOME or
// HOME whatever configuration it reads, does not change how a pass
// rebases: a merge=union there would resolve a conflict silently.
func TestTheUsersAttributesDoNotChangeARebase(t *testing.T) {
	f := newFixture(t, "")
	f.git("checkout", "--quiet", "-b", "a", "main")
	head := f.commit("a's line", "shared.txt")
	f.git("push", "--quiet", "origin", "a")
	f.writeRecordAt(idA, "a", head)
	f.git("checkout", "--quiet", "main")
	if err := os.WriteFile(filepath.Join(f.work, "shared.txt"), []byte("main's line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.git("add", "shared.txt")
	f.git("commit", "--quiet", "-m", "main's line")
	f.git("push", "--quiet", "origin", "main")

	home := t.TempDir()
	for _, dir := range []string{filepath.Join(home, ".config", "git"), filepath.Join(home, "xdg", "git")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "attributes"), []byte("* merge=union\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	r := newRestacker(t, f)
	var out, errOut bytes.Buffer
	if err := once(context.Background(), reportOnly(t, r.c, "--json"), &out, &errOut); err != nil {
		t.Fatalf("once: %v\n%s", err, errOut.String())
	}
	var p Plan
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Families) != 1 || p.Families[0].Writes == nil || p.Families[0].Writes.Result != "conflict" {
		t.Errorf("with merge=union in the user's attributes, the pass says %s", out.String())
	}
}

// writeRecordAt readies the change id at head on branch, owned by alice.
func (f *fixture) writeRecordAt(id, branch, head string) {
	f.t.Helper()
	f.writeRecord(id, &change.Record{
		Schema: change.Schema, ChangeID: id, Target: rebase.Target, Owner: "alice", State: change.Ready, Seq: 1, Active: 1,
		Versions: []change.Version{{N: 1, Head: change.HeadPrefix + head, Title: "A title", Branch: branch, Uploader: "alice"}},
	}, "", head)
}

// A process whose stderr nobody reads, and is full, still ends: the error
// it ends with is given up on after a few seconds.
func TestABlockedStderrDoesNotHoldTheProcess(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	fd := int(w.Fd())
	if err := syscall.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 4096)
	for {
		if _, err := syscall.Write(fd, chunk); err != nil {
			if errors.Is(err, syscall.EAGAIN) {
				break
			}
			t.Fatal(err)
		}
	}
	if err := syscall.SetNonblock(fd, false); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "once", "--report-only")
	cmd.Env = append(os.Environ(), roleEnv+"=main")
	cmd.Stderr = w
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Errorf("the process ended with %v, not the error it could not print", err)
		}
		if took := time.Since(start); took > sayBound+10*time.Second {
			t.Errorf("the process took %v to end", took)
		}
	case <-time.After(sayBound + 30*time.Second):
		cmd.Process.Kill()
		t.Fatal("a process whose stderr nobody reads did not end")
	}
}
