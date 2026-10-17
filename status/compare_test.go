package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// compareRepo is an origin and a clone of it, for compare's tests.
type compareRepo struct {
	t            *testing.T
	origin, work string
}

func newCompareRepo(t *testing.T) *compareRepo {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VALLEY_KNOWN_SIGNERS", "")
	r := &compareRepo{t: t, origin: filepath.Join(dir, "origin.git"), work: filepath.Join(dir, "work")}
	r.in(dir, "init", "--quiet", "--bare", "--initial-branch=main", r.origin)
	r.in(dir, "clone", "--quiet", r.origin, r.work)
	r.git("config", "user.name", "compare-test")
	r.git("config", "user.email", "compare-test@localhost")
	r.git("config", "valley.principal", "klaus-env")
	r.git("checkout", "--quiet", "-b", "main")
	r.file("README", "base\n")
	r.git("commit", "--quiet", "-m", "base")
	r.git("push", "--quiet", "origin", "main")
	t.Chdir(r.work)
	return r
}

func (r *compareRepo) in(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

func (r *compareRepo) git(args ...string) string {
	r.t.Helper()
	return r.in(r.work, args...)
}

// file writes a file in the work tree and stages it.
func (r *compareRepo) file(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.work, name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", name)
}

// valley runs a verb of valley-go and returns what it printed.
func (r *compareRepo) valley(args ...string) string {
	r.t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 0 {
		r.t.Fatalf("valley-go %s: %s", strings.Join(args, " "), stderr.String())
	}
	return stdout.String()
}

// moveMain adds a commit to main on origin, as a landing would, and
// fetches it. It returns main's new tip.
func (r *compareRepo) moveMain(name string) string {
	r.git("checkout", "--quiet", "main")
	r.file(name, name+"\n")
	r.git("commit", "--quiet", "-m", "land "+name)
	r.git("push", "--quiet", "origin", "main")
	return r.git("rev-parse", "HEAD")
}

// rebaseOnto rebases a branch onto main and pushes it, and returns its
// new head.
func (r *compareRepo) rebaseOnto(branch string) string {
	r.git("checkout", "--quiet", branch)
	r.git("rebase", "--quiet", "main")
	r.git("push", "--quiet", "--force", "origin", branch)
	r.git("checkout", "--quiet", "main")
	return r.git("rev-parse", branch)
}

// setRef points a ref on origin at an object, or deletes it for "".
func (r *compareRepo) setRef(ref, oid string) {
	if oid == "" {
		r.in(r.origin, "update-ref", "-d", ref)
		return
	}
	r.in(r.origin, "update-ref", ref, oid)
}

// outcomeRecord writes an outcome record on origin, as the integrator
// writes one, and returns its blob.
func (r *compareRepo) outcomeRecord(head, decision string) string {
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = r.origin
	cmd.Stdin = strings.NewReader(head + " " + r.git("rev-parse", "main") + " 0123456789abcdef " + decision + "\n")
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func (r *compareRepo) compare(id string, version int, head string) (kind, text string) {
	r.t.Helper()
	out := r.valley("compare", "--tip", r.git("rev-parse", "origin/main"), id, strconv.Itoa(version), head)
	kind, text, _ = strings.Cut(out, "\n")
	return kind, text
}

// A request or an outcome under a branch's request name is evidence that
// a head was approved only while nothing else maps to that name: no
// version of another change, and no branch on origin that is not the
// change's own. Under c-<change ID> it always is. An ambiguous name stops
// the search: an older version is not compared instead.
func TestTheApprovedVersionIsNamedUnambiguously(t *testing.T) {
	r := newCompareRepo(t)
	id := strings.Repeat("x", 32)
	r.git("checkout", "--quiet", "-b", "a/b")
	r.file("feature.txt", "feature\n")
	r.git("commit", "--quiet", "-m", "add the feature")
	r.git("push", "--quiet", "origin", "a/b")
	r.valley("ready", "--change", id, "a/b")
	v1 := r.git("rev-parse", "a/b")
	byBranch := requestPrefix + "a-b"
	r.setRef(byBranch, v1)
	r.moveMain("other")
	v2 := r.rebaseOnto("a/b")
	r.valley("ready", "--change", id, "a/b")
	r.git("fetch", "--quiet", "origin")

	clean := func(why string) {
		t.Helper()
		if kind, text := r.compare(id, 2, v2); kind != compareClean || !strings.Contains(text, "clean restack of approved v1 at "+short(v1)) {
			t.Fatalf("%s: %s\n%s", why, kind, text)
		}
	}
	unknown := func(why, ref string) {
		t.Helper()
		kind, text := r.compare(id, 2, v2)
		if kind != compareNone || !strings.Contains(text, "is named by "+ref+", and another change or branch maps to that name too") {
			t.Fatalf("%s: %s\n%s", why, kind, text)
		}
	}
	clean("a request under the branch's name")

	// A plain branch on origin whose name maps to a-b as well.
	r.git("push", "--quiet", "origin", "main:refs/heads/a-b")
	unknown("a request under a name a plain branch maps to", byBranch)
	r.setRef("refs/heads/a-b", "")
	clean("a request once the plain branch is gone")

	// Another change, whose version names a branch that maps to a-b.
	r.git("checkout", "--quiet", "-b", "a-b", "main")
	r.file("y.txt", "y\n")
	r.git("commit", "--quiet", "-m", "another change")
	r.git("push", "--quiet", "origin", "a-b")
	r.valley("ready", "--change", strings.Repeat("y", 32), "a-b")
	r.git("checkout", "--quiet", "main")
	r.setRef("refs/heads/a-b", "")
	unknown("a request under a name another change's version maps to", byBranch)

	// The outcome of the same request, after the integrator consumed it:
	// ambiguous under a-b, and evidence under c-<change ID>.
	r.setRef(byBranch, "")
	r.setRef(outcomePrefix+"a-b", r.outcomeRecord(v1, "stale"))
	unknown("an outcome under a name another change's version maps to", outcomePrefix+"a-b")
	r.setRef(outcomePrefix+"a-b", "")
	r.setRef(outcomePrefix+"c-"+id, r.outcomeRecord(v1, "refused-not-ready"))
	if kind, text := r.compare(id, 2, v2); kind != compareNone || text != "" {
		t.Fatalf("an outcome that judged no approval counted: %s\n%s", kind, text)
	}
	r.setRef(outcomePrefix+"c-"+id, r.outcomeRecord(v1, "land"))
	clean("an outcome under c-<change ID>")

	// A version shown is never its own reference, and a greater version
	// named only ambiguously stops the search before v1.
	r.git("checkout", "--quiet", "a/b")
	r.file("feature.txt", "feature, edited\n")
	r.git("commit", "--quiet", "-m", "edit the feature")
	r.git("push", "--quiet", "origin", "a/b")
	r.valley("ready", "--change", id, "a/b")
	r.git("checkout", "--quiet", "main")
	v3 := r.git("rev-parse", "a/b")
	r.setRef(requestPrefix+"c-"+id, v3)
	if kind, text := r.compare(id, 3, v3); kind != compareChanged || !strings.Contains(text, "changed since approved v1 at "+short(v1)) {
		t.Fatalf("v3 is not set against v1, the greatest version below it with an approval: %s\n%s", kind, text)
	}
	r.setRef(requestPrefix+"c-"+id, "")
	r.setRef(byBranch, v2)
	if kind, text := r.compare(id, 3, v3); kind != compareNone || !strings.Contains(text, "v2 at "+short(v2)+" is named by "+byBranch) {
		t.Fatalf("v2 named ambiguously did not stop the search before v1: %s\n%s", kind, text)
	}
}

// clean names the landings between the two bases, oldest first; changed
// lists the files whose change differs and the range-diff; and a message
// or an author that changed is shown whatever the files say.
func TestCompareSaysWhatChanged(t *testing.T) {
	r := newCompareRepo(t)
	id := strings.Repeat("z", 32)
	r.git("checkout", "--quiet", "-b", "topic")
	r.file("tool.sh", "#!/bin/sh\n")
	r.file("image.bin", "\x00\x01")
	r.git("commit", "--quiet", "-m", "add the tool")
	r.git("push", "--quiet", "origin", "topic")
	r.valley("ready", "--change", id, "topic")
	v1 := r.git("rev-parse", "topic")
	b0 := r.git("rev-parse", "main")
	r.setRef(requestPrefix+"c-"+id, v1)
	one, two := r.moveMain("one"), r.moveMain("two")
	v2 := r.rebaseOnto("topic")
	r.valley("ready", "--change", id, "topic")
	r.git("fetch", "--quiet", "origin")

	kind, text := r.compare(id, 2, v2)
	want := "zzzz v2 at " + short(v2) + ": clean restack of approved v1 at " + short(v1) + ".\n" +
		"File delta unchanged. Base " + short(b0) + " -> " + short(two) + ".\n" +
		"Landings between bases:\n" +
		"  " + short(one) + " land one\n" +
		"  " + short(two) + " land two\n"
	if kind != compareClean || text != want {
		t.Fatalf("clean:\n%s\n%s\nwant:\n%s", kind, text, want)
	}

	// v3: the binary file's content and the tool's mode change. git
	// range-diff calls the two commits equal, since a binary file's patch
	// says only that it differs, so the listing of files is what shows it.
	r.setRef(requestPrefix+"c-"+id, v2)
	r.git("checkout", "--quiet", "topic")
	r.file("image.bin", "\x00\x02")
	if err := os.Chmod(filepath.Join(r.work, "tool.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.git("add", "tool.sh")
	r.git("commit", "--quiet", "--amend", "--no-edit")
	r.git("push", "--quiet", "--force", "origin", "topic")
	r.valley("ready", "--change", id, "topic")
	r.git("checkout", "--quiet", "main")
	v3 := r.git("rev-parse", "topic")
	kind, text = r.compare(id, 3, v3)
	for _, line := range []string{
		"zzzz v3 at " + short(v3) + ": changed since approved v2 at " + short(v2) + ".\nReview the range-diff before approving.\n",
		"Files whose change differs from v2's, as mode and SHA-256 of the content:\n  image.bin\n    v2: absent -> 100644 ",
		"  tool.sh\n    v2: absent -> 100644 ",
		"    v3: absent -> 100755 ",
		"git range-diff " + short(two) + ".." + short(v2) + " " + short(two) + ".." + short(v3) + ":\n",
		"1:  " + short(v2) + " = 1:  " + short(v3) + " add the tool",
	} {
		if !strings.Contains(text, line) {
			t.Fatalf("changed does not say %q:\n%s", line, text)
		}
	}
	if kind != compareChanged || strings.Contains(text, "Commit messages") || strings.Contains(text, "Commit authors") {
		t.Fatalf("changed: %s\n%s", kind, text)
	}

	// v4: the same files, with another message and another author.
	r.setRef(requestPrefix+"c-"+id, v3)
	r.git("checkout", "--quiet", "topic")
	r.git("commit", "--quiet", "--amend", "-m", "add the tool, renamed", "--author", "Someone Else <else@localhost>")
	r.git("push", "--quiet", "--force", "origin", "topic")
	r.valley("ready", "--change", id, "topic")
	r.git("checkout", "--quiet", "main")
	v4 := r.git("rev-parse", "topic")
	kind, text = r.compare(id, 4, v4)
	for _, line := range []string{
		": clean restack of approved v3 at " + short(v3) + ".\nFile delta unchanged. Base " + short(two) + " -> " + short(two) + ".\n",
		"Commit messages changed since approved v3. Read them before approving:\n" +
			"  v3, commit 1 of 1, " + short(v3) + ":\n    add the tool\n" +
			"  v4, commit 1 of 1, " + short(v4) + ":\n    add the tool, renamed\n",
		"Commit authors changed since approved v3:\n  v3, commit 1 of 1, " + short(v3) + ": compare-test <compare-test@localhost> ",
		"  v4, commit 1 of 1, " + short(v4) + ": Someone Else <else@localhost> ",
		"Landings between bases: none.\n",
	} {
		if !strings.Contains(text, line) {
			t.Fatalf("a changed message and author do not say %q:\n%s", line, text)
		}
	}
	if kind != compareClean {
		t.Fatalf("a changed message alone is still a clean file delta: %s", kind)
	}

	// The version given must have the head given.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"compare", "--tip", two, id, "3", v4}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "v3 of change zzzz records the head") {
		t.Fatalf("compare took a head its version does not record: %d %s", code, stderr.String())
	}
}
