package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The tree every case starts from: two local modules, a importing b, and
// flow/ importing a third-party module and b.
var base = map[string]string{
	"a/go.mod":     aMod("../b", ""),
	"a/a.go":       "package a\n\nimport (\n\t\"fmt\"\n\t\"the-valley/b/sub\"\n)\n",
	"b/go.mod":     "module the-valley/b\n\ngo 1.24\n",
	"b/b.go":       "package b\n",
	"b/sub/sub.go": "package sub\n\nimport \"strings\"\n",
}

var flow = map[string]string{
	"flow/go.mod":  "module the-valley/flow\n\ngo 1.24\n\nrequire (\n\tgithub.com/x/y v1.0.0\n\tthe-valley/b v0.0.0\n)\n\nreplace the-valley/b => ../b\n",
	"flow/flow.go": "package flow\n\nimport (\n\t\"github.com/x/y\"\n\t\"the-valley/b\"\n)\n",
}

// aMod is a's go.mod, with the-valley/b replaced by replacement and more
// directives after its own.
func aMod(replacement, directives string) string {
	return "module the-valley/a\n\ngo 1.24\n\nrequire the-valley/b v0.0.0\n\nreplace the-valley/b => " +
		replacement + "\n\n" + directives
}

const thirdParty = "package a\n\nimport \"github.com/x/y\"\n"

// A refusal as the cases name it: the file, and the import or directive.
type want struct{ file, subject string }

func TestCheck(t *testing.T) {
	for _, c := range []struct {
		name   string
		noFlow bool
		files  map[string]string // added to base and flow; "-> x" is a symlink to x
		want   []want
	}{
		{name: "standard and local imports, and third-party ones in flow/"},
		{name: "no flow/", noFlow: true},
		{
			name:  "a third-party module with its go.sum in flow/",
			files: map[string]string{"flow/go.sum": "github.com/x/y v1.0.0 h1:AAAA=\ngithub.com/x/y v1.0.0/go.mod h1:BBBB=\n"},
		},
		{
			name:  "a path element with a leading dot",
			files: map[string]string{"a/dot.go": "package a\n\nimport \"the-valley/b/.sub\"\n"},
		},
		{
			name:  "a standard package of another target",
			files: map[string]string{"a/js.go": "//go:build js && wasm\n\npackage a\n\nimport \"syscall/js\"\n"},
		},
		{
			name:  "a third-party import",
			files: map[string]string{"a/c.go": thirdParty},
			want:  []want{{"a/c.go", `"github.com/x/y"`}},
		},
		{
			name:  "a third-party import behind a build constraint",
			files: map[string]string{"a/c.go": "//go:build ignore\n\n" + thirdParty},
			want:  []want{{"a/c.go", `"github.com/x/y"`}},
		},
		{
			name:  "a third-party import in a test file",
			files: map[string]string{"a/c_test.go": thirdParty},
			want:  []want{{"a/c_test.go", `"github.com/x/y"`}},
		},
		{
			name:  "an import from flow/",
			files: map[string]string{"a/c.go": "package a\n\nimport \"the-valley/flow/sub\"\n"},
			want:  []want{{"a/c.go", `"the-valley/flow/sub"`}},
		},
		{
			name: "flow/ declaring another path, and an import of that path",
			files: map[string]string{
				"flow/go.mod": "module the-valley/c\n\ngo 1.24\n",
				"a/c.go":      "package a\n\nimport \"the-valley/c\"\n",
			},
			want: []want{{"a/c.go", `"the-valley/c"`}, {"flow/go.mod", "module the-valley/c"}},
		},
		{
			name:  "a package pattern and a reserved name",
			files: map[string]string{"a/c.go": "package a\n\nimport (\n\t\"en...g/json\"\n\t\"std\"\n\t\"work\"\n)\n"},
			want:  []want{{"a/c.go", `"en...g/json"`}, {"a/c.go", `"std"`}, {"a/c.go", `"work"`}},
		},
		{
			name:  "a malformed Go file",
			files: map[string]string{"a/bad.go": "package a\n\nimport (\n"},
			want:  []want{{"a/bad.go", ""}},
		},
		{
			name:  "a second module declaring the-valley/flow",
			files: map[string]string{"c/go.mod": "module the-valley/flow\n\ngo 1.24\n"},
			want:  []want{{"c/go.mod", "module the-valley/flow"}},
		},
		{
			name:  "a require of flow/",
			files: map[string]string{"a/go.mod": aMod("../b", "require the-valley/flow v0.0.0\n")},
			want:  []want{{"a/go.mod", "require the-valley/flow"}},
		},
		{
			name:  "a tool from an external module",
			files: map[string]string{"a/go.mod": aMod("../b", "tool github.com/x/y/cmd/z\n")},
			want:  []want{{"a/go.mod", "tool github.com/x/y/cmd/z"}},
		},
		{
			name:  "a require of an external module",
			files: map[string]string{"a/go.mod": aMod("../b", "require github.com/x/y v1.0.0\n")},
			want:  []want{{"a/go.mod", "require github.com/x/y"}},
		},
		{
			name: "a replacement outside the repository",
			files: map[string]string{
				"a/go.mod":            aMod("../../outside/b", ""),
				"../outside/b/go.mod": "module the-valley/b\n\ngo 1.24\n",
			},
			want: []want{{"a/go.mod", "replace the-valley/b => ../../outside/b"}},
		},
		{
			name:  "a replacement by an absolute path",
			files: map[string]string{"a/go.mod": aMod("/nonexistent/b", "")},
			want:  []want{{"a/go.mod", "replace the-valley/b => /nonexistent/b"}},
		},
		{
			name:  "a replacement into flow/",
			files: map[string]string{"a/go.mod": aMod("../flow", "")},
			want:  []want{{"a/go.mod", "replace the-valley/b => ../flow"}},
		},
		{
			name: "a replacement through a symlink into flow/",
			files: map[string]string{
				"a/go.mod":        aMod("../f", ""),
				"f":               "-> flow/sub",
				"flow/sub/sub.go": "package sub\n",
			},
			want: []want{{"a/go.mod", "replace the-valley/b => ../f"}},
		},
		{
			name: "an external module replaced with a local directory",
			files: map[string]string{
				"a/go.mod": aMod("../b", "require github.com/x/y v1.0.0\n\nreplace github.com/x/y => ../b\n"),
				"a/c.go":   thirdParty,
			},
			want: []want{
				{"a/c.go", `"github.com/x/y"`},
				{"a/go.mod", "replace github.com/x/y => ../b"},
				{"a/go.mod", "require github.com/x/y"},
			},
		},
		{
			name:  "a local module replaced with an external one",
			files: map[string]string{"a/go.mod": aMod("example.org/b v1.0.0", "")},
			want:  []want{{"a/go.mod", "replace the-valley/b => example.org/b v1.0.0"}},
		},
		{
			name:  "a local module declaring an external path",
			files: map[string]string{"b/go.mod": "module example.org/b\n\ngo 1.24\n"},
			want: []want{
				{"a/a.go", `"the-valley/b/sub"`},
				{"a/go.mod", "replace the-valley/b => ../b"},
				{"a/go.mod", "require the-valley/b"},
				{"b/go.mod", "module example.org/b"},
			},
		},
		{
			name: "a nested module with a third-party import",
			files: map[string]string{
				"a/inner/go.mod":   "module the-valley/a/inner\n\ngo 1.24\n",
				"a/inner/inner.go": "package inner\n\nimport \"github.com/x/y\"\n",
			},
			want: []want{{"a/inner/inner.go", `"github.com/x/y"`}},
		},
		{
			name:  "a symlinked file from flow/",
			files: map[string]string{"a/f.go": "-> ../flow/flow.go"},
			want:  []want{{"a/f.go", `"github.com/x/y"`}},
		},
		{
			name:  "a dangling symlinked Go file",
			files: map[string]string{"a/gone.go": "-> missing.go"},
			want:  []want{{"a/gone.go", "missing.go"}},
		},
		{
			name:  "a Go file's name linked to a file of another name",
			files: map[string]string{"a/notes.go": "-> ../notes.txt", "notes.txt": thirdParty},
			want:  []want{{"a/notes.go", "../notes.txt"}},
		},
		{
			name:  "a symlink leaving the tree",
			files: map[string]string{"a/out": "-> ../../outside", "../outside/out.go": thirdParty},
			want:  []want{{"a/out", "../../outside"}},
		},
		{
			name:  "a symlink loop",
			files: map[string]string{"a/loop": "-> .."},
			want:  []want{{"a/loop", ""}},
		},
		{
			name:  "a workspace",
			files: map[string]string{"go.work": "go 1.24\n\nuse ./a\n"},
			want:  []want{{"go.work", ""}},
		},
		{
			name:  "a vendor directory",
			files: map[string]string{"a/vendor/modules.txt": "# github.com/x/y v1.0.0\n"},
			want:  []want{{"a/vendor", ""}},
		},
		{
			name:  "a vendor symlink into flow/",
			files: map[string]string{"a/vendor": "-> ../flow"},
			want:  []want{{"a/vendor", ""}},
		},
		{
			name:  "a go.mod the go command cannot parse",
			files: map[string]string{"a/go.mod": "module the-valley/a\n\nrequire (\n"},
			want:  []want{{"a/go.mod", ""}},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			layers := []map[string]string{base, flow, c.files}
			if c.noFlow {
				layers = []map[string]string{base, c.files}
			}
			refusals, _, _, err := check(fixture(t, layers...))
			if err != nil {
				t.Fatal(err)
			}
			var got []want
			for _, r := range refusals {
				if r.File == "" || r.Reason == "" {
					t.Errorf("a refusal names no file or no reason: %+v", r)
				}
				got = append(got, want{r.File, r.Subject})
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("refused %v, want %v; refusals:\n%v", got, c.want, refusals)
			}
		})
	}
}

// fixture writes the files of each layer, later layers replacing earlier
// ones, under a fresh root, and returns the root. A path may climb out of
// the root, and a value "-> target" writes a symlink to target.
func fixture(t *testing.T, layers ...map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	files := map[string]string{}
	for _, l := range layers {
		for name, content := range l {
			files[name] = content
		}
	}
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		var err error
		if target, ok := strings.CutPrefix(content, "-> "); ok {
			err = os.Symlink(target, p)
		} else {
			err = os.WriteFile(p, []byte(content), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return root
}
