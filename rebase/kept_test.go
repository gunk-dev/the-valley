package rebase

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"the-valley/change"
)

// commitFiles writes each file on the current branch and commits them
// together.
func (w *world) commitFiles(subject string, files map[string]string) string {
	w.t.Helper()
	for name, content := range files {
		path := filepath.Join(w.work, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			w.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			w.t.Fatal(err)
		}
	}
	w.git("add", "-A")
	w.git("commit", "--quiet", "-m", subject)
	return w.git("rev-parse", "HEAD")
}

// A conflict keeps every path that conflicted, more than a report holds,
// whatever bytes their names hold: a newline, a control character, bytes
// that are not UTF-8, and a path longer than a report allows.
func TestEveryConflictingPathIsKept(t *testing.T) {
	w := newWorld(t)
	long := strings.Repeat("d", 200) + "/" + strings.Repeat("e", 200) + "/" + strings.Repeat("f", 200) + "/" +
		strings.Repeat("g", 200) + "/" + strings.Repeat("h", 200) + "/" + strings.Repeat("i", 100) + ".txt"
	names := []string{"line\nbreak.txt", "bell\x07.txt", "latin1-\xe9.txt", long}
	for i := 0; i < 130; i++ {
		names = append(names, filepath.Join("many", strings.Repeat("p", i%7+1)+"-"+string(rune('a'+i%26))+"-"+strings.Repeat("q", i/26+1)+".txt"))
	}
	files := func(content string) map[string]string {
		m := map[string]string{}
		for _, n := range names {
			m[n] = content
		}
		return m
	}
	w.git("checkout", "--quiet", "-b", "a", "main")
	w.commitFiles("layer a", files("a\n"))
	w.git("push", "--quiet", "origin", "a")
	w.ready(idA, "a")
	w.git("checkout", "--quiet", "main")
	w.commitFiles("main has its own", files("main\n"))
	w.git("push", "--quiet", "origin", "main")
	before := w.refs()

	_, err := Prepare(context.Background(), w.g, w.capture(Selection{ID: idA}))
	var r *Refusal
	if !errors.As(err, &r) || r.Kind != Conflict {
		t.Fatalf("prepare: %v", err)
	}
	if len(names) <= change.MaxConflictPaths {
		t.Fatalf("the test conflicts on %d paths, no more than a report holds", len(names))
	}
	got := append([]string(nil), r.Paths...)
	want := append([]string(nil), names...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the conflict names %d paths, not the %d that conflicted", len(got), len(want))
		for i := 0; i < len(got) && i < len(want); i++ {
			if got[i] != want[i] {
				t.Errorf("first difference: %q, where %q conflicted", got[i], want[i])
				break
			}
		}
	}
	if after := w.refs(); after != before {
		t.Errorf("a conflict moved something")
	}
}

// Each change's version is built before any branch moves, from the record
// the capture read: origin is untouched, each version keeps its active
// title and is leased on the record read, and every write restores from
// the form it is kept in. Restored, a version's push is refused while its
// branch has not moved. Once the restored branch push moves every branch
// at once, each restored version's push adds the version.
func TestEveryWriteIsBuiltBeforeAnyPush(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.layer("b", "a")
	w.readyTitled(idA, "a", "A's own title")
	w.readyTitled(idB, "b", "B's own title")
	w.moveMain("main.txt", "main\n")
	ctx := context.Background()
	f := w.capture(Selection{ID: idB})
	before := w.refs()

	p, err := Prepare(ctx, w.g, f)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release(ctx, w.g)
	versions, err := p.BuildAppends(ctx, w.g, "restacker")
	if err != nil {
		t.Fatal(err)
	}
	if after := w.refs(); after != before {
		t.Fatalf("building the writes moved something:\n%s\nnow:\n%s", before, after)
	}
	if len(versions) != 2 || versions[0].ID != idA || versions[1].ID != idB {
		t.Fatalf("the versions built are %+v", versions)
	}
	for i, v := range versions {
		l := f.Layers[i]
		active := v.Record.ActiveVersion()
		if v.Old != l.Commit || v.Head != p.Heads[l] || v.Version != 2 || !v.Restack() ||
			active.Title != l.Record.ActiveVersion().Title || active.Uploader != "restacker" || active.Branch != l.Branch {
			t.Errorf("the version of %s is %+v, recording %+v", l.Name, v, active)
		}
	}

	kept, err := json.Marshal(struct {
		Branches *PendingBranches
		Versions []*PendingVersion
	}{p.Branches(), versions})
	if err != nil {
		t.Fatal(err)
	}
	var restored struct {
		Branches *PendingBranches
		Versions []*PendingVersion
	}
	if err := json.Unmarshal(kept, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.Branches, p.Branches()) || !reflect.DeepEqual(restored.Versions, versions) {
		t.Fatalf("the writes restore as %+v; they were kept as %s", restored, kept)
	}
	for _, u := range restored.Branches.Updates {
		if w.at("refs/heads/"+u.Branch) != u.Old {
			t.Errorf("the kept push leases %s on %s, and origin's is at %s", u.Branch, u.Old, w.at("refs/heads/"+u.Branch))
		}
	}

	if ap := restored.Versions[0].Push(ctx, w.g); ap.Err == nil || w.refs() != before {
		t.Fatalf("a version pushed before its branch moved: %+v", ap)
	}
	if push, refused := restored.Branches.Push(ctx, w.g); push != Pushed {
		t.Fatalf("the restored branch push: %v %+v", push, refused)
	}
	for _, u := range restored.Branches.Updates {
		if w.at("refs/heads/"+u.Branch) != u.New {
			t.Errorf("origin's %s is at %s, not %s", u.Branch, w.at("refs/heads/"+u.Branch), u.New)
		}
	}
	for _, v := range restored.Versions {
		if ap := v.Push(ctx, w.g); ap.Err != nil || ap.Version != 2 || w.at(v.Ref) != v.Commit {
			t.Fatalf("the restored version of %s: %+v", v.Handle, ap)
		}
	}
	if a, b := w.record(idA).ActiveVersion(), w.record(idB).ActiveVersion(); a.Title != "A's own title" || b.Title != "B's own title" {
		t.Errorf("the versions on origin are titled %q and %q", a.Title, b.Title)
	}
}

// A kept branch push restores only from its own form, whole and agreeing
// with itself.
func TestBranchPushRestoresOnlyItsForm(t *testing.T) {
	b := PendingBranches{Tip: strings.Repeat("1", 40), Updates: []BranchUpdate{
		{Branch: "a", Old: strings.Repeat("2", 40), New: strings.Repeat("3", 40)},
		{Branch: "b", Old: strings.Repeat("4", 40), New: strings.Repeat("5", 40)},
	}}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var again PendingBranches
	if err := json.Unmarshal(data, &again); err != nil || !reflect.DeepEqual(again, b) {
		t.Fatalf("%s restores as %+v, %v", data, again, err)
	}
	kept := string(data)
	for what, form := range map[string]string{
		"no update":         strings.Replace(kept, kept[strings.Index(kept, `"updates":`):], `"updates":[]}`, 1),
		"a branch twice":    strings.Replace(kept, `"branch":"b"`, `"branch":"a"`, 1),
		"no move":           strings.Replace(kept, strings.Repeat("3", 40), strings.Repeat("2", 40), 1),
		"a short old tip":   strings.Replace(kept, strings.Repeat("4", 40), "4444", 1),
		"no branch name":    strings.Replace(kept, `"branch":"a"`, `"branch":"a..b"`, 1),
		"an unknown field":  strings.Replace(kept, `{"schema"`, `{"lease":"none","schema"`, 1),
		"a version's form":  strings.Replace(kept, pendingBranchesSchema, pendingVersionSchema, 1),
		"the tip twice":     strings.Replace(kept, `"tip":`, `"tip":"`+strings.Repeat("6", 40)+`","tip":`, 1),
		"a field left out":  strings.Replace(kept, `,"new":"`+strings.Repeat("5", 40)+`"`, "", 1),
		"another form next": kept + " " + kept,
	} {
		if form == kept {
			t.Fatalf("%s: the edit changed nothing", what)
		}
		var q PendingBranches
		if err := json.Unmarshal([]byte(form), &q); err == nil {
			t.Errorf("%s restored: %+v", what, q)
		}
	}
}
