package export_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"the-valley/webui/export"
)

// git runs git for a test, with none of the caller's GIT_ variables and no
// system or global configuration, and returns its trimmed output.
func git(t *testing.T, dir string, stdin []byte, args ...string) string {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Example Author", "GIT_AUTHOR_EMAIL=author@example.org",
		"GIT_COMMITTER_NAME=Example Author", "GIT_COMMITTER_EMAIL=author@example.org",
		"GIT_AUTHOR_DATE=2026-10-01T09:00:00Z", "GIT_COMMITTER_DATE=2026-10-01T09:00:00Z")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// source is a bare repository whose main has two commits, first and tip,
// and whose side branch has a commit main does not reach.
type source struct {
	repo, first, tip, side string
}

func newSource(t *testing.T, format string) source {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "source.git")
	git(t, "", nil, "init", "--quiet", "--bare", "--initial-branch=main", "--object-format="+format, repo)
	commit := func(parent, file, content string) string {
		blob := git(t, repo, []byte(content), "hash-object", "-w", "--stdin")
		tree := git(t, repo, []byte(fmt.Sprintf("100644 blob %s\t%s\n", blob, file)), "mktree")
		args := []string{"commit-tree", tree, "-m", file}
		if parent != "" {
			args = append(args, "-p", parent)
		}
		return git(t, repo, nil, args...)
	}
	s := source{repo: repo}
	s.first = commit("", "README.md", "# source\n")
	s.tip = commit(s.first, "main.go", "package main\n")
	s.side = commit(s.first, "side.txt", "not on main\n")
	git(t, repo, nil, "update-ref", "refs/heads/main", s.tip)
	git(t, repo, nil, "update-ref", "refs/heads/side", s.side)
	return s
}

func (s source) bundle(t *testing.T, args ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "b.bundle")
	git(t, s.repo, nil, append([]string{"bundle", "create", "--quiet", path}, args...)...)
	return path
}

// A full bundle of main at the declared tip is accepted into a fresh bare
// repository holding main alone, with a commit-graph of main's history
// that git verifies.
func TestAcceptBundle(t *testing.T) {
	ctx := context.Background()
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			s := newSource(t, format)
			repo := filepath.Join(t.TempDir(), "app.git")
			if err := export.AcceptBundle(ctx, s.bundle(t, "main"), repo, s.tip); err != nil {
				t.Fatal(err)
			}
			if refs := git(t, repo, nil, "for-each-ref", "--format=%(refname) %(objectname)"); refs != "refs/heads/main "+s.tip {
				t.Errorf("the repository holds %q", refs)
			}
			if got := git(t, repo, nil, "rev-parse", "--show-object-format"); got != format {
				t.Errorf("the repository is %s", got)
			}
			hasCommitGraph(t, repo)
		})
	}
}

// hasCommitGraph checks that repo has a commit-graph git verifies.
func hasCommitGraph(t *testing.T, repo string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repo, "objects", "info", "commit-graph")); err != nil {
		t.Errorf("the repository has no commit-graph: %v", err)
	}
	git(t, repo, nil, "commit-graph", "verify")
}

// A bundle and a repository named relative to the working directory are
// read and written there.
func TestAcceptBundleTakesRelativePaths(t *testing.T) {
	s := newSource(t, "sha1")
	bundle := s.bundle(t, "main")
	t.Chdir(filepath.Dir(bundle))
	if err := export.AcceptBundle(context.Background(), filepath.Base(bundle), "app.git", s.tip); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(bundle), "app.git", "HEAD")); err != nil {
		t.Error(err)
	}
}

// What the acceptance procedure refuses, each leaving no repository.
func TestAcceptBundleRefuses(t *testing.T) {
	ctx := context.Background()
	s := newSource(t, "sha1")
	good := s.bundle(t, "main")
	corrupt := func() string {
		data, err := os.ReadFile(good)
		if err != nil {
			t.Fatal(err)
		}
		// Past the header, in the pack: flip one byte of the last object.
		data[len(data)-30] ^= 0xff
		path := filepath.Join(t.TempDir(), "corrupt.bundle")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cases := []struct {
		name, bundle, tip, says string
	}{
		{"a wrong tip", good, s.first, "the manifest declares"},
		{"prerequisites", s.bundle(t, "main~1..main"), s.tip, "prerequisite"},
		{"an extra ref", s.bundle(t, "main", "side"), s.tip, "2 refs"},
		{"another ref than main", s.bundle(t, "side"), s.side, "refs/heads/side"},
		{"a corrupt pack", corrupt(), s.tip, "git fetch"},
		{"a malformed commit", malformedBundle(t), "", "fsck"},
		{"no bundle", filepath.Join(t.TempDir(), "missing.bundle"), s.tip, "no such file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tip := c.tip
			if tip == "" {
				tip = bundleTip(t, c.bundle)
			}
			repo := filepath.Join(t.TempDir(), "app.git")
			err := export.AcceptBundle(ctx, c.bundle, repo, tip)
			if err == nil || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("AcceptBundle gave %v, want an error saying %q", err, c.says)
			}
			if c.name != "no bundle" {
				wantCode(t, err, export.CodeBadBundle)
			}
			if _, err := os.Lstat(repo); !os.IsNotExist(err) {
				t.Errorf("the refusal left %s behind", repo)
			}
		})
	}
	t.Run("a repository that exists", func(t *testing.T) {
		repo := t.TempDir()
		if err := export.AcceptBundle(ctx, good, repo, s.tip); err == nil {
			t.Fatal("AcceptBundle fetched into a directory that existed")
		}
		if _, err := os.Lstat(repo); err != nil {
			t.Errorf("the refusal removed the directory that was there: %v", err)
		}
	})
}

// A bundle's header is read through a 64 KiB bound on the file. A header
// with no line end, a thousand times longer, is refused once the bound
// is reached: the receiver reads about 64 KiB of it, not the whole of it.
// The bundle is a named pipe, so the test counts what was read.
func TestAcceptBundleBoundsTheHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endless.bundle")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	const offered = 64 << 20
	taken := make(chan int64, 1)
	go func() {
		w, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			taken <- -1
			return
		}
		defer w.Close()
		n, _ := w.Write([]byte("# v2 git bundle\n"))
		total := int64(n)
		chunk := bytes.Repeat([]byte("a"), 32<<10)
		for total < offered {
			n, err := w.Write(chunk)
			total += int64(n)
			if err != nil {
				break // the receiver closed the bundle
			}
		}
		taken <- total
	}()
	repo := filepath.Join(t.TempDir(), "app.git")
	err := export.AcceptBundle(context.Background(), path, repo, strings.Repeat("1", 40))
	if err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Fatalf("AcceptBundle gave %v, want a refusal of the header's length", err)
	}
	wantCode(t, err, export.CodeBadBundle)
	// What the pipe holds unread comes on top of what was read.
	if n := <-taken; n < 0 || n > 4<<20 {
		t.Errorf("the receiver took %d bytes of a header with no end, want about 64 KiB", n)
	}
	if _, err := os.Lstat(repo); !os.IsNotExist(err) {
		t.Errorf("the refusal left %s behind", repo)
	}
}

// bundleTip is the one ref a bundle's header names.
func bundleTip(t *testing.T, path string) string {
	t.Helper()
	return strings.Fields(git(t, "", nil, "bundle", "list-heads", path))[0]
}

// malformedBundle is a bundle of a main whose tip is a commit fsck
// refuses: its author has no email and no date.
func malformedBundle(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "bad.git")
	git(t, "", nil, "init", "--quiet", "--bare", "--initial-branch=main", repo)
	tree := git(t, repo, nil, "mktree")
	bad := git(t, repo, []byte("tree "+tree+"\nauthor nobody\ncommitter nobody\n\nmalformed\n"),
		"hash-object", "-t", "commit", "--literally", "-w", "--stdin")
	git(t, repo, nil, "update-ref", "refs/heads/main", bad)
	path := filepath.Join(t.TempDir(), "bad.bundle")
	git(t, repo, nil, "bundle", "create", "--quiet", path, "main")
	return path
}

// unreachableBundle is a bundle of main whose pack also carries a commit
// main does not reach, as a hostile publisher could write one.
func unreachableBundle(t *testing.T, s source) string {
	t.Helper()
	objects := git(t, s.repo, nil, "rev-list", "--objects", "main", "side")
	pack := packObjects(t, s.repo, objects)
	header := "# v2 git bundle\n" + s.tip + " refs/heads/main\n\n"
	if len(s.tip) == 64 {
		header = "# v3 git bundle\n@object-format=sha256\n" + s.tip + " refs/heads/main\n\n"
	}
	path := filepath.Join(t.TempDir(), "unreachable.bundle")
	if err := os.WriteFile(path, append([]byte(header), pack...), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func packObjects(t *testing.T, repo, objects string) []byte {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "pack-objects", "--stdout", "--quiet")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	cmd.Stdin = strings.NewReader(objects + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Reachable admits only commits main's tip reaches, each named by its
// full ID in the repository's object format. A bundle can carry other
// objects; they are in the repository and never reachable. In a SHA-256
// repository, the first 40 hex digits of a commit are refused, though git
// would read them as an abbreviation of it.
func TestReachable(t *testing.T) {
	ctx := context.Background()
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			s := newSource(t, format)
			repo := filepath.Join(t.TempDir(), "app.git")
			if err := export.AcceptBundle(ctx, unreachableBundle(t, s), repo, s.tip); err != nil {
				t.Fatal(err)
			}
			if got := git(t, repo, nil, "cat-file", "-t", s.side); got != "commit" {
				t.Fatalf("the unreachable commit is not in the repository: %q", got)
			}
			tree := git(t, repo, nil, "rev-parse", s.tip+"^{tree}")
			cases := []struct {
				name, commit string
				want         bool
			}{
				{"the tip", s.tip, true},
				{"its parent", s.first, true},
				{"a commit in the bundle that main does not reach", s.side, false},
				{"a tree", tree, false},
				{"an object the repository does not hold", strings.Repeat("1", len(s.tip)), false},
				{"an abbreviated ID", s.tip[:12], false},
				{"a revision expression", s.tip + "~1", false},
				{"an option", "--all", false},
			}
			if format == "sha256" {
				if got := git(t, repo, nil, "rev-parse", "--verify", s.tip[:40]+"^{commit}"); got != s.tip {
					t.Fatalf("git does not read the tip's first 40 hex digits as the tip: %q", got)
				}
				cases = append(cases, struct {
					name, commit string
					want         bool
				}{"the tip's first 40 hex digits", s.tip[:40], false})
			} else {
				cases = append(cases, struct {
					name, commit string
					want         bool
				}{"64 hex digits", s.tip + strings.Repeat("0", 24), false})
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					got, err := export.Reachable(ctx, repo, s.tip, c.commit)
					if err != nil || got != c.want {
						t.Errorf("Reachable gave %v, %v; want %v", got, err, c.want)
					}
				})
			}
			if _, err := export.Reachable(ctx, repo, "main", s.tip); err == nil {
				t.Error("Reachable took a tip that is no object ID")
			}
			// Several commits are reachable only when each of them is.
			for _, c := range []struct {
				commits []string
				want    bool
			}{
				{[]string{s.first, s.tip}, true},
				{[]string{s.first, s.side}, false},
				{[]string{s.first, tree}, false},
				{[]string{s.side, s.tip}, false},
			} {
				if got, err := export.Reachable(ctx, repo, s.tip, c.commits...); err != nil || got != c.want {
					t.Errorf("Reachable of %d commits gave %v, %v; want %v", len(c.commits), got, err, c.want)
				}
			}
			if _, err := export.Reachable(ctx, repo, s.tip); err == nil {
				t.Error("Reachable of no commit did not fail")
			}
		})
	}
}

// A carried repository is fetched and checked as a bundle is, and holds
// main alone.
func TestCarryRepo(t *testing.T) {
	ctx := context.Background()
	s := newSource(t, "sha1")
	held := filepath.Join(t.TempDir(), "held.git")
	if err := export.AcceptBundle(ctx, s.bundle(t, "main"), held, s.tip); err != nil {
		t.Fatal(err)
	}
	carried := filepath.Join(t.TempDir(), "carried.git")
	if err := export.CarryRepo(ctx, held, carried, s.tip); err != nil {
		t.Fatal(err)
	}
	if refs := git(t, carried, nil, "for-each-ref", "--format=%(refname) %(objectname)"); refs != "refs/heads/main "+s.tip {
		t.Errorf("the carried repository holds %q", refs)
	}
	hasCommitGraph(t, carried)
	wrong := filepath.Join(t.TempDir(), "wrong.git")
	if err := export.CarryRepo(ctx, held, wrong, s.first); err == nil {
		t.Error("CarryRepo carried a repository at another tip")
	}
	if _, err := os.Lstat(wrong); !os.IsNotExist(err) {
		t.Error("the refusal left a repository behind")
	}
}

// Git reads the repository it is given and no other: a directory that is
// not a repository fails, even when a repository holds it.
func TestGit(t *testing.T) {
	ctx := context.Background()
	s := newSource(t, "sha1")
	repo := filepath.Join(t.TempDir(), "app.git")
	if err := export.AcceptBundle(ctx, s.bundle(t, "main"), repo, s.tip); err != nil {
		t.Fatal(err)
	}
	out, err := export.Git(ctx, repo, 1<<10, "rev-parse", export.MainRef)
	if err != nil || strings.TrimSpace(string(out)) != s.tip {
		t.Errorf("Git read main as %q, %v; want %s", out, err, s.tip)
	}
	if out, err := export.Git(ctx, filepath.Join(repo, "refs"), 1<<10, "rev-parse", export.MainRef); err == nil {
		t.Errorf("Git read %q from a directory inside the repository", out)
	}
}

// Git reads no more output than its limit: git printing more is stopped
// and waited for, and the read fails. Output of exactly the limit is
// read whole. A reader that returns before the output ends stops git too,
// and is not failed for it. A git left writing to a pipe nobody reads
// would never exit, so each call returning shows git was stopped.
func TestGitOutputLimit(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "big.git")
	git(t, "", nil, "init", "--quiet", "--bare", repo)
	big := strings.Repeat("eight MiB of output\n", (8<<20)/20)
	blob := git(t, repo, []byte(big), "hash-object", "-w", "--stdin")

	if out, err := export.Git(ctx, repo, 1<<20, "cat-file", "blob", blob); !errors.Is(err, export.ErrOutputLimit) {
		t.Errorf("Git read %d bytes of an 8 MiB blob with a 1 MiB limit, and returned %v", len(out), err)
	}
	if out, err := export.Git(ctx, repo, int64(len(big)), "cat-file", "blob", blob); err != nil || string(out) != big {
		t.Errorf("Git read %d bytes of a blob exactly its limit long: %v", len(out), err)
	}

	var first []byte
	err := export.GitStream(ctx, repo, 64<<20, func(r io.Reader) error {
		first = make([]byte, 5)
		_, err := io.ReadFull(r, first)
		return err
	}, "cat-file", "blob", blob)
	if err != nil || string(first) != "eight" {
		t.Errorf("a reader that stopped early read %q and got %v", first, err)
	}

	// A git that fails is reported once its output is read to the end.
	if err := export.GitStream(ctx, repo, 1<<10, func(r io.Reader) error {
		_, err := io.ReadAll(r)
		return err
	}, "cat-file", "blob", strings.Repeat("0", 40)); err == nil {
		t.Error("GitStream did not report git's failure")
	}
}
