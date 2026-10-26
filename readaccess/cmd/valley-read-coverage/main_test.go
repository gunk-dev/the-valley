package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The command runs over a real valley: bare repositories made with real
// git, read under the real schemas with the real cue.

const (
	idApp    = "9da6809f038df92138c131cd16bed76d"
	idSite   = "273d5668702918ea7ae079de374db96b"
	idValley = "f83927d5a4ed068cdd02a8cdaa09f26d"
	key      = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyForARejectedFixtureAAAAAAAAAAA"
)

func needTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
			t.Fatalf("no %s on PATH, and VALLEY_TEST_TOOLS=required", name)
		}
		t.Skipf("no %s on PATH; the nix check provides one", name)
	}
}

func hostGit(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// tree writes a tree holding the files, by path, into repo.
func tree(t *testing.T, repo string, files map[string]string) string {
	t.Helper()
	dirs := map[string]map[string]string{}
	var listing []string
	for path, body := range files {
		if first, rest, nested := strings.Cut(path, "/"); nested {
			if dirs[first] == nil {
				dirs[first] = map[string]string{}
			}
			dirs[first][rest] = body
			continue
		}
		listing = append(listing, "100644 blob "+hostGit(t, body, "-C", repo, "hash-object", "-w", "--stdin")+"\t"+path)
	}
	for name, sub := range dirs {
		listing = append(listing, "040000 tree "+tree(t, repo, sub)+"\t"+name)
	}
	sort.Strings(listing)
	return hostGit(t, strings.Join(listing, "\n")+"\n", "-C", repo, "mktree")
}

// project makes a bare repository beside the others, with a main holding
// the files unless files is nil.
func project(t *testing.T, srv, name string, files map[string]string) string {
	t.Helper()
	repo := filepath.Join(srv, name+".git")
	hostGit(t, "", "init", "--quiet", "--bare", repo)
	if files != nil {
		commit := hostGit(t, "", "-C", repo, "-c", "user.name=test", "-c", "user.email=test@example.org",
			"commit-tree", tree(t, repo, files), "-m", name)
		hostGit(t, "", "-C", repo, "update-ref", "refs/heads/main", commit)
	}
	return repo
}

func policy(name, identity, readers string) map[string]string {
	return map[string]string{"policy/read/read.cue": "package read\n\nproject: \"" + name +
		"\"\nidentity: \"" + identity + "\"\nreaders: {" + readers + "}\n"}
}

// The report names each project a reader lacks a consent for, and which:
// a project with no main, no policy/read, or one that does not name the
// reader lacks its own consent; a project no grant covers lacks the
// instance's.
func TestCoverage(t *testing.T) {
	needTool(t, "git")
	needTool(t, "cue")
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("HOME", root)
	schemas, err := filepath.Abs(filepath.Join("..", "..", "..", "schema"))
	if err != nil {
		t.Fatal(err)
	}
	srv := filepath.Join(root, "srv")

	instance := policy("valley", idValley, "operator: true")
	instance["identity/registry.cue"] = `package identity

boundaries: {
	registry: kind: "registry"
	web: {kind: "edge-read", destination: "example.org"}
}
genesis: "operator"
principals: operator: {
	kind: "human"
	keys: [{class: "ssh-ed25519", bound: "hardware", public: "` + key + `"}]
	grants: {
		govern: boundary: "registry"
		read: {boundary: "web", projects: "all"}
	}
}
anyone: grants: read: {boundary: "web", projects: {app: "` + idApp + `", site: "` + idSite + `"}}
`
	valley := project(t, srv, "valley", instance)
	project(t, srv, "app", policy("app", idApp, "anyone: true, operator: true"))
	project(t, srv, "site", policy("site", idSite, "anyone: true"))
	project(t, srv, "docs", map[string]string{"README.md": "# docs\n"})
	project(t, srv, "fresh", nil)
	list := hostGit(t, "app\ndocs\nfresh\nsite\nvalley\n", "-C", valley, "hash-object", "-w", "--stdin")
	hostGit(t, "", "-C", valley, "update-ref", "refs/the-valley/projects", list)

	for _, c := range []struct {
		reader string
		want   []string
	}{
		{"operator", []string{
			"docs: no project consent (its main holds no policy/read)",
			"fresh: no project consent (it has no main)",
			"site: no project consent (its policy/read does not name operator)",
			"operator may read 2 of 5 projects at example.org",
		}},
		{"anyone", []string{
			"docs: no project consent (its main holds no policy/read); no instance grant (no read grant of anyone at example.org names docs)",
			"fresh: no project consent (it has no main); no instance grant (no read grant of anyone at example.org names fresh)",
			"valley: no project consent (its policy/read does not name anyone); no instance grant (no read grant of anyone at example.org covers valley with the identity " + idValley + ")",
			"anyone may read 2 of 5 projects at example.org",
		}},
	} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"--instance-repo", valley, "--destination", "example.org",
			"--reader", c.reader, "--read-schema", filepath.Join(schemas, "read.cue"),
			"--identity-schema", filepath.Join(schemas, "identity.cue")}, &stdout, &stderr, time.Now())
		if code != 0 {
			t.Fatalf("%s: exited %d: %s", c.reader, code, stderr.String())
		}
		if got := strings.Split(strings.TrimSpace(stdout.String()), "\n"); strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s: the report is\n%s\nwant\n%s", c.reader, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}
}
