package delta

import (
	"strings"
	"testing"
)

// A rename is a deletion and an addition with one hash, and a change of
// mode alone, a symlink's target and a file replacing a symlink are each
// one line, read from trees git itself built.
func TestBetweenReadsTreesAsWholeFiles(t *testing.T) {
	r := newRepo(t)
	pre := r.tree([]file{
		{"old name.txt", ModeFile, []byte("moved\n")},
		{"run.sh", ModeFile, []byte("#!/bin/sh\n")},
		{"link", ModeSymlink, []byte("old name.txt")},
		{"was-link", ModeSymlink, []byte("run.sh")},
		{"same/deep/file", ModeFile, []byte("untouched\n")},
	})
	post := r.tree([]file{
		{"new name.txt", ModeFile, []byte("moved\n")},
		{"run.sh", ModeExec, []byte("#!/bin/sh\n")},
		{"link", ModeSymlink, []byte("new name.txt")},
		{"was-link", ModeFile, []byte("run.sh")},
		{"same/deep/file", ModeFile, []byte("untouched\n")},
	})
	d, err := Between(r.repo(), pre, post)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range d {
		got = append(got, c.Path+": "+c.Pre.Mode+" -> "+c.Post.Mode)
	}
	want := []string{
		"link: 120000 -> 120000",
		"new name.txt:  -> 100644",
		"old name.txt: 100644 -> ",
		"run.sh: 100644 -> 100755",
		"was-link: 120000 -> 100644",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the delta is\n%s", strings.Join(got, "\n"))
	}
	if d[1].Post.Sum != d[2].Pre.Sum {
		t.Fatal("a rename's two lines do not carry the same hash")
	}
	if d[3].Pre.Sum != d[3].Post.Sum || d[4].Pre.Sum != d[4].Post.Sum {
		t.Fatal("a change of mode alone changed the hash")
	}
}

// Trees a checkout on case-sensitive Linux could not hold faithfully are
// refused on either side, and so is one whose entries git would not read
// as written. An alias deep in a directory both trees share is refused
// too: each tree is read whole.
func TestTreesACheckoutCannotHoldAreRefused(t *testing.T) {
	r := newRepo(t)
	blob := r.blob([]byte("x\n"))
	sub := r.tree([]file{{"inner", ModeFile, []byte("x\n")}})
	empty := r.git(nil, "mktree")
	good := r.tree([]file{{"ok", ModeFile, []byte("x\n")}})
	cases := []struct {
		name, says string
		tree       string
	}{
		{"a file and a directory at one path", "named twice",
			r.rawTree(rawEntry{"100644", "a", blob}, rawEntry{"40000", "a", sub})},
		{"one name twice", "named twice",
			r.rawTree(rawEntry{"100644", "a", blob}, rawEntry{"100644", "a", blob})},
		{"a submodule", "submodule",
			r.rawTree(rawEntry{"160000", "mod", strings.Repeat("ab", 20)})},
		{"a mode git does not write", "mode \"100664\"",
			r.rawTree(rawEntry{"100664", "a", blob})},
		{"a zero-padded directory mode", "mode \"040000\"",
			r.rawTree(rawEntry{"040000", "a", sub})},
		{"a name holding a slash", "slash",
			r.rawTree(rawEntry{"100644", "a/b", blob})},
		{"the name ..", "component ..",
			r.rawTree(rawEntry{"100644", "..", blob})},
		{"the name .git", "git keeps for itself",
			r.rawTree(rawEntry{"40000", ".git", sub})},
		{"the name .GIT", "git keeps for itself",
			r.rawTree(rawEntry{"40000", "d", r.rawTree(rawEntry{"100644", ".GIT", blob})})},
		{"an empty directory", "empty directory",
			r.rawTree(rawEntry{"40000", "hollow", empty})},
		{"entries out of order", "out of git's order",
			r.rawTree(rawEntry{"100644", "b", blob}, rawEntry{"100644", "a", blob})},
		{"a directory sorted as a file", "out of git's order",
			r.rawTree(rawEntry{"40000", "a", sub}, rawEntry{"100644", "a.txt", blob})},
		{"a file whose object is a tree", "no blob",
			r.rawTree(rawEntry{"100644", "a", sub})},
		{"a directory whose object is a blob", "is a blob",
			r.rawTree(rawEntry{"40000", "a", blob})},
		{"an alias in a shared directory", "named twice",
			r.rawTree(rawEntry{"40000", "shared",
				r.rawTree(rawEntry{"100644", "a", blob}, rawEntry{"40000", "a", sub})})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, pair := range [][2]string{{c.tree, good}, {good, c.tree}, {c.tree, c.tree}} {
				_, err := Between(r.repo(), pair[0], pair[1])
				if err == nil {
					t.Fatalf("Between(%s, %s) accepted it", pair[0], pair[1])
				}
				if !strings.Contains(err.Error(), c.says) {
					t.Fatalf("the refusal does not say %q: %v", c.says, err)
				}
			}
		})
	}
}

// A file and a directory at one path in two different trees is no alias:
// the file is absent after and the directory's files absent before.
func TestAFileMayBecomeADirectory(t *testing.T) {
	r := newRepo(t)
	pre := r.tree([]file{{"a", ModeFile, []byte("flat\n")}})
	post := r.tree([]file{{"a/b", ModeFile, []byte("nested\n")}})
	d, err := Between(r.repo(), pre, post)
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 2 || d[0].Path != "a" || !d[0].Post.Absent() || d[1].Path != "a/b" || !d[1].Pre.Absent() {
		t.Fatalf("the delta is %q", d.Encode())
	}
}

// Only a full object ID that names a tree or a commit is read.
func TestBetweenTakesTreesAndCommitsByID(t *testing.T) {
	r := newRepo(t)
	tree := r.tree([]file{{"a", ModeFile, []byte("a\n")}})
	blob := r.blob([]byte("a\n"))
	for _, bad := range []string{"HEAD", tree[:12], blob, strings.Repeat("0", 40)} {
		if _, err := Between(r.repo(), bad, tree); err == nil {
			t.Errorf("Between took %q", bad)
		}
	}
}
