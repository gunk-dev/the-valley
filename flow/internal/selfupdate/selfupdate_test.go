package selfupdate

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// acceptedRepos covers every form Validate accepts.
var acceptedRepos = []string{
	"https://github.com/maintainer/klaus",
	"https://github.com/maintainer/klaus.git",
	"ssh://git@github.com/maintainer/klaus",
	"ssh://git@[::1]:2222/srv/klaus.git",
	"git://example.com/klaus.git",
	"file:///srv/git/klaus.git",
	"/srv/git/klaus.git",
	"git@github.com:maintainer/klaus.git",
	"me@host.example:~/klaus.git",
	"https://example.com/a-b_c.d~e/f:g@h",
}

func TestValidateRepo(t *testing.T) {
	for _, repo := range acceptedRepos {
		if err := Validate(repo, "main"); err != nil {
			t.Errorf("Validate(%q) refused: %v", repo, err)
		}
	}
	for _, repo := range []string{
		"",
		"ext::sh -c touch% /tmp/pwned",
		"ext::sh",
		"fd::17",
		"foo::bar",
		"me@host::path",
		"-uhttps://example.com/klaus",
		"--upload-pack=touch /tmp/pwned",
		"/srv/git/klaus.git?rev=0000000",
		"/srv/git/klaus.git#packages.x86_64-linux.evil",
		"https://example.com/klaus?ref=evil",
		"/srv/git/my klaus.git",
		"/srv/git/klaus%20repo.git",
		"https://example.com/my%20klaus",
		"https://example.com/repo+name",
		"https://example.com/repo!",
		"https://example.com/repo$x",
		"https://example.com/repo'x",
		"https://example.com/repo(x",
		"https://example.com/repo,x",
		"https://example.com/repo;x",
		"https://example.com/repo=x",
		"/srv/git/klaus+1.git",
		"/srv/git/[klaus].git",
		"/srv/git/klaus.git\n",
		"http://example.com/klaus",
		"ftp://example.com/klaus",
		"file://relative/klaus",
		"ssh://-oProxyCommand=touch/klaus",
		"relative/klaus",
		"host:klaus",
	} {
		err := Validate(repo, "main")
		if err == nil {
			t.Errorf("Validate(%q) accepted it", repo)
			continue
		}
		if !strings.Contains(err.Error(), "self_update.repo") {
			t.Errorf("Validate(%q) = %v; want the error to name self_update.repo", repo, err)
		}
	}
}

// TestRepoURL: ls-remote and nix read a repo by the same URL, the one the
// flake ref carries.
func TestRepoURL(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for repo, want := range map[string]string{
		"/srv/git/klaus.git":                "file:///srv/git/klaus.git",
		"file:///srv/git/klaus.git":         "file:///srv/git/klaus.git",
		"https://github.com/maintainer/klaus": "https://github.com/maintainer/klaus",
		"git@github.com:maintainer/klaus.git": "ssh://git@github.com/maintainer/klaus.git",
		"me@host.example:/srv/klaus.git":    "ssh://me@host.example/srv/klaus.git",
		"me@host.example:~/klaus.git":       "ssh://me@host.example/~/klaus.git",
	} {
		if got := RepoURL(repo); got != want {
			t.Errorf("RepoURL(%q) = %q, want %q", repo, got, want)
		}
		if got, ref := FlakeRef(repo, "main", sha), "git+"+want+"?ref=refs/heads/main&rev="+sha+"#packages."; !strings.HasPrefix(got, ref) {
			t.Errorf("FlakeRef(%q) = %q, want prefix %q", repo, got, ref)
		}
	}
}

// TestFlakeRefSurvivesNix: the installed nix parses each accepted repo's
// flake ref and prints it back unchanged, so nix fetches the URL ls-remote
// read.
func TestFlakeRefSurvivesNix(t *testing.T) {
	if _, err := exec.LookPath("nix"); err != nil {
		if os.Getenv("KLAUS_TEST_TOOLS") == "required" {
			t.Fatal("nix not installed")
		}
		t.Skip("nix not installed")
	}
	sha := strings.Repeat("a", 40)
	for _, repo := range acceptedRepos {
		ref, _, _ := strings.Cut(FlakeRef(repo, "main", sha), "#")
		// Only stdout is the reference: nix warns on stderr, as it does
		// with no network.
		var stderr strings.Builder
		cmd := exec.Command("nix", "--extra-experimental-features", "nix-command flakes", "eval", "--raw", "--expr",
			`builtins.flakeRefToString (builtins.parseFlakeRef "`+ref+`")`)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("nix eval of %q: %v\n%s", ref, err, stderr.String())
		}
		// nix prints git+git:// by its alias git://, the same URL.
		if got := strings.Replace(ref, "git+git://", "git://", 1); string(out) != got {
			t.Errorf("repo %q: nix turned %q into %q", repo, got, out)
		}
	}
}
