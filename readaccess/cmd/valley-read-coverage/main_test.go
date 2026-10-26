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

// registry is the instance's identity registry: the operator governs it
// and reads every project, and anyone reads the projects in scope.
func registry(scope string) string {
	return `package identity

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
anyone: grants: read: {boundary: "web", projects: ` + scope + `}
`
}

// coverageValley makes a valley in a new directory: the instance repository
// valley, whose registry on main lets anyone read app and site, and the
// projects app, site, docs and fresh beside it. It returns the instance
// repository and the schemas' directory.
func coverageValley(t *testing.T) (string, string) {
	t.Helper()
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
	instance["identity/registry.cue"] = registry(`{app: "` + idApp + `", site: "` + idSite + `"}`)
	valley := project(t, srv, "valley", instance)
	project(t, srv, "app", policy("app", idApp, "anyone: true, operator: true"))
	project(t, srv, "site", policy("site", idSite, "anyone: true"))
	project(t, srv, "docs", map[string]string{"README.md": "# docs\n"})
	project(t, srv, "fresh", nil)
	list := hostGit(t, "app\ndocs\nfresh\nsite\nvalley\n", "-C", valley, "hash-object", "-w", "--stdin")
	hostGit(t, "", "-C", valley, "update-ref", "refs/the-valley/projects", list)
	return valley, schemas
}

// report runs the command over the valley for the reader, with the flags
// given after the others, and fails the test unless it exits 0.
func report(t *testing.T, valley, schemas, reader string, more ...string) (stdout, stderr string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := run(context.Background(), append([]string{"--instance-repo", valley, "--destination", "example.org",
		"--reader", reader, "--read-schema", filepath.Join(schemas, "read.cue"),
		"--identity-schema", filepath.Join(schemas, "identity.cue")}, more...), &out, &errs, time.Now())
	if code != 0 {
		t.Fatalf("%s: exited %d: %s", reader, code, errs.String())
	}
	return out.String(), errs.String()
}

func sameReport(t *testing.T, what, stdout string, want []string) {
	t.Helper()
	if got := strings.Split(strings.TrimSpace(stdout), "\n"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s: the report is\n%s\nwant\n%s", what, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The report names each project a reader lacks a consent for, and which:
// a project with no main, no policy/read, or one that does not name the
// reader lacks its own consent; a project no grant covers lacks the
// instance's.
func TestCoverage(t *testing.T) {
	valley, schemas := coverageValley(t)
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
		stdout, _ := report(t, valley, schemas, c.reader)
		sameReport(t, c.reader, stdout, c.want)
	}
}

// The registry is read at --registry-ref, the ref the identity compiler
// reads it at. A grant revoked on that ref alone is revoked, whatever main
// still says, and an instance repository with no such ref grants no read,
// whatever another ref git would take the name for grants.
func TestRegistryRef(t *testing.T) {
	valley, schemas := coverageValley(t)
	main := hostGit(t, "", "-C", valley, "rev-parse", "main")
	revoked := map[string]string{"identity/registry.cue": registry(`{app: "` + idApp + `"}`)}
	for name, body := range policy("valley", idValley, "operator: true") {
		revoked[name] = body
	}
	commit := hostGit(t, "", "-C", valley, "-c", "user.name=test", "-c", "user.email=test@example.org",
		"commit-tree", tree(t, valley, revoked), "-p", main, "-m", "valley: site's grant to anyone is revoked")
	hostGit(t, "", "-C", valley, "update-ref", "refs/heads/registry", commit)

	noGrant := func(name string) string {
		return name + ": no project consent (its main holds no policy/read); no instance grant (no read grant of anyone at example.org names " + name + ")"
	}
	stdout, _ := report(t, valley, schemas, "anyone", "--registry-ref", "refs/heads/registry")
	sameReport(t, "anyone at refs/heads/registry", stdout, []string{
		noGrant("docs"),
		"fresh: no project consent (it has no main); no instance grant (no read grant of anyone at example.org names fresh)",
		"site: no instance grant (no read grant of anyone at example.org covers site with the identity " + idSite + ")",
		"valley: no project consent (its policy/read does not name anyone); no instance grant (no read grant of anyone at example.org covers valley with the identity " + idValley + ")",
		"anyone may read 1 of 5 projects at example.org",
	})
	if stdout, _ := report(t, valley, schemas, "anyone"); !strings.HasSuffix(strings.TrimSpace(stdout), "anyone may read 2 of 5 projects at example.org") {
		t.Errorf("at main, site's grant is not revoked, and the report is\n%s", stdout)
	}

	stdout, stderr := report(t, valley, schemas, "operator", "--registry-ref", "refs/heads/none")
	if !strings.Contains(stderr, "holds no refs/heads/none, so it has no registry, and grants no read") ||
		!strings.HasSuffix(strings.TrimSpace(stdout), "operator may read 0 of 5 projects at example.org") {
		t.Errorf("with no registry ref, the report is\n%s\nand the command said\n%s", stdout, stderr)
	}

	// With refs/heads/registry gone, git rev-parse would resolve the name
	// to refs/heads/refs/heads/registry, a branch any pusher can make, here
	// granting anyone read of app and site. The registry is read at the
	// ref named exactly, so there is none.
	hostGit(t, "", "-C", valley, "update-ref", "-d", "refs/heads/registry")
	hostGit(t, "", "-C", valley, "update-ref", "refs/heads/refs/heads/registry", main)
	stdout, stderr = report(t, valley, schemas, "anyone", "--registry-ref", "refs/heads/registry")
	if !strings.Contains(stderr, "holds no refs/heads/registry, so it has no registry, and grants no read") ||
		!strings.HasSuffix(strings.TrimSpace(stdout), "anyone may read 0 of 5 projects at example.org") {
		t.Errorf("with refs/heads/registry gone, the report is\n%s\nand the command said\n%s", stdout, stderr)
	}

	for _, ref := range []string{"main", "refs/heads/main~1", "refs/heads/../main", "refs/heads/registry.lock", "refs/heads/a b", "refs//main"} {
		var out, errs bytes.Buffer
		if code := run(context.Background(), []string{"--instance-repo", valley, "--destination", "example.org",
			"--reader", "anyone", "--registry-ref", ref, "--read-schema", filepath.Join(schemas, "read.cue"),
			"--identity-schema", filepath.Join(schemas, "identity.cue")}, &out, &errs, time.Now()); code != 2 {
			t.Errorf("--registry-ref %q: exited %d, want 2", ref, code)
		}
	}
}
