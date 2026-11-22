package location

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestOnlyLocationSpellsStateDirs fails if production Go code outside this
// package spells a directory or lock file flow keeps state in, so every
// path to state goes through location. Prose that mentions one, as help
// text does, is allowed.
func TestOnlyLocationSpellsStateDirs(t *testing.T) {
	flowRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	self, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files := 0
	err = filepath.WalkDir(flowRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path == self || strings.HasPrefix(d.Name(), ".") {
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
		for _, s := range stateDirSpellings(fset, f) {
			rel, _ := filepath.Rel(flowRoot, path)
			t.Errorf("%s:%d: spells %s; find the path through location", rel, s.line, s.what)
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

func TestStateDirSpellings(t *testing.T) {
	src := `package sample
import "path/filepath"
func bad(home, gcd string) {
	_ = filepath.Join(home, ".klaus", "config.json")
	_ = filepath.Join(home, ".valley", "flow")
	_ = filepath.Join(gcd, "klaus-continue.lock")
	_ = ".klaus/prompt.md"
	_ = filepath.Join(gcd, "klaus", "runs")
	_ = filepath.Join(gcd, "valley", "flow", "runs")
}
func good(home string) {
	_ = "the host config, ~/.klaus/config.json, sets it"
	_ = "shorten .klaus/prompt.md"
	_ = filepath.Join(home, "bin", "klaus")
	_ = "klaus"
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "sample.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lines []int
	for _, s := range stateDirSpellings(fset, f) {
		lines = append(lines, s.line)
	}
	if want := []int{4, 5, 6, 7, 8, 9}; !reflect.DeepEqual(lines, want) {
		t.Errorf("found spellings on lines %v, want %v", lines, want)
	}
}

type spelling struct {
	line int
	what string
}

// stateDirSpellings finds where a file spells a state directory: a string
// literal that is one of the root or lock names or starts a relative path
// with one, or a filepath.Join that uses "klaus" or "valley", "flow" as
// directories.
func stateDirSpellings(fset *token.FileSet, f *ast.File) []spelling {
	names := map[string]bool{legacyHome: true, ".valley": true, "klaus-continue.lock": true, "continuation.lock": true}
	var found []spelling
	ast.Inspect(f, func(n ast.Node) bool {
		line := func() int { return fset.Position(n.Pos()).Line }
		switch e := n.(type) {
		case *ast.ImportSpec:
			return false
		case *ast.BasicLit:
			lit, ok := stringLit(e)
			if !ok {
				break
			}
			first, _, _ := strings.Cut(lit, "/")
			if names[lit] || first != lit && names[first] {
				found = append(found, spelling{line(), strconv.Quote(lit)})
			}
		case *ast.CallExpr:
			sel, ok := e.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Join" {
				break
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "filepath" {
				break
			}
			for i, arg := range e.Args {
				lit, _ := stringLit(arg)
				last := i == len(e.Args)-1
				next := ""
				if !last {
					next, _ = stringLit(e.Args[i+1])
				}
				if lit == "klaus" && !last && next != "klaus" || lit == "valley" && next == "flow" {
					found = append(found, spelling{line(), strconv.Quote(lit) + " as a directory"})
					return false
				}
			}
		}
		return true
	})
	return found
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}
