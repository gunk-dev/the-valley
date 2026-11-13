package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	r.git("config", "valley.principal", "alice")
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

// valley runs a verb of valley and returns what it printed.
func (r *compareRepo) valley(args ...string) string {
	r.t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, os.Exit); code != 0 {
		r.t.Fatalf("valley %s: %s", strings.Join(args, " "), stderr.String())
	}
	return stdout.String()
}

// runCompare sets a version against its approved one, as review does.
func (r *compareRepo) runCompare(args ...string) (string, error) {
	var out bytes.Buffer
	err := runCompare(git{env: gitEnvironment(os.Environ()), stderr: io.Discard}, args, &out)
	return out.String(), err
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
	return r.compareWith(id, version, head)
}

func (r *compareRepo) compareVerbose(id string, version int, head string) (kind, text string) {
	r.t.Helper()
	return r.compareWith(id, version, head, "-v")
}

func (r *compareRepo) compareWith(id string, version int, head string, flags ...string) (kind, text string) {
	r.t.Helper()
	args := append(append([]string{}, flags...), "--tip", r.git("rev-parse", "origin/main"), id, strconv.Itoa(version), head)
	out, err := r.runCompare(args...)
	if err != nil {
		r.t.Fatalf("compare %s: %s", strings.Join(args, " "), err)
	}
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
		if kind, text := r.compare(id, 2, v2); kind != compareClean || !strings.Contains(text, "rebased since you approved v1 at "+short(v1)+", with the same file changes.") {
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
	if kind, text := r.compare(id, 3, v3); kind != compareChanged || !strings.Contains(text, "edited since you approved v1 at "+short(v1)+".") {
		t.Fatalf("v3 is not set against v1, the greatest version below it with an approval: %s\n%s", kind, text)
	}
	r.setRef(requestPrefix+"c-"+id, "")
	r.setRef(byBranch, v2)
	if kind, text := r.compare(id, 3, v3); kind != compareNone || !strings.Contains(text, "v2 at "+short(v2)+" is named by "+byBranch) {
		t.Fatalf("v2 named ambiguously did not stop the search before v1: %s\n%s", kind, text)
	}
}

// A branch named c/<ID> or c-<ID> maps to c-<ID>, which is reserved for
// the change with that ID. A request or an outcome under it is that
// change's, so it is no evidence of approval for a change whose versions
// name the branch, even when it names one of their heads.
func TestAnotherChangesReservedNameIsNoEvidence(t *testing.T) {
	id, other := strings.Repeat("x", 32), strings.Repeat("y", 32)
	for _, prefix := range []string{"c/", "c-"} {
		t.Run(prefix, func(t *testing.T) {
			r := newCompareRepo(t)
			branch := prefix + other
			r.git("checkout", "--quiet", "-b", branch)
			r.file("feature.txt", "feature\n")
			r.git("commit", "--quiet", "-m", "add the feature")
			r.git("push", "--quiet", "origin", branch)
			r.valley("ready", "--change", id, branch)
			v1 := r.git("rev-parse", branch)
			r.moveMain("other")
			v2 := r.rebaseOnto(branch)
			r.valley("ready", "--change", id, branch)
			r.git("fetch", "--quiet", "origin")

			reserved := "c-" + other
			r.setRef(requestPrefix+reserved, v1)
			r.setRef(outcomePrefix+reserved, r.outcomeRecord(v1, "land"))
			if kind, text := r.compare(id, 2, v2); kind != compareNone || text != "" {
				t.Fatalf("a request and an outcome under %s counted for %s: %s\n%s", reserved, id[:4], kind, text)
			}
			r.setRef(requestPrefix+"c-"+id, v1)
			if kind, text := r.compare(id, 2, v2); kind != compareClean || !strings.Contains(text, "rebased since you approved v1 at "+short(v1)+", with the same file changes.") {
				t.Fatalf("a request under the change's own c-<change ID>: %s\n%s", kind, text)
			}
		})
	}
}

// land adds a commit to main on origin that writes a file, as a landing
// would, and fetches it. It returns main's new tip.
func (r *compareRepo) land(name, content, subject string) string {
	r.git("checkout", "--quiet", "main")
	r.file(name, content)
	r.git("commit", "--quiet", "-m", subject)
	r.git("push", "--quiet", "origin", "main")
	return r.git("rev-parse", "HEAD")
}

// amend replaces the branch's last commit with the work tree's staged
// files, keeping its message, and returns the new head after readying it.
func (r *compareRepo) amend(id, branch string, args ...string) string {
	r.git(append([]string{"commit", "--quiet", "--amend"}, args...)...)
	r.git("push", "--quiet", "--force", "origin", branch)
	r.valley("ready", "--change", id, branch)
	r.git("checkout", "--quiet", "main")
	return r.git("rev-parse", branch)
}

// lines is one line for each of n, numbered, with the line at edit
// replaced.
func lines(n, edit int, replacement string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if i == edit {
			b.WriteString(replacement + "\n")
		} else {
			b.WriteString("line " + strconv.Itoa(i) + "\n")
		}
	}
	return b.String()
}

func mustSay(t *testing.T, what, text string, want ...string) {
	t.Helper()
	for _, line := range want {
		if !strings.Contains(text, line) {
			t.Fatalf("%s does not say %q:\n%s", what, line, text)
		}
	}
}

// A clean restack names the landings between the two bases, oldest
// first. An edited version lists the files whose change differs and what
// it changes beyond the approved version carried onto main, in git's
// ordinary form, whatever the person's diff configuration; -v adds the
// range-diff. A message or
// an author that changed is an edit, shown whatever the files say.
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
	want := "zzzz v2 at " + short(v2) + ": rebased since you approved v1 at " + short(v1) + ", with the same file changes.\n" +
		"Main moved " + short(b0) + " → " + short(two) + ", landing 2 commits:\n" +
		"  " + short(one) + " land one\n" +
		"  " + short(two) + " land two\n"
	if kind != compareClean || text != want {
		t.Fatalf("clean:\n%s\n%s\nwant:\n%s", kind, text, want)
	}

	// v3: the binary file's content and the tool's mode change. git
	// range-diff calls the two commits equal, since a binary file's patch
	// says only that it differs, but the commit's patch is not the same.
	r.setRef(requestPrefix+"c-"+id, v2)
	r.git("checkout", "--quiet", "topic")
	r.file("image.bin", "\x00\x02")
	if err := os.Chmod(filepath.Join(r.work, "tool.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.git("add", "tool.sh")
	v3 := r.amend(id, "topic", "--no-edit")
	r.git("config", "diff.noprefix", "true")
	kind, text = r.compare(id, 3, v3)
	r.git("config", "--unset", "diff.noprefix")
	mustSay(t, "an edited commit", text,
		"zzzz v3 at "+short(v3)+": edited since you approved v2 at "+short(v2)+".\n"+
			"Its base is still "+short(two)+".\n"+
			"1 of 1 commit changed, and 2 files differ from v2.\n2 files changed since v2:\n  image.bin\n  tool.sh\n"+
			"\n── what v3 changes beyond v2 carried onto main "+short(two)+" ──\ndiff --git a/image.bin b/image.bin\n",
		"Binary files a/image.bin and b/image.bin differ\ndiff --git a/tool.sh b/tool.sh\nold mode 100644\nnew mode 100755\n",
	)
	if kind != compareChanged || strings.Contains(text, "Commit messages") || strings.Contains(text, "v2: absent") || strings.Contains(text, "range-diff") {
		t.Fatalf("an edited commit: %s\n%s", kind, text)
	}
	_, verbose := r.compareVerbose(id, 3, v3)
	mustSay(t, "-v", verbose, "  image.bin\n    v2: absent -> 100644 ", "  tool.sh\n    v2: absent -> 100644 ", "    v3: absent -> 100755 ",
		"── git range-diff "+short(two)+".."+short(v2)+" "+short(two)+".."+short(v3)+" ──\n",
		"1:  "+short(v2)+" = 1:  "+short(v3)+" add the tool")

	// v4: the same files, with another message and another author.
	r.setRef(requestPrefix+"c-"+id, v3)
	r.git("checkout", "--quiet", "topic")
	v4 := r.amend(id, "topic", "-m", "add the tool, renamed", "--author", "Someone Else <else@localhost>")
	kind, text = r.compare(id, 4, v4)
	mustSay(t, "a changed message and author", text,
		": edited since you approved v3 at "+short(v3)+", with the same file changes.\n",
		"Commit messages edited since you approved v3. Read them before approving:\n"+
			"  commit 1 of 1, v3 "+short(v3)+" → v4 "+short(v4)+":\n    -add the tool\n    +add the tool, renamed\n",
		"Commit authors edited since you approved v3:\n  v3, commit 1 of 1, "+short(v3)+": compare-test <compare-test@localhost> ",
		"  v4, commit 1 of 1, "+short(v4)+": Someone Else <else@localhost> ",
		"Its base is still "+short(two)+".\n",
	)
	if kind != compareClean {
		t.Fatalf("a changed message alone is still a clean file delta: %s", kind)
	}

	// The version given must have the head given.
	if _, err := r.runCompare("--tip", two, id, "3", v4); err == nil || !strings.Contains(err.Error(), "v3 of change zzzz records the head") {
		t.Fatalf("compare took a head its version does not record: %v", err)
	}
}

// A rebase whose commit is untouched, onto landings that edited the same
// file, says the commit is unchanged and names those landings, with no
// range-diff. An edit after it names, for each file whose change
// differs, what it changes beyond the approved version carried onto
// main, and with -v the landings that edited each file and the
// range-diff.
func TestCompareAttributesFilesMainAlsoEdited(t *testing.T) {
	r := newCompareRepo(t)
	id := strings.Repeat("w", 32)
	r.land("shared.txt", lines(20, 0, ""), "add shared")
	b0 := r.git("rev-parse", "main")
	r.git("checkout", "--quiet", "-b", "topic")
	r.file("shared.txt", lines(20, 2, "topic's line 2"))
	r.git("commit", "--quiet", "-m", "edit line 2")
	r.git("push", "--quiet", "origin", "topic")
	r.valley("ready", "--change", id, "topic")
	v1 := r.git("rev-parse", "topic")
	r.setRef(requestPrefix+"c-"+id, v1)
	shared := r.land("shared.txt", lines(20, 18, "main's line 18"), "edit line 18 of shared, with a subject long enough that the listing has to cut it short")
	b1 := r.moveMain("unrelated")
	v2 := r.rebaseOnto("topic")
	r.valley("ready", "--change", id, "topic")
	r.git("fetch", "--quiet", "origin")

	kind, text := r.compare(id, 2, v2)
	mustSay(t, "a rebase onto a file main also edited", text,
		"wwww v2 at "+short(v2)+": rebased since you approved v1 at "+short(v1)+".\n"+
			"Its commit is unchanged: same patch, same message.\n"+
			"Main moved "+short(b0)+" → "+short(b1)+" and also edited 1 of its files:\n"+
			"  shared.txt  "+short(shared)+" edit line 18 of shared, with a subject long enough that the listing has to cu…\n"+
			"\n── v2's diff of this file against main "+short(b1)+" ──\ndiff --git a/shared.txt b/shared.txt\n",
		"-line 2\n+topic's line 2\n",
	)
	if kind != compareChanged || strings.Contains(text, "range-diff") {
		t.Fatalf("a rebase onto a file main also edited: %s\n%s", kind, text)
	}

	// v3: the commit is edited and adds a file, after main edits the
	// shared file again.
	r.setRef(requestPrefix+"c-"+id, v2)
	again := r.land("shared.txt", lines(20, 18, "main's line 18, again"), "edit line 18 again")
	b2 := r.git("rev-parse", "main")
	r.rebaseOnto("topic")
	r.git("checkout", "--quiet", "topic")
	r.file("shared.txt", strings.Replace(r.git("show", "HEAD:shared.txt")+"\n", "topic's line 2", "topic's line 2, edited", 1))
	r.file("new.txt", "new\n")
	v3 := r.amend(id, "topic", "--no-edit")
	r.git("fetch", "--quiet", "origin")
	kind, text = r.compare(id, 3, v3)
	mustSay(t, "an edit after a rebase", text,
		"wwww v3 at "+short(v3)+": edited since you approved v2 at "+short(v2)+".\n"+
			"Main moved "+short(b1)+" → "+short(b2)+".\n"+
			"1 of 1 commit changed, and 2 files differ from v2.\n"+
			"2 files changed since v2:\n  new.txt\n  shared.txt\n"+
			"\n── what v3 changes beyond v2 carried onto main "+short(b2)+" ──\ndiff --git a/new.txt b/new.txt\n",
		"@@ -1,5 +1,5 @@\n line 1\n-topic's line 2\n+topic's line 2, edited\n",
	)
	_, verbose := r.compareVerbose(id, 3, v3)
	mustSay(t, "-v", verbose, "  new.txt     not edited on main\n", "  shared.txt  "+short(again)+" edit line 18 again\n",
		"\n── git range-diff "+short(b1)+".."+short(v2)+" "+short(b2)+".."+short(v3)+" ──\n")
	if kind != compareChanged {
		t.Fatalf("an edit after a rebase: %s", kind)
	}
}

// style changes no line's text, styles a comparison's title, headings and
// summary, and leaves a diff's lines as they are; with NO_COLOR set, or
// for no terminal, it copies the page.
func TestStyleKeepsEveryLine(t *testing.T) {
	page := "layer heading\n" +
		"zzzz v3 at 1234abc: edited since you approved v2 at 5678def.\n" +
		"does oc-1234567: a knot\n" +
		"Main moved 1111aaa → 2222bbb.\n" +
		"  shared.txt  3333ccc edit line 18\n" +
		"\n── v3's diff of this file against main 2222bbb ──\n" +
		"diff --git a/f b/f\n" +
		"+edited 4444ddd\n"
	style := func(args ...string) string {
		var out bytes.Buffer
		if err := runStyle(args, strings.NewReader(page), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	t.Setenv("NO_COLOR", "")
	styled := style("--paged")
	if got := styleEscape.ReplaceAllString(styled, ""); got != page {
		t.Fatalf("style changed the text:\n%s", got)
	}
	for _, want := range []string{
		"layer heading\n",
		"\x1b[1mzzzz v3 at 1234abc: \x1b[33medited\x1b[39m since you approved v2 at 5678def.\x1b[22m\n",
		"Main moved \x1b[2m1111aaa\x1b[22m → \x1b[2m2222bbb\x1b[22m.\n",
		"\x1b[36m── v3's diff of this file against main 2222bbb ──\x1b[39m\n",
		"diff --git a/f b/f\n+edited 4444ddd\n",
	} {
		if !strings.Contains(styled, want) {
			t.Fatalf("style does not give %q:\n%q", want, styled)
		}
	}
	if got := style(); got != page {
		t.Fatalf("style styled a page for no terminal:\n%q", got)
	}
	t.Setenv("NO_COLOR", "1")
	if got := style("--paged"); got != page {
		t.Fatalf("style styled a page with NO_COLOR set:\n%q", got)
	}
}

// numbered is n numbered lines, with the lines edits names replaced.
func numbered(n int, edits map[int]string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if e, ok := edits[i]; ok {
			b.WriteString(e + "\n")
		} else {
			b.WriteString("line " + strconv.Itoa(i) + "\n")
		}
	}
	return b.String()
}

// commitFiles writes the files on the branch checked out, commits and
// pushes them, and returns the commit. A file whose content is "" is
// deleted.
func (r *compareRepo) commitFiles(branch, subject string, files map[string]string) string {
	r.t.Helper()
	r.git("checkout", "--quiet", branch)
	for name, content := range files {
		if content == "" {
			r.git("rm", "--quiet", name)
		} else {
			r.file(name, content)
		}
	}
	r.git("commit", "--quiet", "-m", subject)
	r.git("push", "--quiet", "--force", "origin", branch)
	return r.git("rev-parse", "HEAD")
}

// newCarryRepo is main holding a.txt, b.txt and c.txt, 20 lines each, and
// a change that edits line 2 of each, readied and approved as v1. It
// returns v1 and main's tip under it. Every commit has one author date,
// so that an author line differs only where a test changes it.
func newCarryRepo(t *testing.T, id string) (r *compareRepo, v1, b0 string) {
	t.Setenv("GIT_AUTHOR_DATE", "1700000000 +0000")
	r = newCompareRepo(t)
	b0 = r.commitFiles("main", "add the files", map[string]string{"a.txt": numbered(20, nil), "b.txt": numbered(20, nil), "c.txt": numbered(20, nil)})
	r.git("checkout", "--quiet", "-b", "topic")
	edit := numbered(20, map[int]string{2: "topic's line 2"})
	r.commitFiles("topic", "edit line 2", map[string]string{"a.txt": edit, "b.txt": edit, "c.txt": edit})
	r.valley("ready", "--change", id, "topic")
	v1 = r.git("rev-parse", "topic")
	r.setRef(requestPrefix+"c-"+id, v1)
	return r, v1, b0
}

// mainEditsLine5 lands an edit of line 5 of each of the three files: near
// enough the change's line 2 that a rebase changes its commit's patch, far
// enough that it merges.
func (r *compareRepo) mainEditsLine5() string {
	r.t.Helper()
	edit := numbered(20, map[int]string{5: "main's line 5"})
	return r.commitFiles("main", "edit line 5", map[string]string{"a.txt": edit, "b.txt": edit, "c.txt": edit})
}

// readyRebased rebases the change by hand onto main, applies files on
// top, amending its commit, readies the result and returns it.
func (r *compareRepo) readyRebased(id string, files map[string]string) string {
	r.t.Helper()
	r.git("checkout", "--quiet", "topic")
	r.git("rebase", "--quiet", "main")
	for name, content := range files {
		if content == "" {
			r.git("rm", "--quiet", name)
		} else {
			r.file(name, content)
		}
	}
	if len(files) > 0 {
		r.git("commit", "--quiet", "--amend", "--no-edit")
	}
	r.git("push", "--quiet", "--force", "origin", "topic")
	r.valley("ready", "--change", id, "topic")
	r.git("checkout", "--quiet", "main")
	r.git("fetch", "--quiet", "origin")
	return r.git("rev-parse", "topic")
}

// blobIDs are the object names a diff's index lines carry, which the
// expected pages leave out.
var blobIDs = regexp.MustCompile(`(?m)^( *)index [0-9a-f]+\.\.[0-9a-f]+`)

// page is want with each {name} replaced by the short form of the object
// ID after it in vars.
func page(want string, vars ...string) string {
	for i := 0; i+1 < len(vars); i += 2 {
		want = strings.ReplaceAll(want, "{"+vars[i]+"}", short(vars[i+1]))
	}
	return want
}

func wantPage(t *testing.T, what, kind, text, wantKind, want string) {
	t.Helper()
	if got := blobIDs.ReplaceAllString(text, "${1}index …"); kind != wantKind || got != want {
		t.Fatalf("%s: %s\n%s\nwant %s:\n%s", what, kind, got, wantKind, want)
	}
}

// A hand rebase onto main's edits of nearby lines changes the commit and
// none of the author's edits: every file is the approved version carried
// onto main, and the page says so in a few lines, with no range-diff.
func TestEditedPageCarriesUnchangedFiles(t *testing.T) {
	id := strings.Repeat("k", 32)
	r, v1, b0 := newCarryRepo(t, id)
	b1 := r.mainEditsLine5()
	v2 := r.readyRebased(id, nil)
	kind, text := r.compare(id, 2, v2)
	wantPage(t, "a hand rebase", kind, text, compareChanged,
		"kkkk v2 at "+short(v2)+": edited since you approved v1 at "+short(v1)+".\n"+
			"Main moved "+short(b0)+" → "+short(b1)+".\n"+
			"1 of 1 commit changed, and 3 files differ from v1.\n"+
			"3 files carried unchanged onto main:\n  a.txt\n  b.txt\n  c.txt\n"+
			"\n── what v2 changes beyond v1 carried onto main "+short(b1)+" ──\n"+
			"  none: v2 is v1 carried onto main and nothing else.\n")

	// v3: one further edit, and a new file.
	r.setRef(requestPrefix+"c-"+id, v2)
	v3 := r.readyRebased(id, map[string]string{
		"b.txt":   numbered(20, map[int]string{2: "topic's line 2", 5: "main's line 5", 14: "topic's line 14"}),
		"new.txt": "new\n"})
	kind, text = r.compare(id, 3, v3)
	further := page(`kkkk v3 at {v3}: edited since you approved v2 at {v2}.
Its base is still {b1}.
1 of 1 commit changed, and 2 files differ from v2.
2 files changed since v2:
  b.txt
  new.txt
`, "v2", v2, "v3", v3, "b1", b1)
	beyond := page(`
── what v3 changes beyond v2 carried onto main {b1} ──
diff --git a/b.txt b/b.txt
index … 100644
--- a/b.txt
+++ b/b.txt
@@ -11,7 +11,7 @@ line 10
 line 11
 line 12
 line 13
-line 14
+topic's line 14
 line 15
 line 16
 line 17
diff --git a/new.txt b/new.txt
new file mode 100644
index …
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+new
`, "b1", b1)
	wantPage(t, "a further edit", kind, text, compareChanged, further+beyond)

	// -v adds each file's images and the range-diff.
	_, text = r.compareVerbose(id, 3, v3)
	sum := func(rev string) string {
		out := r.git("cat-file", "blob", rev)
		return fmt.Sprintf("%x", sha256.Sum256([]byte(out+"\n")))[:12]
	}
	verbose := strings.Replace(further, "  b.txt\n  new.txt\n", "  b.txt\n"+
		"    v2: 100644 "+sum(b1+":b.txt")+" -> 100644 "+sum(v2+":b.txt")+"\n"+
		"    v3: 100644 "+sum(b1+":b.txt")+" -> 100644 "+sum(v3+":b.txt")+"\n"+
		"  new.txt\n    v2: unchanged\n    v3: absent -> 100644 "+sum(v3+":new.txt")+"\n", 1) + page(`
── git range-diff {b1}..{v2} {b1}..{v3} ──
1:  {v2} ! 1:  {v3} edit line 2
    @@ b.txt
      line 3
      line 4
      main's line 5
    +@@ b.txt: line 10
    + line 11
    + line 12
    + line 13
    +-line 14
    ++topic's line 14
    + line 15
    + line 16
    + line 17
     
      ## c.txt ##
     @@
    @@ c.txt
      line 3
      line 4
      main's line 5
    +
    + ## new.txt (new) ##
    +@@
    ++new
`, "b1", b1, "v2", v2, "v3", v3) + beyond
	wantPage(t, "-v", kind, text, compareChanged, verbose)
}

// A file dropped from the change shows as the approved edit reverted, and
// an added line moved within its file shows though its text is the same.
// With main where it was, the carry is the approved version itself.
func TestEditedPageShowsDroppedAndMovedLines(t *testing.T) {
	id := strings.Repeat("l", 32)
	r, v1, b0 := newCarryRepo(t, id)
	v2 := r.readyRebased(id, map[string]string{
		"c.txt": numbered(20, nil),
		"a.txt": strings.Replace(strings.Replace(numbered(20, nil), "line 2\n", "", 1), "line 10\n", "line 10\ntopic's line 2\n", 1),
	})
	kind, text := r.compare(id, 2, v2)
	wantPage(t, "a dropped file and a moved line", kind, text, compareChanged, page(`llll v2 at {v2}: edited since you approved v1 at {v1}.
Its base is still {b0}.
1 of 1 commit changed, and 2 files differ from v1.
2 files changed since v1:
  a.txt
  c.txt

── what v2 changes beyond v1 carried onto main {b0} ──
diff --git a/a.txt b/a.txt
index … 100644
--- a/a.txt
+++ b/a.txt
@@ -1,5 +1,4 @@
 line 1
-topic's line 2
 line 3
 line 4
 line 5
@@ -8,6 +7,7 @@ line 7
 line 8
 line 9
 line 10
+topic's line 2
 line 11
 line 12
 line 13
diff --git a/c.txt b/c.txt
index … 100644
--- a/c.txt
+++ b/c.txt
@@ -1,5 +1,5 @@
 line 1
-topic's line 2
+line 2
 line 3
 line 4
 line 5
`, "v1", v1, "v2", v2, "b0", b0))
}

// A content conflict the author resolved shows as the carry's conflict
// markers removed and the resolution added, naming the landing that
// edited the file, whatever merge attribute the person's configuration
// sets.
func TestEditedPageShowsAResolvedConflict(t *testing.T) {
	id := strings.Repeat("m", 32)
	r, v1, b0 := newCarryRepo(t, id)
	b1 := r.commitFiles("main", "reword line 2 of a", map[string]string{"a.txt": numbered(20, map[int]string{2: "main's line 2"})})
	r.git("checkout", "--quiet", "topic")
	r.git("reset", "--quiet", "--hard", b1)
	edit := numbered(20, map[int]string{2: "topic's line 2"})
	r.file("a.txt", numbered(20, map[int]string{2: "main's and topic's line 2"}))
	r.file("b.txt", edit)
	r.file("c.txt", edit)
	r.git("commit", "--quiet", "-m", "edit line 2")
	r.git("push", "--quiet", "--force", "origin", "topic")
	r.valley("ready", "--change", id, "topic")
	r.git("checkout", "--quiet", "main")
	r.git("fetch", "--quiet", "origin")
	v2 := r.git("rev-parse", "topic")
	kind, text := r.compare(id, 2, v2)
	wantPage(t, "a resolved conflict", kind, text, compareChanged, page(`mmmm v2 at {v2}: edited since you approved v1 at {v1}.
Main moved {b0} → {b1}.
1 of 1 commit changed, and 1 file differs from v1.
1 file in conflict with main:
  a.txt  {b1} reword line 2 of a

── what v2 changes beyond v1 carried onto main {b1} ──
  none: outside its conflicts with main, v2 is v1 carried onto main and nothing else.

── a.txt: CONFLICT (contents) ──
Removed: the conflict, main's side and v1's. Added: v2's resolution.
diff --git a/a.txt b/a.txt
index … 100644
--- a/a.txt
+++ b/a.txt
@@ -1,9 +1,5 @@
 line 1
-<<<<<<< `+b1+`
-main's line 2
-=======
-topic's line 2
->>>>>>> `+v1+`
+main's and topic's line 2
 line 3
 line 4
 line 5
`, "v1", v1, "v2", v2, "b0", b0, "b1", b1))
	if err := os.MkdirAll(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "git", "attributes"), filepath.Join(r.work, ".git", "info", "attributes")} {
		if err := os.WriteFile(f, []byte("* merge=union\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, union := r.compare(id, 2, v2)
	if union != text {
		t.Errorf("merge=union changed the page:\n%s", union)
	}
}

// A path in any other conflict shows each version's own diff of it, so an
// approved deletion the shown version does not make is in view, and a
// rename conflict names every path it involves.
func TestEditedPageShowsOtherConflicts(t *testing.T) {
	id := strings.Repeat("n", 32)
	r, _, _ := newCarryRepo(t, id)
	base := r.commitFiles("main", "add gone and r", map[string]string{"gone.txt": numbered(5, nil), "r.txt": numbered(8, nil)})
	r.git("checkout", "--quiet", "topic")
	r.git("rebase", "--quiet", "main")
	r.git("rm", "--quiet", "gone.txt")
	r.git("mv", "r.txt", "s.txt")
	r.git("commit", "--quiet", "--amend", "--no-edit")
	r.git("push", "--quiet", "--force", "origin", "topic")
	r.valley("ready", "--change", id, "topic")
	v1 := r.git("rev-parse", "topic")
	r.setRef(requestPrefix+"c-"+id, v1)
	r.git("checkout", "--quiet", "main")
	r.git("mv", "r.txt", "t.txt")
	b1 := r.commitFiles("main", "edit gone, rename r", map[string]string{"gone.txt": numbered(5, map[int]string{3: "main's line 3"})})
	r.git("checkout", "--quiet", "topic")
	r.git("reset", "--quiet", "--hard", b1)
	edit := numbered(20, map[int]string{2: "topic's line 2"})
	r.commitFiles("topic", "edit line 2", map[string]string{"a.txt": edit, "b.txt": edit, "c.txt": edit})
	r.valley("ready", "--change", id, "topic")
	r.git("checkout", "--quiet", "main")
	r.git("fetch", "--quiet", "origin")
	v2 := r.git("rev-parse", "topic")
	kind, text := r.compare(id, 3, v2)
	wantPage(t, "other conflicts", kind, text, compareChanged, page(`nnnn v3 at {v3}: edited since you approved v2 at {v2}.
Main moved {b0} → {b1}.
1 of 1 commit changed, and 3 files differ from v2.
4 files in conflict with main:
  gone.txt  {b1} edit gone, rename r
  r.txt     {b1} edit gone, rename r
  s.txt     not edited on main
  t.txt     {b1} edit gone, rename r

── what v3 changes beyond v2 carried onto main {b1} ──
  none: outside its conflicts with main, v3 is v2 carried onto main and nothing else.

── gone.txt: CONFLICT (modify/delete) ──
v2's diff against main {b0}, indented:
    diff --git a/gone.txt b/gone.txt
    deleted file mode 100644
    index …
    --- a/gone.txt
    +++ /dev/null
    @@ -1,5 +0,0 @@
    -line 1
    -line 2
    -line 3
    -line 4
    -line 5
── v3's diff of gone.txt against main {b1} ──
  none: v3 leaves it as main has it.

── r.txt: CONFLICT (rename/rename) ──
v2's diff against main {b0}, indented:
    diff --git a/r.txt b/r.txt
    deleted file mode 100644
    index …
    --- a/r.txt
    +++ /dev/null
    @@ -1,8 +0,0 @@
    -line 1
    -line 2
    -line 3
    -line 4
    -line 5
    -line 6
    -line 7
    -line 8
── v3's diff of r.txt against main {b1} ──
  none: v3 leaves it as main has it.

── s.txt: CONFLICT (rename/rename) ──
v2's diff against main {b0}, indented:
    diff --git a/s.txt b/s.txt
    new file mode 100644
    index …
    --- /dev/null
    +++ b/s.txt
    @@ -0,0 +1,8 @@
    +line 1
    +line 2
    +line 3
    +line 4
    +line 5
    +line 6
    +line 7
    +line 8
── v3's diff of s.txt against main {b1} ──
  none: v3 leaves it as main has it.

── t.txt: CONFLICT (rename/rename) ──
v2's diff against main {b0}, indented:
  none: v2 leaves it as main has it.
── v3's diff of t.txt against main {b1} ──
  none: v3 leaves it as main has it.
`, "v2", v1, "v3", v2, "b0", base, "b1", b1))
	// A note beside the approved version's indented diff, whose new side
	// is not the shown head, anchors to nothing.
	all := anchors([]byte(text))
	for n, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "    ") && anchorOf(all, n+1) != "(unanchored)" {
			t.Errorf("line %d, %q, anchors to %s", n+1, line, anchorOf(all, n+1))
		}
	}
}

// A message edited in one line of many shows as a line diff of the two;
// an author or an encoding that differs shows both in full.
func TestEditedMessagesShowAsALineDiff(t *testing.T) {
	id := strings.Repeat("o", 32)
	r, _, b0 := newCarryRepo(t, id)
	r.git("checkout", "--quiet", "topic")
	body := "edit line 2\n\n" + numbered(12, nil)
	v2 := r.amend(id, "topic", "-m", body)
	r.setRef(requestPrefix+"c-"+id, v2)
	r.git("checkout", "--quiet", "topic")
	v3 := r.amend(id, "topic", "-m", strings.Replace(body, "line 6\n", "line six\n", 1))
	kind, text := r.compare(id, 3, v3)
	wantPage(t, "a message", kind, text, compareClean, page(`oooo v3 at {v3}: edited since you approved v2 at {v2}, with the same file changes.
Commit messages edited since you approved v2. Read them before approving:
  commit 1 of 1, v2 {v2} → v3 {v3}:
     (4 unchanged lines)
     line 3
     line 4
     line 5
    -line 6
    +line six
     line 7
     line 8
     line 9
     (3 unchanged lines)
Its base is still {b0}.
`, "v2", v2, "v3", v3, "b0", b0))
	r.setRef(requestPrefix+"c-"+id, v3)
	r.git("checkout", "--quiet", "topic")
	v4 := r.amend(id, "topic", "--no-edit", "--author", "Someone Else <else@localhost>")
	kind, text = r.compare(id, 4, v4)
	date := strings.SplitN(r.git("log", "-1", "--format=%ad", "--date=raw", v4), "\n", 2)[0]
	wantPage(t, "an author", kind, text, compareClean, page(`oooo v4 at {v4}: edited since you approved v3 at {v3}, with the same file changes.
Commit authors edited since you approved v3:
  v3, commit 1 of 1, {v3}: compare-test <compare-test@localhost> `+r.git("log", "-1", "--format=%ad", "--date=raw", v3)+`
  v4, commit 1 of 1, {v4}: Someone Else <else@localhost> `+date+`
Its base is still {b0}.
`, "v3", v3, "v4", v4, "b0", b0))
	r.setRef(requestPrefix+"c-"+id, v4)
	r.git("checkout", "--quiet", "topic")
	r.git("config", "i18n.commitEncoding", "ISO-8859-1")
	v5 := r.amend(id, "topic", "--no-edit")
	r.git("config", "--unset", "i18n.commitEncoding")
	kind, text = r.compare(id, 5, v5)
	wantPage(t, "an encoding", kind, text, compareClean, page(`oooo v5 at {v5}: edited since you approved v4 at {v4}, with the same file changes.
Commit messages edited since you approved v4. Read them before approving:
  v4, commit 1 of 1, {v4}:
    edit line 2
    
    line 1
    line 2
    line 3
    line 4
    line 5
    line six
    line 7
    line 8
    line 9
    line 10
    line 11
    line 12
  v5, commit 1 of 1, {v5}:
    (encoding ISO-8859-1)
    edit line 2
    
    line 1
    line 2
    line 3
    line 4
    line 5
    line six
    line 7
    line 8
    line 9
    line 10
    line 11
    line 12
Its base is still {b0}.
`, "v4", v4, "v5", v5, "b0", b0))
}

// When the carry cannot be computed, the page lists every file whose
// change differs, then the range-diff and the shown version's diff.
func TestEditedPageFallsBack(t *testing.T) {
	id := strings.Repeat("p", 32)
	r, v1, b0 := newCarryRepo(t, id)
	b1 := r.mainEditsLine5()
	v2 := r.readyRebased(id, nil)
	t.Setenv("TMPDIR", filepath.Join(r.work, "absent"))
	kind, text := r.compare(id, 2, v2)
	wantPage(t, "no carry", kind, text, compareChanged, page(`pppp v2 at {v2}: edited since you approved v1 at {v1}.
Main moved {b0} → {b1}.
1 of 1 commit changed, and 3 files differ from v1:
  a.txt  {b1} edit line 5
  b.txt  {b1} edit line 5
  c.txt  {b1} edit line 5

The carry of v1 onto main {b1} could not be computed: no scratch git directory could be made.

── git range-diff {b0}..{v1} {b1}..{v2} ──
1:  {v1} ! 1:  {v2} edit line 2
    @@ a.txt
     +topic's line 2
      line 3
      line 4
    - line 5
    + main's line 5
     
      ## b.txt ##
     @@
    @@ b.txt
     +topic's line 2
      line 3
      line 4
    - line 5
    + main's line 5
     
      ## c.txt ##
     @@
    @@ c.txt
     +topic's line 2
      line 3
      line 4
    - line 5
    + main's line 5

── v2's diff of these 3 files against main {b1} ──
diff --git a/a.txt b/a.txt
index … 100644
--- a/a.txt
+++ b/a.txt
@@ -1,5 +1,5 @@
 line 1
-line 2
+topic's line 2
 line 3
 line 4
 main's line 5
diff --git a/b.txt b/b.txt
index … 100644
--- a/b.txt
+++ b/b.txt
@@ -1,5 +1,5 @@
 line 1
-line 2
+topic's line 2
 line 3
 line 4
 main's line 5
diff --git a/c.txt b/c.txt
index … 100644
--- a/c.txt
+++ b/c.txt
@@ -1,5 +1,5 @@
 line 1
-line 2
+topic's line 2
 line 3
 line 4
 main's line 5
`, "v1", v1, "v2", v2, "b0", b0, "b1", b1))
}

func TestConflictReportsParse(t *testing.T) {
	tree := strings.Repeat("a", 40)
	c, err := parseConflicts(tree + "\x00100644 " + strings.Repeat("b", 40) + " 2\ta.txt\x00\x00" +
		"1\x00a.txt\x00Auto-merging\x00Auto-merging a.txt\x00" +
		"1\x00a.txt\x00CONFLICT (contents)\x00Merge conflict in a.txt\x00" +
		"3\x00r.txt\x00s.txt\x00t.txt\x00CONFLICT (rename/rename)\x00r.txt renamed\x00" +
		"1\x00d\x00CONFLICT (file/directory)\x00d is a directory\x00")
	if err != nil || c.tree != tree || fmt.Sprint(c.conflicts) != "map[a.txt:[CONFLICT (contents)] d:[CONFLICT (file/directory)] r.txt:[CONFLICT (rename/rename)] s.txt:[CONFLICT (rename/rename)] t.txt:[CONFLICT (rename/rename)]]" {
		t.Fatalf("%v %v", c, err)
	}
	for _, bad := range []string{"", tree, tree + "\x00", tree + "\x00junk\x00", tree + "\x00\x002\x00a\x00T\x00m\x00", tree + "\x00\x00x\x00a\x00T\x00m\x00"} {
		if _, err := parseConflicts(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
