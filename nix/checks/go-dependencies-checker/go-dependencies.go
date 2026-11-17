// Command go-dependencies holds the Go dependency boundary of the tree
// whose root it is given (AGENTS.md, "Go dependencies"). Every Go module
// imports only the standard library and the tree's own modules, except the
// module rooted at flow/, which may import third-party modules, and which
// no other module imports or replaces into.
//
// It reads every .go file's imports whatever its build constraints, test
// files included, and every go.mod as `go mod edit -json` reads it. It asks
// the go command on PATH, the pinned toolchain, which imported paths are
// standard packages. It builds nothing and fetches nothing. What it cannot
// read, it refuses: a workspace file, a vendor directory, a go.mod the go
// command cannot parse, a Go file it cannot parse, a symlink that leaves
// the tree, and a symlink named like a Go file or go.mod that it cannot
// read through.
//
// Each refusal names the file and the import or directive; any refusal
// exits 1.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The one module that may import third-party modules: its directory,
// relative to the root, and the path it declares.
const (
	flowDir    = "flow"
	flowModule = "the-valley/flow"
)

type refusal struct {
	File    string // slash-separated, relative to the root
	Line    int    // the import's line, for an import
	Subject string // the import path or go.mod directive, when there is one
	Reason  string
}

func (r refusal) String() string {
	s := r.File
	if r.Line > 0 {
		s += ":" + strconv.Itoa(r.Line)
	}
	if r.Subject != "" {
		s += ": " + r.Subject
	}
	return s + ": " + r.Reason
}

type module struct {
	dir     string // logical, slash-separated, relative to the root
	physDir string // where it is on disk, with every symlink resolved
	gomod   string // its go.mod, logical
	path    string // the path it declares; empty when go.mod is unreadable
	mod     goMod
}

// goMod is the part of `go mod edit -json`'s output the check reads.
type goMod struct {
	Module  struct{ Path string }
	Require []struct{ Path string }
	Replace []struct {
		Old, New struct{ Path, Version string }
	}
	Tool []struct{ Path string }
}

type goFile struct {
	file    string
	module  *module // nil for a file in no module
	imports []goImport
}

type goImport struct {
	path string
	line int
}

type tree struct {
	realRoot string
	goBin    string
	env      []string
	tmp      string
	modules  []*module
	files    []*goFile
	byPath   map[string]*module // modules whose declared path is accepted
	std      map[string]bool
	refused  []refusal
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go-dependencies ROOT")
		os.Exit(2)
	}
	refusals, modules, files, err := check(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "go-dependencies:", err)
		os.Exit(2)
	}
	for _, r := range refusals {
		fmt.Println(r)
	}
	if len(refusals) > 0 {
		fmt.Printf("go-dependencies: %d refused\n", len(refusals))
		os.Exit(1)
	}
	fmt.Printf("go-dependencies: %d modules and %d Go files keep to the boundary\n", modules, files)
}

// check judges the tree at root. An error means it could not judge it at
// all.
func check(root string) ([]refusal, int, int, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, 0, 0, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, 0, 0, err
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return nil, 0, 0, err
	}
	// The go command runs in an empty directory, so no go.mod or go.work
	// around it changes what it reads, with its own cache, and with every
	// setting that could switch toolchains or reach the network pinned.
	tmp, err := os.MkdirTemp("", "go-dependencies")
	if err != nil {
		return nil, 0, 0, err
	}
	defer os.RemoveAll(tmp)
	t := &tree{
		realRoot: real,
		goBin:    goBin,
		tmp:      tmp,
		env: append(os.Environ(),
			"GOENV=off", "GOFLAGS=", "GOTOOLCHAIN=local", "GOWORK=off",
			"GOPROXY=off", "GO111MODULE=on", "GOCACHE="+filepath.Join(tmp, "cache")),
		byPath: map[string]*module{},
	}
	t.walk(".", real, nil, nil)
	t.readModules()
	if err := t.readStd(); err != nil {
		return nil, 0, 0, err
	}
	for _, m := range t.modules {
		if m.path != "" && m.dir != flowDir {
			t.judgeGoMod(m)
		}
	}
	for _, f := range t.files {
		if f.module != nil && f.module.dir == flowDir {
			continue
		}
		for _, imp := range f.imports {
			if reason := t.judge(imp.path); reason != "" {
				t.refused = append(t.refused, refusal{f.file, imp.line, strconv.Quote(imp.path), reason})
			}
		}
	}
	sort.Slice(t.refused, func(i, j int) bool {
		a, b := t.refused[i], t.refused[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Subject < b.Subject
	})
	return t.refused, len(t.modules), len(t.files), nil
}

func (t *tree) refuse(file, subject, reason string) {
	t.refused = append(t.refused, refusal{File: file, Subject: subject, Reason: reason})
}

func child(logical, name string) string {
	if logical == "." {
		return name
	}
	return logical + "/" + name
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

func inFlow(dir string) bool {
	return dir == flowDir || strings.HasPrefix(dir, flowDir+"/")
}

func inNamespace(p, ns string) bool {
	return p == ns || strings.HasPrefix(p, ns+"/")
}

// walk reads the directory at phys, which the tree holds at logical, in
// the module mod. ancestors are the directories, resolved, that the walk
// descended through to reach it.
func (t *tree) walk(logical, phys string, mod *module, ancestors []string) {
	entries, err := os.ReadDir(phys)
	if err != nil {
		t.refuse(logical, "", "cannot be read: "+err.Error())
		return
	}
	type entry struct {
		name, logical, phys string
		info                os.FileInfo
	}
	var resolved []entry
	for _, e := range entries {
		name := e.Name()
		if name == ".git" {
			continue
		}
		l, p := child(logical, name), filepath.Join(phys, name)
		info, err := os.Lstat(p)
		if err != nil {
			t.refuse(l, "", "cannot be read: "+err.Error())
			continue
		}
		link := info.Mode()&os.ModeSymlink != 0
		switch {
		case name == "go.work":
			t.refuse(l, "", "a workspace changes how every module beneath it builds, so the check refuses one")
			continue
		case name == "vendor" && (info.IsDir() || link):
			t.refuse(l, "", "a vendor directory supplies packages the check does not read, so it refuses one")
			continue
		}
		if link {
			var ok bool
			if p, info, ok = t.follow(l, p); !ok {
				continue
			}
		}
		resolved = append(resolved, entry{name, l, p, info})
	}
	// A go.mod makes this directory a module's root, and every file below
	// it, down to the next go.mod, part of that module.
	for _, e := range resolved {
		if e.name == "go.mod" && e.info.Mode().IsRegular() {
			mod = &module{dir: logical, physDir: phys, gomod: e.logical}
			t.modules = append(t.modules, mod)
		}
	}
	for _, e := range resolved {
		switch {
		case e.info.IsDir():
			chain := append(append([]string(nil), ancestors...), phys)
			if slices.Contains(chain, e.phys) {
				t.refuse(e.logical, "", "a symlink loops back to a directory above it")
				continue
			}
			t.walk(e.logical, e.phys, mod, chain)
		case e.info.Mode().IsRegular() && strings.HasSuffix(e.name, ".go"):
			t.parse(e.logical, e.phys, mod)
		}
	}
}

// follow resolves the symlink at phys, which the tree holds at logical.
// A file or directory it names inside the tree is read as if it were at
// logical, which is where Go reads it.
func (t *tree) follow(logical, phys string) (string, os.FileInfo, bool) {
	target, err := os.Readlink(phys)
	if err != nil {
		t.refuse(logical, "", "cannot be read: "+err.Error())
		return "", nil, false
	}
	if filepath.IsAbs(target) || !within(t.realRoot, filepath.Join(filepath.Dir(phys), target)) {
		t.refuse(logical, target, "a symlink leaves the repository")
		return "", nil, false
	}
	// The check's source holds the tree's .go files and go.mod files and
	// none of its other files, so a link giving one of those names to a
	// file of another name is a link the check cannot read through.
	name, targetName := path.Base(logical), filepath.Base(target)
	goName := strings.HasSuffix(name, ".go") || name == "go.mod"
	if strings.HasSuffix(name, ".go") && !strings.HasSuffix(targetName, ".go") ||
		name == "go.mod" && targetName != "go.mod" {
		t.refuse(logical, target, "a symlink gives a Go file's name to a file of another name, which the check does not read")
		return "", nil, false
	}
	real, err := filepath.EvalSymlinks(phys)
	if err != nil {
		// A link of any other name that resolves to nothing here names no
		// Go source, since every Go file is in the tree.
		if goName {
			t.refuse(logical, target, "a symlink resolves to no file in the tree the check reads")
		}
		return "", nil, false
	}
	if !within(t.realRoot, real) {
		t.refuse(logical, target, "a symlink leaves the repository")
		return "", nil, false
	}
	info, err := os.Stat(real)
	if err != nil {
		t.refuse(logical, target, "cannot be read: "+err.Error())
		return "", nil, false
	}
	return real, info, true
}

func (t *tree) parse(logical, phys string, mod *module) {
	src, err := os.ReadFile(phys)
	if err != nil {
		t.refuse(logical, "", "cannot be read: "+err.Error())
		return
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, logical, src, parser.ImportsOnly)
	if err != nil {
		t.refuse(logical, "", "its imports cannot be parsed: "+err.Error())
		return
	}
	gf := &goFile{file: logical, module: mod}
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.refuse(logical, spec.Path.Value, "not an import path Go accepts")
			continue
		}
		gf.imports = append(gf.imports, goImport{p, fset.Position(spec.Pos()).Line})
	}
	t.files = append(t.files, gf)
}

// readModules reads every go.mod with the go command, and holds each
// declared path to the boundary before any module may be named by it.
func (t *tree) readModules() {
	declared := map[string]*module{}
	for _, m := range t.modules {
		cmd := exec.Command(t.goBin, "mod", "edit", "-json", filepath.Join(m.physDir, "go.mod"))
		cmd.Dir, cmd.Env = t.tmp, t.env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err == nil {
			err = json.Unmarshal(out, &m.mod)
		}
		if err == nil && m.mod.Module.Path == "" {
			err = errors.New("it declares no module path")
		}
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			t.refuse(m.gomod, "", "go mod edit cannot read it: "+msg)
			continue
		}
		m.path = m.mod.Module.Path
		subject := "module " + m.path
		switch {
		case m.dir == flowDir && m.path != flowModule:
			t.refuse(m.gomod, subject, "flow/ declares "+flowModule)
		case !inFlow(m.dir) && inNamespace(m.path, flowModule):
			t.refuse(m.gomod, subject, "only flow/ declares "+flowModule)
		case !inFlow(m.dir) && strings.Contains(strings.SplitN(m.path, "/", 2)[0], "."):
			// A first element with a dot names a host the go command
			// fetches from, so such a module would stand in for a
			// third-party one.
			t.refuse(m.gomod, subject, "a module outside flow/ declares a path that cannot name a third-party module")
		case declared[m.path] != nil:
			t.refuse(m.gomod, subject, declared[m.path].gomod+" declares it too")
		default:
			declared[m.path] = m
			t.byPath[m.path] = m
		}
	}
}

// readStd asks the toolchain which of the imported paths are standard
// packages. go list reports a standard package as one even where build
// constraints exclude every file of it for this target, so a package that
// exists only for another target counts. The command's packages under
// cmd/ are no part of the standard library, as go list std has it.
func (t *tree) readStd() error {
	t.std = map[string]bool{}
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		if !seen[p] && validImportPath(p) {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for _, f := range t.files {
		for _, imp := range f.imports {
			add(imp.path)
		}
	}
	for _, m := range t.modules {
		for _, tool := range m.mod.Tool {
			add(tool.Path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	cmd := exec.Command(t.goBin, append([]string{"list", "-e", "-json=ImportPath,Standard", "--"}, paths...)...)
	cmd.Dir, cmd.Env = t.tmp, t.env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("go list: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p struct {
			ImportPath string
			Standard   bool
		}
		if err := dec.Decode(&p); err == io.EOF {
			return nil
		} else if err != nil {
			return fmt.Errorf("go list: %v", err)
		}
		if p.Standard && !inNamespace(p.ImportPath, "cmd") {
			t.std[p.ImportPath] = true
		}
	}
}

// validImportPath reports whether p is an import path by the go command's
// rules (golang.org/x/mod/module.CheckImportPath), and neither a package
// pattern nor a name the go command reads as a package pattern, which go
// list would expand instead of looking up.
func validImportPath(p string) bool {
	switch p {
	case "", "main", "all", "std", "cmd", "tool", "work":
		return false
	}
	if p[0] == '-' || strings.Contains(p, "...") {
		return false
	}
	for _, elem := range strings.Split(p, "/") {
		if strings.Trim(elem, ".") == "" || strings.HasSuffix(elem, ".") {
			return false
		}
		for _, c := range elem {
			if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.ContainsRune("-._~+", c)) {
				return false
			}
		}
		short, _, _ := strings.Cut(elem, ".")
		if windowsReserved(short) {
			return false
		}
		if i := strings.LastIndexByte(short, '~'); i >= 0 && i < len(short)-1 && strings.Trim(short[i+1:], "0123456789") == "" {
			return false
		}
	}
	return true
}

// windowsReserved reports whether a path element, up to its first dot, is
// a device name Windows reserves, which Go refuses in an import path.
func windowsReserved(short string) bool {
	switch strings.ToUpper(short) {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(short) == 4 && short[3] >= '1' && short[3] <= '9' {
		prefix := strings.ToUpper(short[:3])
		return prefix == "COM" || prefix == "LPT"
	}
	return false
}

func (t *tree) owner(p string) *module {
	var best *module
	for mp, m := range t.byPath {
		if inNamespace(p, mp) && (best == nil || len(mp) > len(best.path)) {
			best = m
		}
	}
	return best
}

// judge returns why a module outside flow/ may not import p, or "".
func (t *tree) judge(p string) string {
	switch {
	case p == "C": // cgo's pseudo-package
		return ""
	case !validImportPath(p):
		return "not an import path Go accepts, or a package pattern"
	case inNamespace(p, flowModule):
		return "no module outside flow/ imports from it"
	case t.std[p]:
		return ""
	}
	if m := t.owner(p); m != nil {
		if inFlow(m.dir) {
			return "no module outside flow/ imports from it"
		}
		return ""
	}
	return "neither the standard library nor a module of this repository"
}

// judgeGoMod holds the go.mod of a module outside flow/ to the boundary.
func (t *tree) judgeGoMod(m *module) {
	for _, r := range m.mod.Require {
		subject := "require " + r.Path
		owner := t.byPath[r.Path]
		switch {
		case inNamespace(r.Path, flowModule) || owner != nil && inFlow(owner.dir):
			t.refuse(m.gomod, subject, "no module outside flow/ requires a module in it")
		case owner == nil:
			t.refuse(m.gomod, subject, "not a module of this repository")
		}
	}
	for _, r := range m.mod.Replace {
		subject := "replace " + r.Old.Path
		if r.Old.Version != "" {
			subject += " " + r.Old.Version
		}
		subject += " => " + r.New.Path
		if r.New.Version != "" {
			subject += " " + r.New.Version
		}
		if reason := t.judgeReplace(m, r.Old.Path, r.New.Path, r.New.Version); reason != "" {
			t.refuse(m.gomod, subject, reason)
		}
	}
	for _, tool := range m.mod.Tool {
		if reason := t.judge(tool.Path); reason != "" {
			t.refuse(m.gomod, "tool "+tool.Path, reason)
		}
	}
}

func (t *tree) judgeReplace(m *module, old, dir, version string) string {
	const outside = "the replacement is outside the repository"
	const intoFlow = "no module outside flow/ replaces into it"
	if version != "" || filepath.IsAbs(dir) {
		return outside
	}
	logical := path.Join(m.dir, filepath.ToSlash(dir))
	if logical == ".." || strings.HasPrefix(logical, "../") {
		return outside
	}
	if inFlow(logical) {
		return intoFlow
	}
	real, err := filepath.EvalSymlinks(filepath.Join(m.physDir, dir))
	if err != nil {
		return "the replacement is no module of this repository"
	}
	if !within(t.realRoot, real) {
		return "the replacement resolves through a symlink to outside the repository"
	}
	if within(filepath.Join(t.realRoot, flowDir), real) {
		return intoFlow
	}
	var target *module
	for _, other := range t.modules {
		if other.physDir == real {
			target = other
			break
		}
	}
	switch {
	case target == nil || target.path == "":
		return "the replacement is no module of this repository"
	case inFlow(target.dir):
		return intoFlow
	case target.path != old || t.byPath[old] != target:
		return "the replacement declares " + target.path + ", so it would stand in for another module"
	}
	return ""
}
