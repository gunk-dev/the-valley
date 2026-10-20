package review

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"the-valley/change"
	"the-valley/delta"
)

// world is a scratch repository the tests build commits, change records
// and review histories in, with real git.
type world struct {
	t   *testing.T
	dir string
	env []string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{t: t, dir: dir, env: append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "HOME="+dir,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@localhost", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@localhost")}
	w.git(nil, "init", "--quiet", "--bare")
	return w
}

// clone is another repository beside w, with w as its remote origin.
func (w *world) clone() *world {
	w.t.Helper()
	c := &world{t: w.t, dir: w.t.TempDir(), env: w.env}
	c.git(nil, "init", "--quiet", "--bare")
	c.git(nil, "remote", "add", "origin", w.dir)
	c.git(nil, "fetch", "--quiet", "origin", "+refs/*:refs/*")
	return c
}

func (w *world) repo() Repo { return Repo{Dir: w.dir, Env: w.env} }

func (w *world) git(stdin []byte, args ...string) string {
	w.t.Helper()
	out, err := w.run(stdin, args...)
	if err != nil {
		w.t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func (w *world) run(stdin []byte, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = w.dir
	cmd.Env = w.env
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &gitError{err, stderr.String()}
	}
	return strings.TrimSpace(string(out)), nil
}

type gitError struct {
	err    error
	stderr string
}

func (e *gitError) Error() string { return e.err.Error() + ": " + e.stderr }

func (w *world) blob(content string) string {
	return w.git([]byte(content), "hash-object", "-w", "--stdin")
}

// tree writes the tree that holds exactly the files given, each a plain
// file, through a scratch index.
func (w *world) tree(files map[string]string) string {
	w.t.Helper()
	index := filepath.Join(w.t.TempDir(), "index")
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var input bytes.Buffer
	for _, p := range paths {
		input.WriteString("100644 " + w.blob(files[p]) + "\t" + p + "\n")
	}
	saved := w.env
	w.env = append(w.env, "GIT_INDEX_FILE="+index)
	defer func() { w.env = saved }()
	w.git(input.Bytes(), "update-index", "--add", "--index-info")
	return w.git(nil, "write-tree")
}

// commit writes a commit of the files given on the parents given.
func (w *world) commit(files map[string]string, parents ...string) string {
	w.t.Helper()
	return w.commitTree(w.tree(files), parents...)
}

func (w *world) commitTree(tree string, parents ...string) string {
	w.t.Helper()
	args := []string{"commit-tree", tree, "-m", "c"}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	return w.git(nil, args...)
}

// oneFile writes a commit whose tree holds one plain file, on the parents
// given.
func (w *world) oneFile(name string, content []byte, parents ...string) string {
	w.t.Helper()
	blob := w.git(content, "hash-object", "-w", "--stdin")
	tree := w.git([]byte("100644 blob "+blob+"\t"+name+"\n"), "mktree")
	return w.commitTree(tree, parents...)
}

// record writes a change record commit holding r, on the parents the hook
// would have it on.
func (w *world) record(r *change.Record, parents ...string) string {
	w.t.Helper()
	data, err := change.Encode(r)
	if err != nil {
		w.t.Fatal(err)
	}
	return w.oneFile("record.json", data, parents...)
}

func (w *world) setRef(ref, commit string) {
	w.t.Helper()
	w.git(nil, "update-ref", ref, commit)
}

func (w *world) refAt(ref string) string {
	w.t.Helper()
	out, err := w.run(nil, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		return ""
	}
	return out
}

const (
	changeID = "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz"
	otherID  = "kkkkllllmmmmnnnnooooppppqqqqrrrr"
)

// story is a change in w: main at tip, and a change readied at head1 and
// then head2, both on base, with its record ref at rec2, whose version 2
// is head2. rec1 is its first record, whose one version is head1.
type story struct {
	base, tip, head1, head2 string
	rec1, rec2              string
	record                  *change.Record
}

func (w *world) story() story {
	w.t.Helper()
	var s story
	s.base = w.commit(map[string]string{"README.md": "hello\n", "docs/guide.md": "one\ntwo\nthree\n"})
	s.tip = w.commit(map[string]string{"README.md": "hello, world\n", "docs/guide.md": "one\ntwo\nthree\n"}, s.base)
	s.head1 = w.commit(map[string]string{"README.md": "hello\n", "docs/guide.md": "one\n2\nthree\n"}, s.base)
	s.head2 = w.commit(map[string]string{"README.md": "hello\n", "docs/guide.md": "one\n2\nthree\nfour\n", "docs/new file.md": "new\n"}, s.base)
	w.setRef("refs/heads/main", s.tip)
	r := &change.Record{
		Schema: change.Schema, ChangeID: changeID, Target: "main", Owner: "author", State: change.Ready, Seq: 1,
		Versions: []change.Version{{N: 1, Head: change.HeadPrefix + s.head1, Title: "Edit the guide", Branch: "guide", Uploader: "author"}},
		Active:   1,
	}
	s.rec1 = w.record(r, s.head1)
	r.Seq, r.Active = 2, 2
	r.Versions = append(r.Versions, change.Version{N: 2, Head: change.HeadPrefix + s.head2, Title: "Edit the guide", Branch: "guide", Uploader: "author"})
	s.rec2 = w.record(r, s.rec1, s.head2)
	s.record = r
	w.setRef(change.Ref("main", changeID), s.rec2)
	return s
}

// budget is one no test of a small repository comes near.
func budget() delta.Budget {
	return delta.Budget{Bytes: 64 << 20, Entries: 100000, Deadline: time.Now().Add(time.Minute)}
}

func soon() time.Time { return time.Now().Add(time.Minute) }

// verdict is a review of version 2 of the story's change by reviewer,
// bound with Bind.
func (w *world) verdict(s story, reviewer, verdict string) *Record {
	w.t.Helper()
	_, b, err := Bind(w.repo(), s.rec2, 2, s.tip, budget())
	if err != nil {
		w.t.Fatal(err)
	}
	id, err := NewID()
	if err != nil {
		w.t.Fatal(err)
	}
	return &Record{
		Schema: Schema, ReviewID: id, ChangeID: changeID, Target: "main", Review: "docs-style",
		Reviewer: reviewer, Verdict: verdict, Binding: &b, Summary: "Read against " + s.tip[:7] + ".",
		Notes: []Note{
			{Side: After, Path: "docs/guide.md", Blob: change.HeadPrefix + w.git(nil, "rev-parse", s.head2+":docs/guide.md"),
				Lines: &Lines{Start: 2, End: 4}, Body: "Spell out the number."},
			{Side: Before, Path: "docs/guide.md", Blob: change.HeadPrefix + w.git(nil, "rev-parse", s.base+":docs/guide.md"),
				Lines: &Lines{Start: 2, End: 2}, Body: "This line read better."},
			{Side: After, Path: "docs/new file.md", Blob: change.HeadPrefix + w.git(nil, "rev-parse", s.head2+":docs/new file.md"),
				Body: "A whole new file."},
		},
	}
}

// withdrawal is a withdrawal of the review given, by reviewer.
func withdrawal(of *Record, reviewer string) *Record {
	id, err := NewID()
	if err != nil {
		panic(err)
	}
	w := of.ReviewID
	return &Record{
		Schema: Schema, ReviewID: id, ChangeID: of.ChangeID, Target: of.Target, Review: of.Review,
		Reviewer: reviewer, Verdict: Withdraw, Withdraws: &w, Summary: "Judged in error.",
	}
}

// against is what the story's reviews verify against.
func (w *world) against() Against {
	return Against{
		RecordTip: w.refAt(change.Ref("main", changeID)),
		TargetTip: w.refAt("refs/heads/main"),
		Budget:    budget(),
	}
}
