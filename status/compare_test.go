package main

import (
	"bytes"
	"io"
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
// first. An edited version lists the files whose change differs, the
// range-diff, and the shown version's diff of those files in git's
// ordinary form, whatever the person's diff configuration. A message or
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
			"1 of 1 commit changed, and 2 files differ from v2:\n  image.bin\n  tool.sh\n\n",
		"── git range-diff "+short(two)+".."+short(v2)+" "+short(two)+".."+short(v3)+" ──\n",
		"1:  "+short(v2)+" = 1:  "+short(v3)+" add the tool",
		"\n── v3's diff of these 2 files against main "+short(two)+" ──\ndiff --git a/image.bin b/image.bin\n",
		"Binary files /dev/null and b/image.bin differ\ndiff --git a/tool.sh b/tool.sh\nnew file mode 100755\n",
		"@@ -0,0 +1 @@\n+#!/bin/sh\n",
	)
	if kind != compareChanged || strings.Contains(text, "Commit messages") || strings.Contains(text, "v2: absent") {
		t.Fatalf("an edited commit: %s\n%s", kind, text)
	}
	_, verbose := r.compareVerbose(id, 3, v3)
	mustSay(t, "-v", verbose, "  image.bin\n    v2: absent -> 100644 ", "  tool.sh\n    v2: absent -> 100644 ", "    v3: absent -> 100755 ")

	// v4: the same files, with another message and another author.
	r.setRef(requestPrefix+"c-"+id, v3)
	r.git("checkout", "--quiet", "topic")
	v4 := r.amend(id, "topic", "-m", "add the tool, renamed", "--author", "Someone Else <else@localhost>")
	kind, text = r.compare(id, 4, v4)
	mustSay(t, "a changed message and author", text,
		": edited since you approved v3 at "+short(v3)+", with the same file changes.\n",
		"Commit messages edited since you approved v3. Read them before approving:\n"+
			"  v3, commit 1 of 1, "+short(v3)+":\n    add the tool\n"+
			"  v4, commit 1 of 1, "+short(v4)+":\n    add the tool, renamed\n",
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
// differs, the landings that edited it or none, then the range-diff.
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
			"1 of 1 commit changed, and 2 files differ from v2:\n"+
			"  new.txt     not edited on main\n"+
			"  shared.txt  "+short(again)+" edit line 18 again\n"+
			"\n── git range-diff "+short(b1)+".."+short(v2)+" "+short(b2)+".."+short(v3)+" ──\n",
		"1:  "+short(v3)+" edit line 2\n",
		"\n── v3's diff of these 2 files against main "+short(b2)+" ──\ndiff --git a/new.txt b/new.txt\n",
	)
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
