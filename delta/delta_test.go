package delta

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// repo is a scratch git repository the tests write objects into.
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) repo {
	t.Helper()
	r := repo{t: t, dir: t.TempDir()}
	r.git(nil, "init", "--quiet")
	return r
}

func (r repo) git(stdin []byte, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+r.dir,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@localhost", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@localhost")
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

func (r repo) blob(content []byte) string {
	return r.git(content, "hash-object", "-w", "--stdin")
}

// rawTree writes a tree object exactly as given, past every check git makes.
func (r repo) rawTree(entries ...rawEntry) string {
	var b bytes.Buffer
	for _, e := range entries {
		b.WriteString(e.mode + " " + e.name + "\x00")
		raw, err := hex.DecodeString(e.oid)
		if err != nil {
			r.t.Fatal(err)
		}
		b.Write(raw)
	}
	return r.git(b.Bytes(), "hash-object", "-t", "tree", "--literally", "-w", "--stdin")
}

type rawEntry struct{ mode, name, oid string }

// file is one file of a vector's tree.
type file struct {
	path, mode string
	content    []byte
}

// tree writes the tree that holds exactly the files given, with git's own
// mktree, which orders each tree's entries.
func (r repo) tree(files []file) string {
	type dir struct {
		files []file
	}
	here := map[string]string{}
	sub := map[string]*dir{}
	for _, f := range files {
		name, rest, nested := strings.Cut(f.path, "/")
		if !nested {
			here[name] = f.mode + " blob " + r.blob(f.content)
			continue
		}
		if sub[name] == nil {
			sub[name] = &dir{}
		}
		sub[name].files = append(sub[name].files, file{path: rest, mode: f.mode, content: f.content})
	}
	for name, d := range sub {
		here[name] = "040000 tree " + r.tree(d.files)
	}
	names := make([]string, 0, len(here))
	for name := range here {
		names = append(names, name)
	}
	sort.Strings(names)
	var input bytes.Buffer
	for _, name := range names {
		input.WriteString(here[name] + "\t" + name + "\x00")
	}
	return r.git(input.Bytes(), "mktree", "-z")
}

func (r repo) commit(tree string) string {
	return r.git(nil, "commit-tree", tree, "-m", "c")
}

func (r repo) repo() Repo { return Repo{Dir: r.dir} }

// specFile is one file of a vector's tree, as valid/<name>.json lists it.
type specFile struct {
	Path       string `json:"path"`
	PathHex    string `json:"path_hex"`
	Mode       string `json:"mode"`
	Content    string `json:"content"`
	ContentHex string `json:"content_hex"`
}

func readVector(t *testing.T, name string) (pre, post []file) {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Pre  []specFile `json:"pre"`
		Post []specFile `json:"post"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	decode := func(list []specFile) []file {
		var out []file
		for _, f := range list {
			path, content := []byte(f.Path), []byte(f.Content)
			if f.PathHex != "" {
				path = mustHex(t, f.PathHex)
			}
			if f.ContentHex != "" {
				content = mustHex(t, f.ContentHex)
			}
			out = append(out, file{path: string(path), mode: f.Mode, content: content})
		}
		return out
	}
	return decode(v.Pre), decode(v.Post)
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Each valid vector's trees, built with real git, give exactly the encoding
// and digest vectors.py computed from the specification, whether they are
// named as trees or as commits. The encoding parses, and writes back byte
// for byte.
func TestTheVectorsHold(t *testing.T) {
	r := newRepo(t)
	specs, err := filepath.Glob("testdata/valid/*.json")
	if err != nil || len(specs) == 0 {
		t.Fatalf("no vectors: %v", err)
	}
	for _, spec := range specs {
		name := strings.TrimSuffix(filepath.Base(spec), ".json")
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(strings.TrimSuffix(spec, ".json") + ".delta")
			if err != nil {
				t.Fatal(err)
			}
			digest, err := os.ReadFile(strings.TrimSuffix(spec, ".json") + ".sha256")
			if err != nil {
				t.Fatal(err)
			}
			pre, post := readVector(t, spec)
			preTree, postTree := r.tree(pre), r.tree(post)
			for _, ids := range [][2]string{{preTree, postTree}, {r.commit(preTree), r.commit(postTree)}} {
				d, err := Between(r.repo(), ids[0], ids[1])
				if err != nil {
					t.Fatalf("Between: %v", err)
				}
				if got := d.Encode(); !bytes.Equal(got, want) {
					t.Fatalf("the encoding is\n%q\nwhere vectors.py has\n%q", got, want)
				}
				if got := d.Digest(); got != strings.TrimSpace(string(digest)) {
					t.Fatalf("the digest is %s where vectors.py has %s", got, digest)
				}
			}
			parsed, err := Parse(want)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !bytes.Equal(parsed.Encode(), want) {
				t.Fatalf("the parsed encoding writes back as\n%q", parsed.Encode())
			}
		})
	}
}

// The delta of a tree against itself is the header alone, and the delta
// back again swaps every pre-image with its post-image.
func TestADeltaReversesExactly(t *testing.T) {
	r := newRepo(t)
	pre, post := readVector(t, "testdata/valid/mixed.json")
	a, b := r.tree(pre), r.tree(post)
	same, err := Between(r.repo(), a, a)
	if err != nil || string(same.Encode()) != Header {
		t.Fatalf("a tree against itself: %q, %v", same.Encode(), err)
	}
	forth, err := Between(r.repo(), a, b)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Between(r.repo(), b, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(forth) != len(back) || len(forth) == 0 {
		t.Fatalf("%d changes forth and %d back", len(forth), len(back))
	}
	for i := range forth {
		if forth[i].Path != back[i].Path || forth[i].Pre != back[i].Post || forth[i].Post != back[i].Pre {
			t.Fatalf("%q does not reverse: %+v against %+v", forth[i].Path, forth[i], back[i])
		}
	}
}

// Every malformed vector is refused.
func TestMalformedEncodingsAreRefused(t *testing.T) {
	vectors, err := filepath.Glob("testdata/malformed/*.delta")
	if err != nil || len(vectors) == 0 {
		t.Fatalf("no vectors: %v", err)
	}
	for _, name := range vectors {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if d, err := Parse(data); err == nil {
			t.Errorf("%s parsed, as %d changes", filepath.Base(name), len(d))
		}
	}
}
