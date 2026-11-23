package flowenv

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestVarPrecedence(t *testing.T) {
	for _, v := range All {
		t.Run(v.New, func(t *testing.T) {
			unsetBoth := func(t *testing.T) {
				t.Helper()
				t.Setenv(v.New, "")
				t.Setenv(v.Legacy, "")
				_ = os.Unsetenv(v.New)
				_ = os.Unsetenv(v.Legacy)
			}

			t.Run("neither set", func(t *testing.T) {
				unsetBoth(t)
				if got, ok := v.Lookup(); ok || got != "" {
					t.Errorf("Lookup() = (%q, %v), want (\"\", false)", got, ok)
				}
				if got := v.Get(); got != "" {
					t.Errorf("Get() = %q, want \"\"", got)
				}
			})

			t.Run("legacy only", func(t *testing.T) {
				unsetBoth(t)
				t.Setenv(v.Legacy, "old-val")
				if got, ok := v.Lookup(); !ok || got != "old-val" {
					t.Errorf("Lookup() = (%q, %v), want (\"old-val\", true)", got, ok)
				}
				if got := v.Get(); got != "old-val" {
					t.Errorf("Get() = %q, want \"old-val\"", got)
				}
			})

			t.Run("new only", func(t *testing.T) {
				unsetBoth(t)
				t.Setenv(v.New, "new-val")
				if got, ok := v.Lookup(); !ok || got != "new-val" {
					t.Errorf("Lookup() = (%q, %v), want (\"new-val\", true)", got, ok)
				}
				if got := v.Get(); got != "new-val" {
					t.Errorf("Get() = %q, want \"new-val\"", got)
				}
			})

			t.Run("both set prefers new", func(t *testing.T) {
				unsetBoth(t)
				t.Setenv(v.Legacy, "old-val")
				t.Setenv(v.New, "new-val")
				if got, ok := v.Lookup(); !ok || got != "new-val" {
					t.Errorf("Lookup() = (%q, %v), want (\"new-val\", true)", got, ok)
				}
				if got := v.Get(); got != "new-val" {
					t.Errorf("Get() = %q, want \"new-val\"", got)
				}
			})

			t.Run("new set to empty overrides legacy", func(t *testing.T) {
				unsetBoth(t)
				t.Setenv(v.Legacy, "old-val")
				t.Setenv(v.New, "")
				if got, ok := v.Lookup(); !ok || got != "" {
					t.Errorf("Lookup() = (%q, %v), want (\"\", true)", got, ok)
				}
				if got := v.Get(); got != "" {
					t.Errorf("Get() = %q, want \"\"", got)
				}
			})
		})
	}
}

// A child gets the value flow sets under the legacy name, whatever either
// spelling it inherits, in each form flow builds a child's environment in.
func TestSetForChild(t *testing.T) {
	const value = "it's set"
	for _, v := range All {
		t.Run(v.New, func(t *testing.T) {
			path := "PATH=" + os.Getenv("PATH")
			inherited := []string{path, v.New + "=stale", v.Legacy + "=stale"}
			got := v.Set(inherited, value)
			if want := []string{path, v.Legacy + "=" + value}; !reflect.DeepEqual(got, want) {
				t.Errorf("Set() = %q, want %q", got, want)
			}

			report := `printf '%s|%s' "${` + v.New + `-unset}" "${` + v.Legacy + `-unset}"`
			for name, tc := range map[string]struct{ script, want string }{
				"Export":      {v.Export(value) + report, "unset|" + value},
				"EnvPrefix":   {v.EnvPrefix(value) + "sh -c " + shellQuote(report), "unset|" + value},
				"UnsetPrefix": {v.UnsetPrefix() + "sh -c " + shellQuote(report), "unset|unset"},
			} {
				cmd := exec.Command("sh", "-c", tc.script)
				cmd.Env = inherited
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s: %v: %s", name, err, out)
				}
				if string(out) != tc.want {
					t.Errorf("%s: child saw %q, want %q", name, out, tc.want)
				}
			}
		})
	}
}

// TestOnlyFlowenvSpellsFlowVars fails if production Go code outside flowenv
// spells a listed variable's name, so every read, set and clear of one goes
// through flowenv. Prose that mentions a name is allowed.
func TestOnlyFlowenvSpellsFlowVars(t *testing.T) {
	flowRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	skip := map[string]bool{}
	for _, dir := range []string{
		"internal/flowenv",
		// Test support no binary links: it clears both spellings from its own
		// test process before any test runs.
		"internal/tmux/tmuxtest",
	} {
		skip[filepath.Join(flowRoot, dir)] = true
	}

	spellings := newSpellingFinder()
	fset := token.NewFileSet()
	files := 0
	err = filepath.WalkDir(flowRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if skip[path] || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		files++
		for _, v := range spellings.find(fset, f) {
			rel, _ := filepath.Rel(flowRoot, path)
			t.Errorf("%s:%d: %s outside flowenv; read, set or clear it through flowenv", rel, v.line, v.what)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatalf("found no production Go files under %s", flowRoot)
	}
}

func TestSpellingFinder(t *testing.T) {
	src := `package sample
import (
	"fmt"
	"os"
	fe "the-valley/flow/internal/flowenv"
	"example.org/other"
)
const sessionIDEnv = "KLAUS_SESSION_ID"
func bad(env []string, id string) {
	_, _ = os.LookupEnv("VALLEY_FLOW_RUN_ID")
	env = append(env, "KLAUS_BACKEND="+id)
	_ = fmt.Sprintf("export KLAUS_RUN_ID=%s; ", id)
	_ = "env -u TMUX -u VALLEY_FLOW_SCRATCH cmd"
	_ = "unset TMUX KLAUS_TMUX_SOCKET; "
	_ = fe.Scratch.New
	_ = os.Getenv(fe.TmuxSocket.Legacy)
}
func good() {
	_ = fmt.Errorf("KLAUS_SESSION_ID is not set")
	_ = "go build -o \"$KLAUS_SCRATCH/<name>\""
	_ = "KLAUS_SCRATCHPAD=1 MY_KLAUS_BACKEND=x"
	_ = os.Getenv("TMUX")
	_ = other.Scratch.New
	_ = fe.SessionID.Get()
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sample.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := newSpellingFinder().find(fset, f)
	var lines []int
	for _, v := range got {
		lines = append(lines, v.line)
	}
	if want := []int{8, 10, 11, 12, 13, 14, 15, 16}; !reflect.DeepEqual(lines, want) {
		t.Errorf("found spellings on lines %v, want %v: %+v", lines, want, got)
	}
}

type spelling struct {
	line int
	what string
}

// spellingFinder finds where a file spells a listed name: a string literal
// that is one, that assigns one (NAME=), or that unsets one in shell text
// (unset NAME, env -u NAME), or a Var's New or Legacy field.
type spellingFinder struct {
	names          map[string]bool
	assign, unsets *regexp.Regexp
}

func newSpellingFinder() spellingFinder {
	names := map[string]bool{}
	var alts []string
	for _, v := range All {
		for _, n := range []string{v.New, v.Legacy} {
			names[n] = true
			alts = append(alts, regexp.QuoteMeta(n))
		}
	}
	any := "(?:" + strings.Join(alts, "|") + ")"
	return spellingFinder{
		names:  names,
		assign: regexp.MustCompile(`(?:^|[^A-Za-z0-9_])` + any + `=`),
		unsets: regexp.MustCompile(`(?:\bunset(?:\s+[A-Za-z_][A-Za-z0-9_]*)*\s+|-u\s*)` + any + `(?:[^A-Za-z0-9_]|$)`),
	}
}

func (s spellingFinder) find(fset *token.FileSet, f *ast.File) []spelling {
	flowenvAliases := map[string]bool{}
	for _, imp := range f.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); path == "the-valley/flow/internal/flowenv" {
			alias := filepath.Base(path)
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			flowenvAliases[alias] = true
		}
	}
	var found []spelling
	ast.Inspect(f, func(n ast.Node) bool {
		line := func() int { return fset.Position(n.Pos()).Line }
		switch e := n.(type) {
		case *ast.ImportSpec:
			return false
		case *ast.BasicLit:
			lit, ok := stringLiteral(e)
			switch {
			case !ok:
			case s.names[lit]:
				found = append(found, spelling{line(), "names " + lit})
			case s.assign.MatchString(lit):
				found = append(found, spelling{line(), "assigns " + s.assign.FindString(lit)})
			case s.unsets.MatchString(lit):
				found = append(found, spelling{line(), "clears " + s.unsets.FindString(lit)})
			}
		case *ast.SelectorExpr:
			if e.Sel.Name != "New" && e.Sel.Name != "Legacy" {
				return true
			}
			if inner, ok := e.X.(*ast.SelectorExpr); ok {
				if pkg, ok := inner.X.(*ast.Ident); ok && flowenvAliases[pkg.Name] {
					found = append(found, spelling{line(), "names flowenv." + inner.Sel.Name + "." + e.Sel.Name})
				}
			}
		}
		return true
	})
	return found
}

func stringLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// useGeneration points HOME at a host config that sets naming_generation g.
func useGeneration(t *testing.T, g string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".klaus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".klaus", "config.json"), []byte(`{"naming_generation": "`+g+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// childSees runs report in sh with env and returns what it printed.
func childSees(t *testing.T, env []string, script string) string {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return string(out)
}

// Under bridge a child gets each variable flow sets under both names with
// one value, an explicitly empty one included, whatever it inherited; one
// flow removes, under neither.
func TestBridgeSetsBothNames(t *testing.T) {
	useGeneration(t, "bridge")
	for _, v := range All {
		t.Run(v.New, func(t *testing.T) {
			path := "PATH=" + os.Getenv("PATH")
			inherited := []string{path, v.New + "=new-stale", v.Legacy + "=legacy-stale"}
			report := `printf '%s|%s' "${` + v.New + `-unset}" "${` + v.Legacy + `-unset}"`
			for _, value := range []string{"it's set", ""} {
				got := v.Set(inherited, value)
				if want := []string{path, v.New + "=" + value, v.Legacy + "=" + value}; !reflect.DeepEqual(got, want) {
					t.Errorf("Set(%q) = %q, want %q", value, got, want)
				}
				both := value + "|" + value
				if got := childSees(t, inherited, v.Export(value)+report); got != both {
					t.Errorf("Export(%q): child saw %q, want %q", value, got, both)
				}
				if got := childSees(t, inherited, v.EnvPrefix(value)+"sh -c "+shellQuote(report)); got != both {
					t.Errorf("EnvPrefix(%q): child saw %q, want %q", value, got, both)
				}
			}
			if got := childSees(t, inherited, v.UnsetPrefix()+"sh -c "+shellQuote(report)); got != "unset|unset" {
				t.Errorf("UnsetPrefix: child saw %q", got)
			}
			if v.Name() != v.New {
				t.Errorf("Name() = %q, want %q", v.Name(), v.New)
			}
		})
	}
}

// Normalize resolves each variable as Lookup would, the new name first and
// an empty value included, and gives the child it under both names, or
// under neither; under legacy it leaves the environment as it is.
func TestNormalize(t *testing.T) {
	v := Scratch
	cases := []struct {
		name string
		env  []string
		want []string
	}{
		{"names that disagree", []string{"A=1", v.Legacy + "=old", v.New + "=new"}, []string{"A=1", v.New + "=new", v.Legacy + "=new"}},
		{"legacy alone", []string{v.Legacy + "=old", "A=1"}, []string{"A=1", v.New + "=old", v.Legacy + "=old"}},
		{"an empty new name", []string{v.New + "=", v.Legacy + "=old"}, []string{v.New + "=", v.Legacy + "="}},
		{"the last entry wins", []string{v.New + "=first", v.New + "=last"}, []string{v.New + "=last", v.Legacy + "=last"}},
		{"neither", []string{"A=1"}, []string{"A=1"}},
	}
	useGeneration(t, "bridge")
	for _, tc := range cases {
		if got := Normalize(tc.env); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("bridge, %s: Normalize = %q, want %q", tc.name, got, tc.want)
		}
	}
	useGeneration(t, "legacy")
	for _, tc := range cases {
		if got := Normalize(tc.env); !reflect.DeepEqual(got, tc.env) {
			t.Errorf("legacy, %s: Normalize = %q, want it unchanged", tc.name, got)
		}
	}
	if NormalizePrefix() != "" {
		t.Errorf("legacy NormalizePrefix = %q, want none", NormalizePrefix())
	}
	if v.Name() != v.Legacy {
		t.Errorf("legacy Name() = %q, want %q", v.Name(), v.Legacy)
	}
}

// A pane's shell starts from the tmux server's environment: under bridge
// NormalizePrefix gives it this process's values under both names, and
// clears what this process has under neither.
func TestNormalizePrefixAndProcess(t *testing.T) {
	useGeneration(t, "bridge")
	for _, v := range All {
		t.Setenv(v.New, "")
		t.Setenv(v.Legacy, "")
		_ = os.Unsetenv(v.New)
		_ = os.Unsetenv(v.Legacy)
	}
	t.Setenv(SessionID.Legacy, "session-a")
	t.Setenv(RunID.New, "run-new")
	t.Setenv(RunID.Legacy, "run-old")
	t.Setenv(Backend.New, "")

	// The server's environment holds stale values of its own.
	server := []string{"PATH=" + os.Getenv("PATH"), Scratch.Legacy + "=stale", AgentBackend.New + "=stale"}
	var report []string
	for _, v := range All {
		report = append(report, `printf '%s=%s|%s\n' `+v.New+` "${`+v.New+`-unset}" "${`+v.Legacy+`-unset}"`)
	}
	got := childSees(t, server, NormalizePrefix()+strings.Join(report, "; "))
	want := map[string]string{
		SessionID.New:    "session-a|session-a",
		RunID.New:        "run-new|run-new",
		Backend.New:      "|",
		RunEvents.New:    "unset|unset",
		AgentBackend.New: "unset|unset",
		Scratch.New:      "unset|unset",
		TmuxSocket.New:   "unset|unset",
	}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		name, seen, _ := strings.Cut(line, "=")
		if seen != want[name] {
			t.Errorf("pane saw %s as %q, want %q", name, seen, want[name])
		}
	}

	NormalizeProcess()
	for _, v := range []Var{SessionID, RunID, Backend} {
		n, nok := os.LookupEnv(v.New)
		l, lok := os.LookupEnv(v.Legacy)
		if !nok || !lok || n != l {
			t.Errorf("after NormalizeProcess %s = %q (%v), %s = %q (%v)", v.New, n, nok, v.Legacy, l, lok)
		}
	}
	if _, ok := os.LookupEnv(Scratch.New); ok {
		t.Errorf("NormalizeProcess set %s, which was under neither name", Scratch.New)
	}
}
