// knotcheck holds the Go validator of the plan graph to the CUE schema and
// the knowledge lint (the knot-agree check). It writes graphs, shared
// fixtures and generated ones, each with the verdict both validators must
// give, and it gives the Go validator's verdict on a graph on disk. The
// check runs the lint over the same graphs and fails on any disagreement.
//
//	knotcheck fixtures DIR            write the shared fixtures under DIR
//	knotcheck generate DIR N SEED     write N generated graphs under DIR
//	knotcheck validate DIR            the Go verdict on the graph at DIR
//	knotcheck rules                   the rules both validators share
//	knotcheck contract                the constants the lint must share
//
// fixtures and generate write each case as DIR/<case>/graph, the whole
// graph, and DIR/<case>/tree/knots, the same graph less its comments,
// which the lint does not read. Each adds a line to DIR/cases.tsv: the
// case, accept or reject, and for a rejection the rule it breaks.
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"the-valley/knot"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: knotcheck fixtures DIR | generate DIR N SEED | validate DIR | rules | contract")
		return 2
	}
	var err error
	switch {
	case args[0] == "fixtures" && len(args) == 2:
		err = write(args[1], Fixtures())
	case args[0] == "generate" && len(args) == 4:
		n, nerr := strconv.Atoi(args[2])
		seed, serr := strconv.ParseUint(args[3], 10, 64)
		if nerr != nil || serr != nil {
			fmt.Fprintln(os.Stderr, "knotcheck: generate takes a count and a seed")
			return 2
		}
		err = write(args[1], Generate(n, seed))
	case args[0] == "validate" && len(args) == 2:
		files, rerr := readDir(args[1])
		if rerr != nil {
			fmt.Fprintln(os.Stderr, "knotcheck:", rerr)
			return 2
		}
		if verr := validate(files); verr != nil {
			fmt.Printf("reject %s\n", ruled(verr))
			return 1
		}
		fmt.Println("accept")
		return 0
	case args[0] == "rules" && len(args) == 1:
		for _, r := range knot.SharedRules {
			fmt.Println(r)
		}
	case args[0] == "contract" && len(args) == 1:
		dirs := map[string]string{}
		for _, t := range knot.Types {
			dirs[t.Name] = t.Directory
		}
		err = json.NewEncoder(os.Stdout).Encode(map[string]any{"cutoff": knot.IDRuleCutoff, "directories": dirs})
	default:
		fmt.Fprintln(os.Stderr, "usage: knotcheck fixtures DIR | generate DIR N SEED | validate DIR | rules | contract")
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "knotcheck:", err)
		return 1
	}
	return 0
}

// Case is one graph, and the verdict both validators must give it.
type Case struct {
	Name   string
	Accept bool
	// Rule is the shared rule a rejected graph breaks.
	Rule knot.Rule
	// Files maps each file's path in the graph to its content.
	Files map[string]string
}

// validate is the Go validator's verdict on a graph's files: its layout,
// and then every knot and comment.
func validate(files map[string]file) error {
	var entries []knot.Entry
	dirs := map[string]bool{}
	for p, f := range files {
		entries = append(entries, knot.Entry{Path: p, Mode: f.mode, OID: p})
		for d := filepath.Dir(p); d != "."; d = filepath.Dir(d) {
			dirs[d] = true
		}
	}
	for d := range dirs {
		entries = append(entries, knot.Entry{Path: d, Mode: "40000"})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	idx, err := knot.Layout(entries)
	if err != nil {
		return err
	}
	read := func(oid string, max int) ([]byte, error) {
		data := files[oid].data
		if len(data) > max {
			return nil, fmt.Errorf("is %d bytes, more than %d", len(data), max)
		}
		return data, nil
	}
	return knot.Validate(idx, read)
}

func ruled(err error) error {
	if e, ok := err.(*knot.Error); ok {
		return fmt.Errorf("%s: %s", e.Rule, e.Error())
	}
	return err
}

type file struct {
	mode string
	data []byte
}

// readDir reads every file under dir, with the mode git would give it.
func readDir(dir string) (map[string]file, error) {
	files := map[string]file{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		mode := "100644"
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			mode = "120000"
		case info.Mode()&0o111 != 0:
			mode = "100755"
		}
		data, err := os.ReadFile(p)
		if err != nil && mode != "120000" {
			return err
		}
		files[filepath.ToSlash(rel)] = file{mode: mode, data: data}
		return nil
	})
	return files, err
}

// write lays every case out under dir, and lists them in dir/cases.tsv.
func write(dir string, cases []Case) error {
	var index strings.Builder
	for _, c := range cases {
		verdict, rule := "accept", ""
		if !c.Accept {
			verdict, rule = "reject", string(c.Rule)
		}
		fmt.Fprintf(&index, "%s\t%s\t%s\n", c.Name, verdict, rule)
		for p, content := range c.Files {
			targets := []string{filepath.Join(dir, c.Name, "graph", p)}
			if !strings.HasPrefix(p, knot.CommentsDirectory+"/") {
				targets = append(targets, filepath.Join(dir, c.Name, "tree", "knots", p))
			}
			for _, t := range targets {
				if err := os.MkdirAll(filepath.Dir(t), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(t, []byte(content), 0o644); err != nil {
					return err
				}
			}
		}
	}
	return os.WriteFile(filepath.Join(dir, "cases.tsv"), []byte(index.String()), 0o644)
}
