package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"the-valley/webui/export"
)

// fails runs one pass through run, the program's entry point, and fails the
// test unless the pass exits 1 having sent and written nothing: to rv when
// it is given, and otherwise to a fresh --out directory. It returns what
// the pass said on stderr.
func (v *testValley) fails(rv *testReceiver, more ...string) string {
	v.t.Helper()
	out := filepath.Join(v.t.TempDir(), "export")
	to := []string{"--out", out}
	if rv != nil {
		rv.taken()
		to = []string{"--receiver", rv.server.URL}
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), v.args(append(to, more...)...), &stdout, &stderr); code != 1 {
		v.t.Fatalf("the pass exited %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if rv != nil {
		sameRequests(v.t, "a failed pass", rv.taken())
	} else if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		v.t.Errorf("a failed pass wrote %s", out)
	}
	return stderr.String()
}

func mustSay(t *testing.T, what, said, want string) {
	t.Helper()
	if !strings.Contains(said, want) {
		t.Errorf("%s: the pass did not say %q:\n%s", what, want, said)
	}
}

// elsewhere makes a second instance beside the first, with projects of the
// same names: valley, which allows app and site; app, which declares
// itself public and carries a sentinel of its own; and site, with no main.
// It returns the instance repository.
func (v *testValley) elsewhere(sentinel string) string {
	v.t.Helper()
	srv := filepath.Join(v.root, "elsewhere")
	for _, name := range []string{"valley", "app", "site"} {
		v.run("", "git", "init", "--quiet", "--bare", filepath.Join(srv, name+".git"))
	}
	for _, name := range []string{"valley", "app"} {
		work := filepath.Join(v.root, "elsewhere-work", name)
		v.run("", "git", "init", "--quiet", work)
		v.run(work, "git", "remote", "add", "origin", filepath.Join(srv, name+".git"))
		v.work["elsewhere/"+name] = work
	}
	v.write(filepath.Join(v.work["elsewhere/valley"], "policy", "instance", "floor.cue"), "package verification\n")
	v.declare("elsewhere/valley", `instance: projects: {
	app: visibility: "public"
	site: visibility: "public"
}`)
	v.write(filepath.Join(v.work["elsewhere/app"], "policy", "valley"), filepath.Join(srv, "valley.git")+"\n")
	v.write(filepath.Join(v.work["elsewhere/app"], "source.txt"), sentinel+"\n")
	v.declare("elsewhere/app", `project: visibility: "public"`)
	list := v.runInput("", "app\nsite\nvalley\n", "git", "-C", filepath.Join(srv, "valley.git"), "hash-object", "-w", "--stdin")
	v.run("", "git", "-C", filepath.Join(srv, "valley.git"), "update-ref", projectsRef, strings.TrimSpace(list))
	return filepath.Join(srv, "valley.git")
}

// Two instances publish a project called app. When the instance repository
// the publisher is given points at the other with policy/valley, the CLI
// would read the other's app, and the pass refuses before it reads any
// project.
func TestAnotherInstanceIsNeverRead(t *testing.T) {
	v := newValley(t)
	other := v.elsewhere("elsewhere-app-source-c3d1")
	v.write(filepath.Join(v.work["valley"], "policy", "valley"), other+"\n")
	v.commit("valley", "valley: point at another instance")

	mustSay(t, "--out", v.fails(nil), "holds policy/valley on main")
	rv := newReceiver(t, v)
	mustSay(t, "--receiver", v.fails(rv), "holds policy/valley on main")
	if _, err := os.Stat(filepath.Join(v.root, "cache", "valley-web-publish", "repos", "app.git")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the pass fetched a project before it refused the instance repository")
	}
}

// A policy/valley that lands on the instance repository's main while the
// CLI reads is caught: the read is confirmed against main once it is done,
// and the confirmation refuses the pointer. Without it, the other
// instance's app would be published.
func TestAPointerLandingDuringAReadIsCaught(t *testing.T) {
	v := newValley(t)
	other := v.elsewhere("elsewhere-app-source-c3d1")
	work := v.work["valley"]
	v.run(work, "git", "checkout", "--quiet", "-b", "pointer")
	v.write(filepath.Join(work, "policy", "valley"), other+"\n")
	v.run(work, "git", "add", "--all")
	v.run(work, "git", "commit", "--quiet", "-m", "valley: point at another instance")
	pointer := strings.TrimSpace(v.run(work, "git", "rev-parse", "HEAD"))
	v.run(work, "git", "push", "--quiet", "origin", "pointer")

	// The CLI's first command moves main to the pointer before it runs.
	wrapper := filepath.Join(v.root, "bin", "valley")
	data, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	shebang, rest, _ := strings.Cut(string(data), "\n")
	moved := filepath.Join(v.root, "moved")
	v.write(wrapper, fmt.Sprintf("%s\nif [ ! -e %s ]; then : > %s; git -C %s update-ref refs/heads/main %s; fi\n%s",
		shebang, moved, moved, filepath.Join(v.srv, "valley.git"), pointer, rest))

	mustSay(t, "a pointer landing during the read", v.fails(nil), "holds policy/valley on main")
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("the CLI never ran, so the pointer was refused before the read: %v", err)
	}
}

// A project the instance allows that cannot be read fails the pass, which
// sends and writes nothing: published without it, the export would take
// it off the web. A project with no main yet is private, and the pass
// goes on.
func TestAProjectThatCannotBeReadFailsThePass(t *testing.T) {
	cases := []struct {
		name   string
		upload bool
		breaks func(v *testValley) []string
		says   string
	}{
		{
			name:   "the pending listing cannot read site",
			upload: true,
			breaks: func(v *testValley) []string {
				if err := os.Rename(filepath.Join(v.srv, "site.git"), filepath.Join(v.srv, "site.moved")); err != nil {
					v.t.Fatal(err)
				}
				return nil
			},
			says: "site: the pending listing could not read it",
		},
		{
			name: "site's main cannot be fetched into its mirror",
			breaks: func(v *testValley) []string {
				mirror := filepath.Join(v.root, "cache", "valley-web-publish", "repos", "site.git")
				v.run("", "git", "init", "--quiet", "--bare", mirror)
				v.run("", "git", "-C", mirror, "config", "core.repositoryformatversion", "99")
				return nil
			},
			says: "site: could not read main",
		},
		{
			name: "discovery fails, and the CLI lists its own repository alone",
			breaks: func(v *testValley) []string {
				list := v.runInput("", "app\nsite\nvalley\nnot a name\n", "git", "-C", filepath.Join(v.srv, "valley.git"), "hash-object", "-w", "--stdin")
				v.run("", "git", "-C", filepath.Join(v.srv, "valley.git"), "update-ref", projectsRef, strings.TrimSpace(list))
				return nil
			},
			says: "valley plan --all lists valley, and the instance repository publishes app, not a name, site, valley",
		},
		{
			name: "the instance publishes no project list",
			breaks: func(v *testValley) []string {
				v.run("", "git", "-C", filepath.Join(v.srv, "valley.git"), "update-ref", "-d", projectsRef)
				return nil
			},
			says: "could not read main and refs/the-valley/projects in the instance repository",
		},
		{
			name:   "the schema cannot be read",
			breaks: func(v *testValley) []string { return []string{"--schema", filepath.Join(v.root, "no-such.cue")} },
			says:   "does not load",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := newValley(t)
			more := c.breaks(v)
			var rv *testReceiver
			if c.upload {
				rv = newReceiver(t, v)
			}
			mustSay(t, c.name, v.fails(rv, more...), c.says)
		})
	}

	t.Run("a project with no main is private", func(t *testing.T) {
		v := newValley(t)
		v.run("", "git", "init", "--quiet", "--bare", filepath.Join(v.srv, "fresh.git"))
		list := v.runInput("", "app\nfresh\nsite\nvalley\n", "git", "-C", filepath.Join(v.srv, "valley.git"), "hash-object", "-w", "--stdin")
		v.run("", "git", "-C", filepath.Join(v.srv, "valley.git"), "update-ref", projectsRef, strings.TrimSpace(list))
		v.declare("valley", `instance: projects: {
	app: visibility: "public"
	fresh: visibility: "public"
	site: visibility: "public"
}`)
		stdout, _ := v.publish("--out", filepath.Join(t.TempDir(), "export"))
		mustSay(t, "a project with no main", stdout, "public: app; private: fresh, site, valley")
	})
}

// The bundles of an export are held to --max-bundles together, and the
// state and the cache to --max-storage, before anything is sent. What a
// program prints is held to a bound: git's output past it fails the call,
// and a declaration whose export is past cue's bound is invalid.
func TestBoundsStopThePass(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	mustSay(t, "--max-bundles", v.fails(rv, "--max-bundles", "100"), "more than --max-bundles 100")
	mustSay(t, "--max-storage", v.fails(nil, "--max-storage", "1K"), "more than --max-storage 1024")
	for _, size := range []string{"0", "-1", "1X", "K", "9000000000T"} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), v.args("--out", t.TempDir(), "--max-bundles", size), &stdout, &stderr); code != 2 {
			t.Errorf("--max-bundles %s: the pass exited %d, want 2", size, code)
		}
	}

	big := v.runInput("", strings.Repeat("x", maxGitOutput+1), "git", "-C", filepath.Join(v.srv, "app.git"), "hash-object", "-w", "--stdin")
	if _, err := git(context.Background(), filepath.Join(v.srv, "app.git"), "cat-file", "blob", strings.TrimSpace(big)); !errors.Is(err, errTooLong) {
		t.Errorf("git printing more than %d bytes returned %v, want errTooLong", maxGitOutput, err)
	}

	v.declare("valley", `import "list"

instance: projects: {for i in list.Range(0, 9000, 1) {"project-with-a-long-name-for-the-bound-on-cue-output-\(i)": visibility: "public"}}`)
	stdout, stderr := v.publish("--out", filepath.Join(t.TempDir(), "export"))
	mustSay(t, "a declaration past cue's bound", stdout, "public: none")
	mustSay(t, "a declaration past cue's bound", stderr, "cue exported more than")
}

// objects is every object a repository holds, decompressed, as git
// cat-file --batch-all-objects prints them.
func (v *testValley) objects(repo string) string {
	v.t.Helper()
	return v.run("", "git", "-C", repo, "cat-file", "--batch-all-objects", "--batch")
}

// unbundled is every object a bundle carries, decompressed.
func (v *testValley) unbundled(bundle string) string {
	v.t.Helper()
	repo := filepath.Join(v.t.TempDir(), "unbundled.git")
	v.run("", "git", "init", "--quiet", "--bare", repo)
	v.run("", "git", "-C", repo, "bundle", "unbundle", bundle)
	return v.objects(repo)
}

// noSentinels fails the test if any private sentinel appears in what.
func noSentinels(t *testing.T, where, what string) {
	t.Helper()
	for _, s := range []string{sentinelSource, sentinelBody, sentinelComment} {
		if strings.Contains(what, s) {
			t.Errorf("%s holds the private sentinel %s", where, s)
		}
	}
}

// inActive runs f over the generation the receiver serves.
func (rv *testReceiver) inActive(f func(export.Generation)) {
	rv.t.Helper()
	rv.mu.Lock()
	defer rv.mu.Unlock()
	g, release, err := rv.store.Open()
	defer release()
	if err != nil {
		rv.t.Fatal(err)
	}
	f(g)
}

// No private sentinel leaves the host: not in export.json, and not in any
// object of any bundle, whether the export is written to --out or
// uploaded and activated by a receiver.
func TestPrivateSentinelsNeverLeave(t *testing.T) {
	v := newValley(t)
	held := v.objects(filepath.Join(v.srv, "site.git"))
	for _, s := range []string{sentinelSource, sentinelBody, sentinelComment} {
		if !strings.Contains(held, s) {
			t.Fatalf("site's repository does not hold the sentinel %s, so the test proves nothing", s)
		}
	}

	dir, _, _ := v.out()
	data, err := os.ReadFile(filepath.Join(dir, export.ExportFile))
	if err != nil {
		t.Fatal(err)
	}
	noSentinels(t, "--out's export.json", string(data))
	bundles, _ := filepath.Glob(filepath.Join(dir, "bundles", "*"))
	if len(bundles) != 1 {
		t.Fatalf("--out holds the bundles %v, want app's alone", bundles)
	}
	noSentinels(t, "--out's bundle of app", v.unbundled(bundles[0]))

	rv := newReceiver(t, v)
	v.publish("--receiver", rv.server.URL)
	rv.inActive(func(g export.Generation) {
		data, err := os.ReadFile(g.ExportPath())
		if err != nil {
			t.Fatal(err)
		}
		noSentinels(t, "the receiver's export.json", string(data))
		repos, _ := filepath.Glob(filepath.Join(g.Dir, "repos", "*"))
		if len(repos) != 1 {
			t.Fatalf("the receiver serves the repositories %v, want app's alone", repos)
		}
		noSentinels(t, "the receiver's repository of app", v.objects(repos[0]))
	})
}

// A project that declares itself private again is gone from the next
// export the receiver activates: from export.json and from its
// repositories.
func TestAProjectMadePrivateLeavesTheReceiver(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	v.declare("site", `project: visibility: "public"`)
	v.publish("--receiver", rv.server.URL)
	if got := names(rv.active()); !reflect.DeepEqual(got, []string{"app", "site"}) {
		t.Fatalf("the receiver serves %v, want app and site", got)
	}

	v.declare("site", `project: visibility: "private"`)
	stdout, _ := v.publish("--receiver", rv.server.URL)
	mustSay(t, "site made private", stdout, "public: app; private: site, valley")
	if got := names(rv.active()); !reflect.DeepEqual(got, []string{"app"}) {
		t.Errorf("the receiver serves %v once site is private, want app alone", got)
	}
	rv.inActive(func(g export.Generation) {
		repo, _ := g.Repo("site")
		if _, err := os.Stat(repo); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the receiver's active generation still holds site's repository")
		}
		data, err := os.ReadFile(g.ExportPath())
		if err != nil {
			t.Fatal(err)
		}
		noSentinels(t, "the receiver's export.json once site is private", string(data))
	})
}

// landedChangeID is a change of app's that the integrator lands as a new
// commit, so only its transfer statement shows it landed.
const landedChangeID = "kpqrstuvwxyzkpqrstuvwxyzkpqrstuv"

// publishSigners publishes, in the instance repository, the signers a
// client verifies the integrator's landings with, as the host's signers
// publisher writes them: a tree at refs/the-valley/signers holding the
// known-signers file and the integrator's name. The integrator's key is
// made here; its verifier line is the file's one line.
func (v *testValley) publishSigners(name string) (key string) {
	v.t.Helper()
	key = filepath.Join(v.root, "integrator.key")
	v.run("", "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "integrator", "-f", key)
	instance := filepath.Join(v.srv, "valley.git")
	keys := v.runInput("", v.run("", "attest", "key", "--key", key, "--name", name), "git", "-C", instance, "hash-object", "-w", "--stdin")
	named := v.runInput("", name, "git", "-C", instance, "hash-object", "-w", "--stdin")
	tree := v.runInput("", fmt.Sprintf("100644 blob %s\tintegrator-name\n100644 blob %s\tknown-signers\n",
		strings.TrimSpace(named), strings.TrimSpace(keys)), "git", "-C", instance, "mktree")
	v.run("", "git", "-C", instance, "update-ref", "refs/the-valley/signers", strings.TrimSpace(tree))
	return key
}

// landByIntegration readies a change on app and lands it as the
// integrator does: a new commit on main, the outcome record, and the
// transfer statement, signed with key under name.
func (v *testValley) landByIntegration(key, name string) {
	v.t.Helper()
	work, bare := v.work["app"], filepath.Join(v.srv, "app.git")
	v.run(work, "git", "checkout", "--quiet", "-b", "landed", "main")
	v.write(filepath.Join(work, "landed.txt"), "landed\n")
	v.run(work, "git", "add", "landed.txt")
	v.run(work, "git", "commit", "--quiet", "-m", "app: a change the integrator lands")
	head := strings.TrimSpace(v.run(work, "git", "rev-parse", "HEAD"))
	v.run(work, "git", "push", "--quiet", "origin", "landed")
	v.valley("app", "ready", "--change", landedChangeID, "landed")
	// main moves on first, so the landing is a new commit and never the
	// change's head.
	v.run(work, "git", "checkout", "--quiet", "main")
	v.write(filepath.Join(work, "moved.txt"), "moved\n")
	v.commit("app", "app: main moves on")
	tip := strings.TrimSpace(v.run(work, "git", "rev-parse", "main"))
	v.run(work, "git", "checkout", "--quiet", "-b", "landing", "main")
	v.run(work, "git", "cherry-pick", head)
	landed := strings.TrimSpace(v.run(work, "git", "rev-parse", "HEAD"))
	v.run(work, "git", "push", "--quiet", "origin", "landing:main")
	v.run(work, "git", "checkout", "--quiet", "main")
	v.run(work, "git", "pull", "--quiet", "--ff-only", "origin", "main")

	request := "c-" + landedChangeID
	memo := v.runInput("", fmt.Sprintf("%s %s 0123456789abcdef land\n", head, tip), "git", "-C", bare, "hash-object", "-w", "--stdin")
	v.run("", "git", "-C", bare, "update-ref", "refs/the-valley/integration-outcomes/main/"+request, strings.TrimSpace(memo))
	digest := func(rev string) string {
		out := strings.TrimSpace(v.run("", "attest", "digest", "--repo", work, "--rev", rev))
		_, d, _ := strings.Cut(out, ":")
		return d
	}
	statement, err := json.Marshal(map[string]any{
		"statementType": "the-valley/attestation/v1",
		"subject": map[string]any{"primary": "valley-tree-v1",
			"digest": map[string]string{"valley-tree-v1": digest(landed), "git-sha1": landed}},
		"predicateType": "the-valley/integration/transfer/v1",
		"predicate": map[string]any{"change": request, "target": "refs/heads/main",
			"base":   map[string]string{"valley-tree-v1": digest(head)},
			"policy": map[string]string{"sha256": strings.Repeat("ab", 32), "git-sha1": tip}, "required": "none"},
	})
	if err != nil {
		v.t.Fatal(err)
	}
	path := filepath.Join(v.root, "statement.json")
	v.write(path, string(statement))
	note := v.runInput("", v.run("", "attest", "render", path), "attest", "sign", "--key", key, "--name", name)
	blob := v.runInput("", note, "git", "-C", bare, "hash-object", "-w", "--stdin")
	tree := v.runInput("", "100644 blob "+strings.TrimSpace(blob)+"\ttransfer.note\n", "git", "-C", bare, "mktree")
	v.run("", "git", "-C", bare, "update-ref",
		fmt.Sprintf("refs/the-valley/attestations/%s/%08x", digest(landed), len(name)), strings.TrimSpace(tree))
}

// The CLI the publisher runs verifies a landing with the signers the
// instance repository publishes, and with nothing the caller's environment
// names: a landed change is not exported as ready for approval. Without
// the publication the landing is not believed, and the change is.
func TestLandingsAreVerifiedWithThePublishedSigners(t *testing.T) {
	v := newValley(t)
	needTool(t, "attest")
	needTool(t, "ssh-keygen")
	const signer = "valley-host/integration"
	key := v.publishSigners(signer)
	v.landByIntegration(key, signer)

	// The caller's own material names the signer with another key, under
	// which the statement would not verify.
	other := filepath.Join(v.root, "other.key")
	v.run("", "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "other", "-f", other)
	otherSigners := filepath.Join(v.root, "other-signers")
	v.write(otherSigners, v.run("", "attest", "key", "--key", other, "--name", signer))
	t.Setenv("VALLEY_KNOWN_SIGNERS", otherSigners)
	t.Setenv("VALLEY_INTEGRATOR_SIGNER", signer)

	changes := func() []string {
		_, _, e := v.out()
		ids := []string{}
		for _, c := range e.Projects[0].Changes {
			ids = append(ids, c.ChangeID)
		}
		return ids
	}
	if got := changes(); !reflect.DeepEqual(got, []string{changeID}) {
		t.Errorf("with the published signers, app's changes ready for approval are %v, want %s alone", got, changeID)
	}
	v.run("", "git", "-C", filepath.Join(v.srv, "valley.git"), "update-ref", "-d", "refs/the-valley/signers")
	got := changes()
	if len(got) != 2 || !strings.Contains(strings.Join(got, " "), landedChangeID) {
		t.Errorf("with no published signers, app's changes ready for approval are %v, want the landed change among them", got)
	}
}
