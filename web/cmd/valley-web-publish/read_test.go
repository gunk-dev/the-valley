package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"the-valley/web/export"
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
		to = rv.flags()
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
// same names: valley, whose registry grants anyone read of app and site;
// app, which lets anyone read it and carries a sentinel of its own; and
// site, with no main.
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
	v.grantIn("elsewhere/valley", anyoneReadsAppAndSite)
	v.write(filepath.Join(v.work["elsewhere/app"], "policy", "valley"), filepath.Join(srv, "valley.git")+"\n")
	v.write(filepath.Join(v.work["elsewhere/app"], "source.txt"), sentinel+"\n")
	v.declare("elsewhere/app", policy("app", "anyone"))
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

	// The CLI reads policy/valley up to a # and without the spaces around
	// it, so an instance repository whose URL holds them is refused before
	// the pass runs: policy/valley would name another.
	instance := filepath.Join(v.srv, "valley.git")
	for _, url := range []string{"relative.git", "https://example.org/valley.git", instance + "#other", " " + instance, instance + "\n" + other} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), v.args("--out", t.TempDir(), "--instance-repo", url), &stdout, &stderr); code != 2 {
			t.Errorf("--instance-repo %q: the pass exited %d, want 2", url, code)
		}
	}
}

// The pass reads the instance repository at the main it began with, and
// the CLI never reads that main again. So a policy/valley that lands on
// main while the CLI reads changes nothing in the read: not when main is
// put back before the read ends, which no comparison of main before and
// after the read could see, and not when it stays, which the next pass
// refuses. Read through the pointer, the export would hold the other
// instance's app.
func TestAPointerLandingDuringAReadIsNeverRead(t *testing.T) {
	const otherSource = "elsewhere-app-source-c3d1"
	for _, restored := range []bool{true, false} {
		t.Run(fmt.Sprintf("restored %v", restored), func(t *testing.T) {
			v := newValley(t)
			other := v.elsewhere(otherSource)
			instance := filepath.Join(v.srv, "valley.git")
			began := strings.TrimSpace(v.run("", "git", "-C", instance, "rev-parse", "main"))
			work := v.work["valley"]
			v.run(work, "git", "checkout", "--quiet", "-b", "pointer")
			v.write(filepath.Join(work, "policy", "valley"), other+"\n")
			v.run(work, "git", "add", "--all")
			v.run(work, "git", "commit", "--quiet", "-m", "valley: point at another instance")
			pointer := strings.TrimSpace(v.run(work, "git", "rev-parse", "HEAD"))
			v.run(work, "git", "push", "--quiet", "origin", "pointer")

			// The CLI's first command moves main to the pointer before it
			// runs. When restored, main is put back once the second, the
			// pending listing, is done: the CLI has then read the pointer
			// twice, if it reads main at all, and main is where the pass
			// began.
			wrapper := filepath.Join(v.root, "bin", "valley")
			data, err := os.ReadFile(wrapper)
			if err != nil {
				t.Fatal(err)
			}
			shebang, rest, _ := strings.Cut(string(data), "\n")
			rest = strings.Replace(rest, "exec ", "", 1)
			count := filepath.Join(v.root, "count")
			restore := ":"
			if restored {
				restore = fmt.Sprintf("git -C %s update-ref refs/heads/main %s", instance, began)
			}
			v.write(wrapper, fmt.Sprintf(`%s
n=$(( $(cat %[2]s 2>/dev/null || echo 0) + 1 )); echo "$n" > %[2]s
if [ "$n" = 1 ]; then git -C %[3]s update-ref refs/heads/main %[4]s; fi
rc=0
%[5]s || rc=$?
if [ "$n" = 2 ]; then %[6]s; fi
exit "$rc"
`, shebang, count, instance, pointer, strings.TrimSpace(rest), restore))

			dir, _, e := v.out()
			if calls, _ := strconv.Atoi(strings.TrimSpace(v.run("", "cat", count))); calls < 2 {
				t.Fatalf("the CLI ran %d commands, so the pointer was never in place while it read", calls)
			}
			if got := names(e); !reflect.DeepEqual(got, []string{"app"}) {
				t.Fatalf("the export holds %v, want app alone", got)
			}
			app := strings.TrimSpace(v.run("", "git", "-C", filepath.Join(v.srv, "app.git"), "rev-parse", "main"))
			if e.Projects[0].SourceTip != app {
				t.Errorf("the export's app is at %s, and this instance's app is at %s", e.Projects[0].SourceTip, app)
			}
			if strings.Contains(v.unbundled(filepath.Join(dir, "bundles", "app.bundle")), otherSource) {
				t.Errorf("the bundle of app holds the other instance's source")
			}
			if len(knotOf(t, e.Projects[0], v.knots["bug"]).Comments) != 1 {
				t.Errorf("the export's app does not hold this instance's plan")
			}

			if restored {
				return
			}
			v.write(wrapper, shebang+"\n"+rest)
			mustSay(t, "the next pass", v.fails(nil), "holds policy/valley on main")
		})
	}
}

// A project a read grant to anyone names that cannot be read fails the
// pass, which sends and writes nothing: published without it, the export
// would take it off the web. A project with no main yet is private, and the pass
// goes on.
func TestAProjectThatCannotBeReadFailsThePass(t *testing.T) {
	cases := []struct {
		name   string
		upload bool
		breaks func(v *testValley) []string
		says   string
	}{
		{
			// The policy path reads site's policy before any content
			// work, finds it cannot, and fails the pass once it has sent
			// what it could check.
			name:   "site cannot be read",
			upload: true,
			breaks: func(v *testValley) []string {
				if err := os.Rename(filepath.Join(v.srv, "site.git"), filepath.Join(v.srv, "site.moved")); err != nil {
					v.t.Fatal(err)
				}
				return nil
			},
			says: "the policy check is incomplete: site: could not read main",
		},
		{
			name: "a project mirror cannot be staged",
			breaks: func(v *testValley) []string {
				// A file where the parent directory belongs prevents staging.
				v.write(filepath.Join(v.root, "cache", "valley-web-publish", "repos"), "blocked")
				return nil
			},
			says: "app: could not read main",
		},
		{
			name: "discovery fails, and the CLI lists its own repository alone",
			breaks: func(v *testValley) []string {
				list := v.runInput("", "app\nsite\nvalley\nnot a name\n", "git", "-C", filepath.Join(v.srv, "valley.git"), "hash-object", "-w", "--stdin")
				v.run("", "git", "-C", filepath.Join(v.srv, "valley.git"), "update-ref", projectsRef, strings.TrimSpace(list))
				return nil
			},
			says: "valley plan --all lists _pointer, and the instance repository publishes app, not a name, site, valley",
		},
		{
			// With no project list, git rev-parse would read a tag named
			// refs/tags/refs/the-valley/projects as the list. The tag
			// holds the list the instance published, which reads as a
			// project list, so only the exact read refuses it.
			name: "the instance publishes no project list",
			breaks: func(v *testValley) []string {
				instance := filepath.Join(v.srv, "valley.git")
				list := strings.TrimSpace(v.run("", "git", "-C", instance, "show-ref", "--verify", "--hash", projectsRef))
				v.run("", "git", "-C", instance, "update-ref", "-d", projectsRef)
				v.run("", "git", "-C", instance, "update-ref", "refs/tags/"+projectsRef, list)
				return nil
			},
			says: "the instance repository publishes no refs/the-valley/projects",
		},
		{
			// With no main, git rev-parse would read a branch named
			// refs/heads/refs/heads/main, here the instance's own main,
			// as main.
			name: "the instance has no main",
			breaks: func(v *testValley) []string {
				instance := filepath.Join(v.srv, "valley.git")
				v.run("", "git", "-C", instance, "update-ref", "refs/heads/refs/heads/main", "refs/heads/main")
				v.run("", "git", "-C", instance, "update-ref", "-d", "refs/heads/main")
				return nil
			},
			says: filepath.Join("SRV", "valley.git") + " holds no main, so it is not an instance repository",
		},
		{
			name:   "the read schema cannot be read",
			breaks: func(v *testValley) []string { return []string{"--read-schema", filepath.Join(v.root, "no-such.cue")} },
			says:   "does not load",
		},
		{
			name: "the identity schema cannot be read",
			breaks: func(v *testValley) []string {
				return []string{"--identity-schema", filepath.Join(v.root, "no-such.cue")}
			},
			says: "does not load",
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
			mustSay(t, c.name, v.fails(rv, more...), strings.ReplaceAll(c.says, "SRV", v.srv))
		})
	}

	// The grants are read before any project is: the CLI's clones are
	// made for the projects a grant names, and the CLI can make no other.
	// So a project no grant names is never read, and one that cannot be
	// read fails no pass.
	t.Run("a project no grant names that cannot be read is private", func(t *testing.T) {
		v := newValley(t)
		appAlone := `{app: "` + identities["app"] + `"}`
		v.write(filepath.Join(v.work["valley"], "identity", "registry.cue"), registryFor(appAlone, appAlone))
		v.commit("valley", "valley: grant app alone")
		if err := os.Rename(filepath.Join(v.srv, "site.git"), filepath.Join(v.srv, "site.moved")); err != nil {
			t.Fatal(err)
		}
		stdout, _ := v.publish("--out", filepath.Join(t.TempDir(), "export"))
		mustSay(t, "site cannot be read, and no grant names it", stdout, "exported: app; not exported: site, valley")
		clones, err := filepath.Glob(filepath.Join(v.root, "cache", "valley", "repos", "*-site"))
		if err != nil || len(clones) != 0 {
			t.Errorf("the CLI has a clone of site, which no grant names: %v %v", clones, err)
		}
	})

	t.Run("a project with no main is private", func(t *testing.T) {
		v := newValley(t)
		v.run("", "git", "init", "--quiet", "--bare", filepath.Join(v.srv, "fresh.git"))
		list := v.runInput("", "app\nfresh\nsite\nvalley\n", "git", "-C", filepath.Join(v.srv, "valley.git"), "hash-object", "-w", "--stdin")
		v.run("", "git", "-C", filepath.Join(v.srv, "valley.git"), "update-ref", projectsRef, strings.TrimSpace(list))
		v.grant(`"all"`)
		stdout, _ := v.publish("--out", filepath.Join(t.TempDir(), "export"))
		mustSay(t, "a project with no main", stdout, "exported: app; not exported: fresh, site, valley")
	})
}

// Only a cue run that ends by refusing a document makes it invalid, and so
// private. A cue that is killed, that crashes or that runs past its timeout
// has read nothing, and the pass fails, sending and writing nothing: taken
// as invalid, the instance's registry would make every project private and
// take them all off the web.
func TestACueThatDoesNotFinishFailsThePass(t *testing.T) {
	cases := []struct {
		name, does, says string
		timeout          time.Duration
	}{
		{name: "killed", does: "kill -KILL $$", says: "cue did not finish reading the document: signal: killed"},
		{name: "crashed", does: "echo 'panic: runtime error' >&2; exit 2",
			says: "cue did not finish reading the document: exit status 2: panic: runtime error"},
		{name: "past its timeout", does: "exec sleep 60", says: "cue took longer than 3s", timeout: 3 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := newValley(t)
			bash, cue := needTool(t, "bash"), needTool(t, "cue")
			// Each schema is checked with cue over it alone; a document is
			// read with cue over its schema and its files.
			wrapper := filepath.Join(v.root, "bin", "cue")
			v.write(wrapper, fmt.Sprintf("#!%s\nif [ \"$#\" -gt 4 ]; then %s; fi\nexec %s \"$@\"\n", bash, c.does, cue))
			if err := os.Chmod(wrapper, 0o755); err != nil {
				t.Fatal(err)
			}
			if c.timeout != 0 {
				was := cueTimeout
				cueTimeout = c.timeout
				t.Cleanup(func() { cueTimeout = was })
			}
			said := v.fails(nil)
			mustSay(t, c.name, said, "could not read the instance's identity registry")
			mustSay(t, c.name, said, c.says)
		})
	}
}

// The bundles of an export are held to --max-bundles together, and the
// state and the cache to --max-storage, before anything is sent. What a
// program prints is held to a bound: git's output past it fails the call,
// and a policy whose export is past cue's bound is invalid.
func TestBoundsStopThePass(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	mustSay(t, "--max-bundles", v.fails(rv, "--max-bundles", "100"), "app: its bundle is larger than the 100 bytes --max-bundles 100 leaves for it")
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

	v.declare("app", `import "list"

project: "app"
identity: "`+identities["app"]+`"
readers: {for i in list.Range(0, 20000, 1) {"reader-with-a-long-name-for-the-bound-on-cue-output-\(i)": true}}`)
	stdout, stderr := v.publish("--out", filepath.Join(t.TempDir(), "export"))
	mustSay(t, "a policy past cue's bound", stdout, "exported: none")
	mustSay(t, "a policy past cue's bound", stderr, "cue exported more than")
}

// du is what the regular files under dirs hold together. A file that goes
// while it is counted is not counted.
func du(dirs ...string) int64 {
	var total int64
	for _, dir := range dirs {
		filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if info, err := d.Info(); err == nil {
					total += info.Size()
				}
			}
			return nil
		})
	}
	return total
}

// peakSize runs f, and returns the most the files under dirs held together
// at any moment it looked while f ran. It looks again as soon as it has
// looked.
func peakSize(dirs []string, f func()) int64 {
	done, peak := make(chan struct{}), make(chan int64)
	go func() {
		var most int64
		for {
			most = max(most, du(dirs...))
			select {
			case <-done:
				peak <- most
				return
			default:
			}
		}
	}()
	f()
	close(done)
	return <-peak
}

// A pass over its budget writes no more than the budget, and leaves no
// bundle behind. A bundle is written only into what --max-bundles and
// --max-storage leave, and its writing stops there. A pass whose state and
// cache are past --max-storage stops before it fetches anything, and that
// holds for a pass that would send nothing because the receiver
// acknowledged its digest.
func TestABundlePastTheBudgetIsNeverWritten(t *testing.T) {
	v := newValley(t)
	cache := filepath.Join(v.root, "cache")
	bundles := filepath.Join(cache, "valley-web-publish", "bundles")
	// What --max-storage bounds: the state directory, the publisher's
	// cache, and the CLI's.
	storage := []string{v.state, filepath.Join(cache, "valley-web-publish"), filepath.Join(cache, "valley")}
	mustSay(t, "--max-storage 1", v.fails(nil, "--max-storage", "1"), "more than --max-storage 1,")
	if _, err := os.Stat(filepath.Join(cache, "valley-web-publish")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a pass whose state was past --max-storage fetched before it stopped")
	}

	// app's history grows by 4 MiB that do not compress, so its bundle is
	// past every budget below.
	noise := make([]byte, 4<<20)
	if _, err := rand.Read(noise); err != nil {
		t.Fatal(err)
	}
	v.write(filepath.Join(v.work["app"], "noise"), string(noise))
	v.commit("app", "app: a large history")

	// A cold cache with room for less than one shared repository refuses
	// before writing it. The source history is much larger than the budget.
	coldBudget := du(storage...) + 128
	var coldSaid string
	coldPeak := peakSize(storage, func() {
		coldSaid = v.fails(nil, "--max-storage", strconv.FormatInt(coldBudget, 10))
	})
	mustSay(t, "cold cache", coldSaid, "more than --max-storage")
	if coldPeak > coldBudget {
		t.Fatalf("cold cache reached %d bytes with budget %d", coldPeak, coldBudget)
	}
	if entries, _ := os.ReadDir(filepath.Join(cache, "valley-web-publish")); len(entries) != 0 {
		t.Fatalf("cold refusal left partial data: %v", entries)
	}

	var said string
	peak := peakSize(storage, func() { said = v.fails(nil, "--max-bundles", "64K", "--max-storage", "256K") })
	mustSay(t, "--max-bundles", said, "app: its bundle is larger than the 65536 bytes --max-bundles 65536 leaves for it")
	if peak > 256<<10 {
		t.Errorf("the cold pass held %d bytes, more than --max-storage 262144", peak)
	}
	if left, _ := os.ReadDir(bundles); len(left) != 0 {
		t.Errorf("the refused pass left %v behind in the bundles", left)
	}

	publisherCache := filepath.Join(cache, "valley-web-publish")
	if size := du(storage...); size > int64(len(noise))/16 {
		t.Fatalf("shared cache grew to %d bytes for a %d-byte history", size, len(noise))
	}
	for _, repo := range []string{"origin.git", "repos/app.git"} {
		objects := filepath.Join(publisherCache, repo, "objects")
		if _, err := os.Stat(filepath.Join(objects, "info", "alternates")); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(objects)
		if err != nil || len(entries) != 1 || entries[0].Name() != "info" {
			t.Fatalf("cache copied objects: %v, %v", entries, err)
		}
	}

	// The CLI has a clone of each project a grant to anyone names: app
	// and site.
	cliRepos := filepath.Join(cache, "valley", "repos")
	entries, err := os.ReadDir(cliRepos)
	if err != nil || len(entries) != 2 {
		t.Fatalf("CLI caches: %v, %v", entries, err)
	}
	for _, entry := range entries {
		objects := filepath.Join(cliRepos, entry.Name(), ".git", "objects")
		files, err := os.ReadDir(objects)
		if err != nil || len(files) != 1 || files[0].Name() != "info" {
			t.Fatalf("CLI copied objects: %v, %v", files, err)
		}
	}

	// The cache now holds what a read fetches, and the budget leaves
	// 256 KiB beside it.
	budget := du(storage...) + 256<<10
	peak = peakSize(storage, func() { said = v.fails(nil, "--max-storage", strconv.FormatInt(budget, 10)) })
	mustSay(t, "--max-storage", said, fmt.Sprintf("--max-storage %d leaves for it", budget))
	if peak > budget {
		t.Errorf("the state and the cache held %d bytes while the pass ran, more than --max-storage %d", peak, budget)
	}
	if left, _ := os.ReadDir(bundles); len(left) != 0 {
		t.Errorf("the refused pass left %v behind in the bundles", left)
	}

	rv := newReceiver(t, v)
	v.publish(rv.flags()...)
	mustSay(t, "an acknowledged digest past --max-storage", v.fails(rv, "--max-storage", strconv.FormatInt(budget, 10)),
		fmt.Sprintf("more than --max-storage %d", budget))
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
	v.publish(rv.flags()...)
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

// A project that loses one of two readers stays on the edge for the other.
// One that loses its last reader is gone from the next export the
// receiver activates: from export.json and from its repositories.
func TestAProjectMadePrivateLeavesTheReceiver(t *testing.T) {
	v := newValley(t)
	rv := newReceiver(t, v)
	v.declare("site", policy("site", "anyone", "operator"))
	v.publish(rv.flags()...)
	if got := names(rv.active()); !reflect.DeepEqual(got, []string{"app", "site"}) {
		t.Fatalf("the receiver serves %v, want app and site", got)
	}

	v.declare("site", policy("site", "operator"))
	stdout, _ := v.publish(rv.flags()...)
	mustSay(t, "site read by the operator alone", stdout, "site to operator")
	if got := names(rv.active()); !reflect.DeepEqual(got, []string{"app", "site"}) {
		t.Errorf("the receiver serves %v once anyone may not read site, want app and site", got)
	}

	v.declare("site", policy("site"))
	stdout, _ = v.publish(rv.flags()...)
	mustSay(t, "site made private", stdout, "exported: app; not exported: site, valley")
	if got := names(rv.active()); !reflect.DeepEqual(got, []string{"app"}) {
		t.Errorf("the receiver serves %v once no one may read site, want app alone", got)
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

// A refresh reserves the entire replacement while the old cache remains.
// Refusal leaves the old readable snapshot intact and no staged files.
func TestSharedCacheRefreshBudget(t *testing.T) {
	v := newValley(t)
	cache := filepath.Join(v.root, "shared-cache")
	p := &publisher{cache: cache, cfg: config{state: v.state, maxStorage: 1 << 20}}
	dir := filepath.Join(cache, "app.git")
	source := filepath.Join(v.srv, "app.git")
	refs := map[string]string{"refs/heads/main": "refs/heads/main"}
	ctx := context.Background()
	if _, err := p.shareRepository(ctx, dir, source, refs); err != nil {
		t.Fatal(err)
	}
	old, err := revParse(ctx, dir, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	v.write(filepath.Join(v.work["app"], "next"), "next")
	v.commit("app", "app: next")
	used, err := p.storageUsed()
	if err != nil {
		t.Fatal(err)
	}
	p.cfg.maxStorage = byteSize(used)
	if _, err := p.shareRepository(ctx, dir, source, refs); err == nil {
		t.Fatal("refresh exceeded its budget")
	}
	if tip, err := revParse(ctx, dir, "refs/heads/main"); err != nil || tip != old {
		t.Fatalf("refused refresh changed the cache: %s, %v", tip, err)
	}
	if entries, err := os.ReadDir(cache); err != nil || len(entries) != 1 {
		t.Fatalf("refused refresh left partial data: %v, %v", entries, err)
	}
	p.cfg.maxStorage = 1 << 20
	if _, err := p.shareRepository(ctx, dir, source, refs); err != nil {
		t.Fatal(err)
	}
	if tip, err := revParse(ctx, dir, "refs/heads/main"); err != nil || tip == old {
		t.Fatalf("refresh did not advance main: %s, %v", tip, err)
	}
}

// A cache's refs are read from the source by their exact names. With main
// gone from app and refs/heads/refs/heads/main made in its place, git
// rev-parse would read that branch as main; sharedFiles finds no main. The
// publisher lists a project's main before it reads it, so only a push
// between the two reaches this.
func TestSharedFilesReadEachRefByItsExactName(t *testing.T) {
	v := newValley(t)
	source := filepath.Join(v.srv, "app.git")
	v.run("", "git", "-C", source, "update-ref", "refs/heads/refs/heads/main", "refs/heads/main")
	v.run("", "git", "-C", source, "update-ref", "-d", "refs/heads/main")
	files, err := sharedFiles(context.Background(), source, map[string]string{"refs/heads/main": "refs/heads/main"})
	if err == nil || err.Error() != source+" holds no refs/heads/main" {
		t.Fatalf("with app's main gone, the cache's files are %v (%v)", files, err)
	}
}

// Refusing a pointer replacement leaves the previous pointer readable.
func TestPointerBudget(t *testing.T) {
	v := newValley(t)
	cache := filepath.Join(v.root, "cache", "valley-web-publish")
	p := &publisher{cache: cache, pointer: filepath.Join(cache, "_pointer.git"), cfg: config{instanceRepo: filepath.Join(v.srv, "valley.git"), state: v.state, maxStorage: 1}}
	ctx := context.Background()
	if err := p.preparePointer(ctx); err == nil {
		t.Fatal("cold pointer exceeded budget")
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 0 {
		t.Fatalf("partial pointer: %v", entries)
	}
	p.cfg.maxStorage = 1 << 20
	if err := p.preparePointer(ctx); err != nil {
		t.Fatal(err)
	}
	tip, err := revParse(ctx, p.pointer, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	used, err := p.storageUsed()
	if err != nil {
		t.Fatal(err)
	}
	p.cfg.maxStorage = byteSize(used)
	if err := p.preparePointer(ctx); err == nil {
		t.Fatal("warm pointer exceeded budget")
	}
	if got, err := revParse(ctx, p.pointer, "refs/heads/main"); err != nil || got != tip {
		t.Fatalf("pointer changed: %s, %v", got, err)
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 1 {
		t.Fatalf("partial pointer: %v", entries)
	}
}

// The CLI's cache must be complete enough for cacheComplete to reuse it,
// and a refusal must leave no repository for the CLI to mistake as complete.
func TestCLICacheBudget(t *testing.T) {
	v := newValley(t)
	cache := filepath.Join(v.root, "cache", "valley-web-publish")
	p := &publisher{cache: cache, cfg: config{instanceRepo: filepath.Join(v.srv, "valley.git"), state: v.state, maxStorage: 1}}
	ctx := context.Background()
	if err := p.prepareCLICaches(ctx, []string{"app"}); err == nil {
		t.Fatal("cold CLI cache exceeded budget")
	}
	root := filepath.Join(v.root, "cache", "valley", "repos")
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("partial CLI cache: %v", entries)
	}
	p.cfg.maxStorage = 1 << 20
	if err := p.prepareCLICaches(ctx, []string{"app"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("CLI cache: %v, %v", entries, err)
	}
	dir := filepath.Join(root, entries[0].Name())
	got := strings.TrimSpace(v.run(dir, "git", "config", "--file", ".git/config", "--get", "remote.origin.url"))
	if got != filepath.Join(v.srv, "app.git") {
		t.Fatalf("CLI cache origin: %q", got)
	}
	used, err := p.storageUsed()
	if err != nil {
		t.Fatal(err)
	}
	p.cfg.maxStorage = byteSize(used)
	if err := p.prepareCLICaches(ctx, []string{"app"}); err == nil {
		t.Fatal("warm CLI cache exceeded budget")
	}
	if after, _ := p.storageUsed(); after != used {
		t.Fatalf("refusal changed storage: %d -> %d", used, after)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("partial CLI replacement: %v", entries)
	}
}

// Neither live discovery nor a foreign knot edge may make an unshared
// cache after the publisher has staged its snapshot's project list.
func TestUnstagedCLICachesCannotGrowStorage(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions require an unprivileged user")
	}
	for _, added := range []bool{false, true} {
		t.Run(fmt.Sprintf("added during collection %v", added), func(t *testing.T) {
			v := newValley(t)
			foreign := filepath.Join(v.srv, "unlisted.git")
			v.run("", "git", "init", "--quiet", "--bare", foreign)
			noise := make([]byte, 4<<20)
			if _, err := rand.Read(noise); err != nil {
				t.Fatal(err)
			}
			blob := strings.TrimSpace(v.runInput("", string(noise), "git", "-C", foreign, "hash-object", "-w", "--stdin"))
			tree := strings.TrimSpace(v.runInput("", "100644 blob "+blob+"\tnoise\n", "git", "-C", foreign, "mktree"))
			commit := strings.TrimSpace(v.runInput("", "large history\n", "git", "-C", foreign, "commit-tree", tree))
			for _, ref := range []string{"refs/heads/main", "refs/the-valley/knots"} {
				v.run("", "git", "-C", foreign, "update-ref", ref, commit)
			}
			if !added {
				v.newKnot("app", "outcome", "Wait for external work", "--blocked-by", "unlisted:oc-a000001")
			} else {
				instance := filepath.Join(v.srv, "valley.git")
				list := strings.TrimSpace(v.runInput("", "app\nsite\nunlisted\nvalley\n", "git", "-C", instance, "hash-object", "-w", "--stdin"))
				wrapper := filepath.Join(v.root, "bin", "valley")
				data, err := os.ReadFile(wrapper)
				if err != nil {
					t.Fatal(err)
				}
				shebang, rest, _ := strings.Cut(string(data), "\n")
				v.write(wrapper, fmt.Sprintf("%s\ngit -C %s update-ref %s %s\n%s", shebang, instance, projectsRef, list, rest))
			}
			cache := filepath.Join(v.root, "cache")
			if err := os.RemoveAll(cache); err != nil {
				t.Fatal(err)
			}
			repos := filepath.Join(cache, "valley", "repos")
			// Remove an obsolete complete cache and recover a parent sealed
			// by an interrupted earlier pass before staging the new set.
			sum := sha256.Sum256([]byte(foreign))
			stale := filepath.Join(repos, fmt.Sprintf("%x-unlisted", sum[:8]))
			v.run("", "git", "init", "--quiet", stale)
			v.run(stale, "git", "remote", "add", "origin", foreign)

			if err := os.Chmod(repos, 0o500); err != nil {
				t.Fatal(err)
			}
			const budget = 256 << 10
			storage := []string{v.state, filepath.Join(cache, "valley"), filepath.Join(cache, "valley-web-publish")}
			peak := peakSize(storage, func() {
				if added {
					mustSay(t, "new project", v.fails(nil, "--max-storage", "256K"), "valley plan --all lists")
				} else {
					v.publish("--out", filepath.Join(v.root, "export"), "--max-storage", "256K")
				}
			})
			if peak > budget {
				t.Fatalf("storage reached %d bytes, budget %d", peak, budget)
			}
			// app and site, the projects a grant to anyone names.
			entries, err := os.ReadDir(repos)
			if err != nil || len(entries) != 2 {
				t.Fatalf("unstaged caches: %v, %v", entries, err)
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), "-unlisted") || entry.Name() == "obsolete" {
					t.Fatalf("unstaged cache %s", entry.Name())
				}
				objects, err := os.ReadDir(filepath.Join(repos, entry.Name(), ".git", "objects"))
				if err != nil || len(objects) != 1 || objects[0].Name() != "info" {
					t.Fatalf("copied objects: %v, %v", objects, err)
				}
			}
			info, err := os.Stat(repos)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o700 {
				t.Fatalf("cache parent remains sealed: %v", info.Mode())
			}
		})
	}
}
