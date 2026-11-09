package composed

import (
	"bytes"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// layerRepo is a scratch repository with one commit, whose tree holds the
// files written into it.
type layerRepo struct {
	t   *testing.T
	dir string
	tip string
}

func newLayerRepo(t *testing.T, files map[string]string) *layerRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
			t.Fatal("no git on PATH, and VALLEY_TEST_TOOLS=required")
		}
		t.Skip("no git to read a layer with; the composed-unit check provides one")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	r := &layerRepo{t: t, dir: t.TempDir()}
	r.git("init", "--quiet", "--initial-branch=main")
	files["README"] = "a repository\n"
	for path, body := range files {
		full := filepath.Join(r.dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r.git("add", "--all")
	r.git("-c", "user.name=t", "-c", "user.email=t@localhost", "commit", "--quiet", "-m", "layers")
	r.tip = r.git("rev-parse", "HEAD")
	return r
}

func (r *layerRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

// lose removes the loose object that path names at the tip, so reading it
// fails as a damaged repository's read does.
func (r *layerRepo) lose(path string) {
	r.t.Helper()
	oid := r.git("rev-parse", r.tip+":"+path)
	if err := os.Remove(filepath.Join(r.dir, ".git", "objects", oid[:2], oid[2:])); err != nil {
		r.t.Fatal(err)
	}
}

// project is Project of dir at the tip.
func (r *layerRepo) project(dir string) (docs map[string]string, note string, err error) {
	r.t.Helper()
	path, note, done, err := Project(r.t.Context(), r.dir, r.tip, dir, "composed-test-")
	defer done()
	if err != nil {
		if path != "" {
			r.t.Errorf("a refusal resolved the layer to %s", path)
		}
		return nil, note, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		r.t.Fatal(err)
	}
	docs = map[string]string{}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(path, e.Name()))
		if err != nil {
			r.t.Fatal(err)
		}
		docs[e.Name()] = string(body)
	}
	return docs, note, nil
}

const addsNothing = "package verification\n"

// A project layer is the *.cue entries directly in its directory at the
// tip, dotfiles among them, as bin/valley's copy_layer copies them. A
// directory that is not there, or holds no *.cue, composes the floor
// alone.
func TestProjectReadsTheLayerAtTheTip(t *testing.T) {
	r := newLayerRepo(t, map[string]string{
		"policy/project/a.cue":       "package verification // a\n",
		"policy/project/.hidden.cue": "package verification // hidden\n",
		"policy/project/notes.md":    "not a document\n",
		"policy/project/sub/b.cue":   "package verification // nested\n",
		"policy/dotted/.only.cue":    "package verification // only\n",
		"policy/prose/notes.md":      "not a document\n",
	})
	for _, c := range []struct {
		dir   string
		want  map[string]string
		alone bool
	}{
		{"policy/project", map[string]string{"a.cue": "package verification // a\n", ".hidden.cue": "package verification // hidden\n"}, false},
		// Present, and the composer reads no document in it (layer), so
		// composing it is refused as bin/valley refuses it.
		{"policy/dotted", map[string]string{".only.cue": "package verification // only\n"}, false},
		{"policy/missing", map[string]string{"adds-nothing.cue": addsNothing}, true},
		{"policy/prose", map[string]string{"adds-nothing.cue": addsNothing}, true},
		{"README", map[string]string{"adds-nothing.cue": addsNothing}, true},
	} {
		t.Run(c.dir, func(t *testing.T) {
			docs, note, err := r.project(c.dir)
			if err != nil {
				t.Fatal(err)
			}
			if (note != "") != c.alone {
				t.Errorf("the note reads %q", note)
			}
			if !maps.Equal(docs, c.want) {
				t.Errorf("the layer holds %q, not %q", docs, c.want)
			}
		})
	}
}

// A directory is named and never matched: policy/proj* is that directory,
// and not policy/projX beside it.
func TestProjectReadsADirectoryNamedLikeAPatternAsNamed(t *testing.T) {
	r := newLayerRepo(t, map[string]string{
		"policy/proj*/one.cue":   "package verification // one\n",
		"policy/projX/decoy.cue": "package verification // decoy\n",
		":(glob)p/decoy.cue":     "package verification // magic\n",
		"p/two.cue":              "package verification // two\n",
	})
	docs, _, err := r.project("policy/proj*")
	if err != nil || !maps.Equal(docs, map[string]string{"one.cue": "package verification // one\n"}) {
		t.Errorf("policy/proj* read as %q, %v", docs, err)
	}
	// Pathspec magic is a name like any other.
	docs, _, err = r.project(":(glob)p")
	if err != nil || !maps.Equal(docs, map[string]string{"decoy.cue": "package verification // magic\n"}) {
		t.Errorf(":(glob)p read as %q, %v", docs, err)
	}
}

// A layer that cannot be read is refused, never composed as the floor
// alone: a tip that names a missing object, a layer whose tree is gone, a
// document whose blob is gone, and a *.cue entry that is a directory,
// which copy_layer's cat-file blob refuses.
func TestProjectRefusesALayerThatCannotBeRead(t *testing.T) {
	files := map[string]string{
		"policy/project/a.cue":       "package verification\n",
		"policy/odd/x.cue/inner.cue": "package verification\n",
		"policy/odd/y.cue":           "package verification\n",
	}
	cases := []struct {
		name, dir string
		damage    func(r *layerRepo)
	}{
		{"a missing tip", "policy/project", func(r *layerRepo) { r.tip = strings.Repeat("0", len(r.tip)-1) + "1" }},
		{"the layer's tree lost", "policy/project", func(r *layerRepo) { r.lose("policy/project") }},
		{"a document's blob lost", "policy/project", func(r *layerRepo) { r.lose("policy/project/a.cue") }},
		{"a *.cue directory", "policy/odd", func(*layerRepo) {}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newLayerRepo(t, maps.Clone(files))
			c.damage(r)
			docs, note, err := r.project(c.dir)
			if err == nil {
				t.Fatalf("an unreadable layer resolved to %q, %q", docs, note)
			}
			if note != "" {
				t.Errorf("a refusal noted %q", note)
			}
			for _, want := range []string{c.dir, r.dir, r.tip} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not name %s", err, want)
				}
			}
		})
	}
}

// A floor that cannot be read is refused as what it is, and not as a floor
// that is not there, which a reader may look for elsewhere (ErrNoFloor).
func TestOpenTellsAnUnreadableFloorFromAnAbsentOne(t *testing.T) {
	files := map[string]string{
		"policy/instance/floor.cue": "package verification\n",
		"policy/prose/notes.md":     "not a document\n",
	}
	for _, c := range []struct {
		dir       string
		lose      string
		noFloor   bool
		mentioned string
	}{
		{"policy/missing", "", true, "has no policy/missing/"},
		{"policy/prose", "", true, "holds no *.cue in policy/prose/"},
		{"policy/instance", "policy/instance", false, "reading policy/instance/"},
		{"policy/instance", "policy/instance/floor.cue", false, "reading policy/instance/floor.cue"},
	} {
		t.Run(c.dir+" "+c.lose, func(t *testing.T) {
			r := newLayerRepo(t, maps.Clone(files))
			if c.lose != "" {
				r.lose(c.lose)
			}
			dir, done, err := Floor{Repo: r.dir, Ref: "refs/heads/main", Dir: c.dir}.Open(t.Context(), "composed-test-")
			defer done()
			if err == nil {
				t.Fatalf("the floor resolved to %s", dir)
			}
			if errors.Is(err, ErrNoFloor) != c.noFloor {
				t.Errorf("the refusal %q is read as no floor: %v", err, !c.noFloor)
			}
			if !strings.Contains(err.Error(), c.mentioned) {
				t.Errorf("the refusal %q does not say %q", err, c.mentioned)
			}
		})
	}
}
