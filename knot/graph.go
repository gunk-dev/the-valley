package knot

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// Entry is one entry of a graph's tree, as a listing of it gives it: a
// path from the root, the entry's mode as git writes it, and its object.
// A listing holds every tree below the root as well as every file.
type Entry struct {
	Path string
	Mode string
	OID  string
}

// Index is a graph's layout: where each knot and each comment is, read
// from the tree's listing alone. A knot's ID and type are in its path, so
// an index resolves an edge without reading any knot.
type Index struct {
	// Knots maps each knot's ID to its file.
	Knots map[string]File
	// Comments maps each comment's path to its file.
	Comments map[string]File
	// Files maps every file's path to its object.
	Files map[string]string
	// knotAt maps each knot's path to its ID.
	knotAt map[string]string
}

// File is one knot's or comment's file in a graph.
type File struct {
	Path string
	OID  string
	// For a knot: its ID, type and slug. For a comment: the ID of the
	// knot it is on, its stamp and its operation.
	ID, Type, Slug string
	Stamp, Op      string
}

// Layout reads a graph's listing into its index, and refuses any listing
// that is not exactly a graph's layout: the root holds only the four type
// directories and comments/; a type directory holds only knots of its
// type, named <id>-<slug>.md; comments/ holds only a directory per knot,
// named by the knot's ID, holding only comments named <stamp>-<op>.md.
// Every directory has the mode 40000, exactly as git writes it, and every
// file is a plain file, mode 100644: no symlink, executable or submodule.
// Every directory a path passes through is an entry of the listing in its
// own right, as it is in any listing of a tree git wrote. No directory is
// empty, no name repeats, and no ID is used twice. The counts are held to
// MaxKnots and MaxComments. Whether each directory of comments is on a
// knot the graph holds is the push's to check, since a knot deleted is the
// clearer refusal.
func Layout(entries []Entry) (*Index, error) {
	idx, err := layout(entries)
	if err != nil {
		return nil, err
	}
	return idx, nil
}

func layout(entries []Entry) (*Index, *Error) {
	idx := &Index{Knots: map[string]File{}, Comments: map[string]File{}, Files: map[string]string{}, knotAt: map[string]string{}}
	seen := map[string]bool{}
	children := map[string]int{}
	isDir := map[string]bool{}
	var dirs []string
	for _, e := range entries {
		if seen[e.Path] {
			return nil, refuse(RuleLayout, e.Path, "is named twice in its tree")
		}
		seen[e.Path] = true
		dir, base := path.Split(e.Path)
		dir = strings.TrimSuffix(dir, "/")
		children[dir]++
		parts := strings.Split(e.Path, "/")
		if e.Mode == "40000" {
			dirs = append(dirs, e.Path)
			isDir[e.Path] = true
			if err := layoutDir(parts); err != nil {
				return nil, err
			}
			continue
		}
		if e.Mode != "100644" {
			return nil, refuse(RuleLayout, e.Path, fmt.Sprintf("has mode %s, and a graph holds only directories, mode 40000, and plain files, mode 100644: no symlink, executable or submodule", e.Mode))
		}
		f, err := layoutFile(parts, base)
		if err != nil {
			return nil, err
		}
		f.Path, f.OID = e.Path, e.OID
		idx.Files[e.Path] = e.OID
		if f.Stamp == "" {
			if prior, ok := idx.Knots[f.ID]; ok {
				return nil, refuse(RuleUniqueID, e.Path, fmt.Sprintf("has the id %s, which %s has already", f.ID, prior.Path))
			}
			idx.Knots[f.ID] = f
			idx.knotAt[e.Path] = f.ID
		} else {
			idx.Comments[e.Path] = f
		}
	}
	for _, d := range dirs {
		if children[d] == 0 {
			return nil, refuse(RuleLayout, d, "is an empty directory, which git does not write")
		}
	}
	for _, e := range entries {
		parts := strings.Split(e.Path, "/")
		for i := 1; i < len(parts); i++ {
			if d := strings.Join(parts[:i], "/"); !isDir[d] {
				return nil, refuse(RuleLayout, e.Path, fmt.Sprintf("is listed without the directory %s it is in, and a tree git writes names each directory as an entry of its own", d))
			}
		}
	}
	switch {
	case len(idx.Knots) > MaxKnots:
		return nil, refuse(RuleLimit, "", fmt.Sprintf("the graph would hold %d knots, more than the %d it may", len(idx.Knots), MaxKnots))
	case len(idx.Comments) > MaxComments:
		return nil, refuse(RuleLimit, "", fmt.Sprintf("the graph would hold %d comments, more than the %d it may", len(idx.Comments), MaxComments))
	}
	return idx, nil
}

func layoutDir(parts []string) *Error {
	p := strings.Join(parts, "/")
	switch len(parts) {
	case 1:
		if _, ok := typeOfDirectory(parts[0]); ok || parts[0] == CommentsDirectory {
			return nil
		}
		return refuse(RuleLayout, p, fmt.Sprintf("is no directory a graph holds; the root holds %s", strings.Join(rootNames(), ", ")))
	case 2:
		if parts[0] == CommentsDirectory && IsLocalID(parts[1]) {
			return nil
		}
		if parts[0] == CommentsDirectory {
			return refuse(RuleLayout, p, "is not named by a knot's id, as each directory of comments is")
		}
	}
	return refuse(RuleLayout, p, "is a directory where a graph holds files")
}

func layoutFile(parts []string, base string) (File, *Error) {
	p := strings.Join(parts, "/")
	switch {
	case len(parts) == 2 && parts[0] != CommentsDirectory:
		t, ok := typeOfDirectory(parts[0])
		if !ok {
			break
		}
		m := knotFile.FindStringSubmatch(base)
		if m == nil {
			return File{}, refuse(RuleLayout, p, fmt.Sprintf("is not named <id>-<slug>.md, as a knot is, with the id %s- and 7 hex digits and the slug lowercase letters, digits and hyphens", t.Prefix))
		}
		id, slug := m[1], m[3]
		if !slugShape.MatchString(slug) || len(slug) > MaxSlug {
			return File{}, refuse(RuleLayout, p, fmt.Sprintf("has the slug %q, and a slug is at most %d lowercase letters and digits, in words joined by single hyphens", slug, MaxSlug))
		}
		if pt, _ := typeOfPrefix(m[2]); pt.Name != t.Name {
			return File{}, refuse(RuleDirectory, p, fmt.Sprintf("is a%s %s, and a%s %s belongs in %s/", article(pt.Name), pt.Name, article(pt.Name), pt.Name, pt.Directory))
		}
		return File{ID: id, Type: t.Name, Slug: slug}, nil
	case len(parts) == 3 && parts[0] == CommentsDirectory:
		m := commentFile.FindStringSubmatch(base)
		if m == nil {
			return File{}, refuse(RuleLayout, p, "is not named <stamp>-<op>.md, as a comment is, with the stamp YYYYMMDDTHHMMSSZ and the op 16 hex digits")
		}
		if _, err := time.Parse(CommentStamp, m[1]); err != nil {
			return File{}, refuse(RuleLayout, p, fmt.Sprintf("is stamped %s, which is no time: a comment's stamp is when it was written, YYYYMMDDTHHMMSSZ", m[1]))
		}
		return File{ID: parts[1], Stamp: m[1], Op: m[2]}, nil
	}
	return File{}, refuse(RuleLayout, p, fmt.Sprintf("is outside the graph's layout: a graph holds %s/<id>-<slug>.md and %s/<id>/<stamp>-<op>.md, and nothing else", strings.Join(typeDirectories(), "|"), CommentsDirectory))
}

func rootNames() []string { return append(typeDirectories(), CommentsDirectory) }

func typeDirectories() []string {
	var names []string
	for _, t := range Types {
		names = append(names, t.Directory)
	}
	return names
}

// Reader reads a file's content by its object, refusing one larger than
// max bytes before reading it.
type Reader func(oid string, max int) ([]byte, error)

// Push is one write of a graph to judge: the graph before it, which is nil
// for the push that creates the ref, and the graph it would leave.
type Push struct {
	Old, New *Index
	Read     Reader
	// Pusher is the principal pushing. A new knot's created_by and a new
	// comment's author must name it.
	Pusher string
}

// Check holds a push to every rule. The graph before it was valid, so only
// what changed is read: each knot and comment the push adds or changes,
// and, for cycles, the knots their edges reach. The rules about the whole
// graph are checked on every push all the same: the counts from the
// listing, and cycles by walking the edges from every changed knot.
func (p Push) Check() error {
	if err := p.check(true); err != nil {
		return err
	}
	return nil
}

// Validate holds a whole graph to every rule but the push's: it reads
// every knot and comment, and needs no pusher.
func Validate(idx *Index, read Reader) error {
	if err := (Push{New: idx, Read: read}).check(false); err != nil {
		return err
	}
	return nil
}

func (p Push) check(push bool) *Error {
	g := &graph{idx: p.New, read: p.Read, knots: map[string]*Knot{}}
	var changed []string
	for path, oid := range p.New.Files {
		if p.Old == nil || p.Old.Files[path] != oid {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	if p.Old != nil {
		var gone []string
		for path := range p.Old.Files {
			if _, ok := p.New.Files[path]; !ok {
				gone = append(gone, path)
			}
		}
		sort.Strings(gone)
		for _, path := range gone {
			what := "a knot is never deleted"
			if _, ok := p.Old.Comments[path]; ok {
				what = "a comment is never deleted"
			}
			return refuse(RuleTransition, path, "is deleted, and "+what+"; a knot that is done is closed by its status, and the history is the record")
		}
	}
	if push && len(changed) > MaxChangedPaths {
		return refuse(RuleLimit, "", fmt.Sprintf("the push adds or changes %d files, more than the %d one commit may", len(changed), MaxChangedPaths))
	}
	var touched []string
	for _, path := range changed {
		if c, ok := p.New.Comments[path]; ok {
			if err := p.checkComment(g, c); err != nil {
				return err
			}
			continue
		}
		f := p.New.Knots[p.New.knotAt[path]]
		k, err := g.knot(f.ID)
		if err != nil {
			return err
		}
		if err := g.checkEdges(k); err != nil {
			return err
		}
		if err := p.checkTransition(f, k); err != nil {
			return err
		}
		touched = append(touched, f.ID)
	}
	return g.checkCycles(touched)
}

// checkComment holds a comment the push adds to its rules. A comment is
// only ever added: one whose path the old graph has already is a change to
// it, and is refused.
func (p Push) checkComment(g *graph, c File) *Error {
	if p.Old != nil {
		if _, ok := p.Old.Files[c.Path]; ok {
			return refuse(RuleTransition, c.Path, "changes a comment, and a comment stays exactly as it was written")
		}
	}
	// A comment the push leaves unchanged is on a knot the graph holds,
	// since no knot is deleted. One it adds must be too.
	if _, ok := p.New.Knots[c.ID]; !ok {
		return refuse(RuleEdgeResolves, path.Dir(c.Path), fmt.Sprintf("holds comments on %s, which is no knot in the graph", c.ID))
	}
	data, err := g.read(c.OID, MaxCommentSize)
	if err != nil {
		return refuse(RuleLimit, c.Path, err.Error())
	}
	comment, perr := parseComment(c.Path, data)
	if perr != nil {
		return perr
	}
	if p.Pusher != "" && comment.Author != p.Pusher {
		return refuse(RuleProvenance, c.Path, fmt.Sprintf("names %s as its author, and %s is pushing it: a comment's author is the principal that writes it", comment.Author, p.Pusher))
	}
	return nil
}

// checkTransition holds a knot the push adds or changes to what a push
// may do to one: a new knot names the pusher as its creator, and a changed
// one keeps its id, type, created date and creator.
func (p Push) checkTransition(f File, k *Knot) *Error {
	var before File
	if p.Old != nil {
		before = p.Old.Knots[f.ID]
	}
	if before.Path == "" {
		if p.Pusher != "" && k.CreatedBy != p.Pusher {
			return refuse(RuleProvenance, f.Path, fmt.Sprintf("names %s in created_by, and %s is pushing it: a new knot's creator is the principal that pushes it", k.CreatedBy, p.Pusher))
		}
		return nil
	}
	if before.Path != f.Path {
		return refuse(RuleTransition, f.Path, fmt.Sprintf("moves %s from %s, and a knot's file never moves", f.ID, before.Path))
	}
	data, err := p.Read(before.OID, MaxKnotSize)
	if err != nil {
		return refuse(RuleLimit, before.Path, "as it was: "+err.Error())
	}
	old, perr := parseKnot(before.Path, before.Slug, data)
	if perr != nil {
		return refuse(perr.Rule, before.Path, "as it was: "+perr.Msg)
	}
	for _, field := range []string{"id", "type", "created", "created_by"} {
		if old.Get(field) != k.Get(field) {
			return refuse(RuleTransition, f.Path, fmt.Sprintf("changes %s from %q to %q, and a knot's id, type, created and created_by never change", field, old.Get(field), k.Get(field)))
		}
	}
	return nil
}

// graph reads the knots of one index on demand, each once.
type graph struct {
	idx   *Index
	read  Reader
	knots map[string]*Knot
}

// knot is the knot with id, read and held to every rule about it alone,
// and to its filename.
func (g *graph) knot(id string) (*Knot, *Error) {
	if k, ok := g.knots[id]; ok {
		return k, nil
	}
	f := g.idx.Knots[id]
	data, err := g.read(f.OID, MaxKnotSize)
	if err != nil {
		return nil, refuse(RuleLimit, f.Path, err.Error())
	}
	k, perr := parseKnot(f.Path, f.Slug, data)
	if perr != nil {
		return nil, perr
	}
	switch {
	case k.ID != f.ID:
		return nil, refuse(RuleFilename, f.Path, fmt.Sprintf("its filename says id %s, and its frontmatter says %s", f.ID, k.ID))
	case k.Type != f.Type:
		t, _ := TypeNamed(k.Type)
		return nil, refuse(RuleDirectory, f.Path, fmt.Sprintf("is a%s %s, and a%s %s belongs in %s/", article(k.Type), k.Type, article(k.Type), k.Type, t.Directory))
	}
	if derived := DeriveID(k.TypeOf(), f.Slug); derived != k.ID {
		return nil, refuse(RuleIDDerived, f.Path, fmt.Sprintf("its id %s is not derived from its slug %q, which gives %s", k.ID, f.Slug, derived))
	}
	g.knots[id] = k
	return k, nil
}

// checkEdges holds every local edge of k to naming a knot in the graph,
// of the type the edge takes. A reference to another project's knot is
// held to its shape alone, which the knot's own check has done.
func (g *graph) checkEdges(k *Knot) *Error {
	resolve := func(field, id, want string) *Error {
		f, ok := g.idx.Knots[id]
		switch {
		case !ok:
			return refuse(RuleEdgeResolves, k.Path(), fmt.Sprintf("%s names %s, which is no knot in the graph", field, id))
		case want != "" && f.Type != want:
			return refuse(RuleParentOutcome, k.Path(), fmt.Sprintf("%s names %s, a %s, and it names a%s %s", field, id, f.Type, article(want), want))
		}
		return nil
	}
	if k.Parent != "" {
		if err := resolve("parent", k.Parent, "outcome"); err != nil {
			return err
		}
	}
	if k.GraduatedInto != "" {
		if err := resolve("graduated_into", k.GraduatedInto, "decision"); err != nil {
			return err
		}
	}
	for _, ref := range k.BlockedBy {
		if IsLocalID(ref) {
			if err := resolve("blocked_by", ref, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkCycles refuses a cycle in parent edges, or in blocked_by edges,
// through any knot in from. The graph before the push had none, so every
// cycle the push could make runs through a knot it changed. Each knot
// reached is read once.
//
// The two edges are checked apart. A milestone may wait on one of its own
// work items, and that is no cycle: the work item can finish.
func (g *graph) checkCycles(from []string) *Error {
	for _, edge := range []string{"parent", "blocked_by"} {
		const (
			visiting = 1
			done     = 2
		)
		state := map[string]int{}
		var trail []string
		var visit func(id string) *Error
		visit = func(id string) *Error {
			switch state[id] {
			case visiting:
				at := 0
				for trail[at] != id {
					at++
				}
				loop := append(append([]string(nil), trail[at:]...), id)
				k := g.knots[id]
				return refuse(RuleCycle, k.Path(), fmt.Sprintf("its %s edges form a cycle, %s", edge, strings.Join(loop, " → ")))
			case done:
				return nil
			}
			k, err := g.knot(id)
			if err != nil {
				return err
			}
			state[id] = visiting
			trail = append(trail, id)
			var next []string
			if edge == "parent" && k.Parent != "" {
				next = []string{k.Parent}
			} else if edge == "blocked_by" {
				for _, ref := range k.BlockedBy {
					if _, ok := g.idx.Knots[ref]; ok {
						next = append(next, ref)
					}
				}
			}
			for _, n := range next {
				if err := visit(n); err != nil {
					return err
				}
			}
			trail = trail[:len(trail)-1]
			state[id] = done
			return nil
		}
		for _, id := range from {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}
