package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"the-valley/selfupdate"
)

const selfUpdateHelp = `usage: valley self-update [-v] [--retry | --rollback | --pin | --unpin]
                          [--repo <url>] [--branch <name>]

Brings the installed valley up to the tip of a branch of the-valley, for a
machine whose valley should follow that branch without a system rebuild.
It is off until a repository is named, by --repo or by the user's git
config, which is read whatever the current directory:

  git config --global valley.selfUpdate.repo git@<valley host>:the-valley
  git config --global valley.selfUpdate.branch main   (the default)

repo is an https://, ssh://, git:// or file:// URL, an absolute path, or
user@host:path. user@host:path names what git itself would: a path that
starts with / or ~ as written, and any other path under the login's home,
read as ssh://user@host/~/path, which is how a valley host serves its
projects. Name a forge by its https:// or ssh:// URL. Any other transport
(ext::, fd::) and any character outside [A-Za-z0-9._~/:@-], which nix's
flake-ref parser would re-encode, is refused. git ls-remote and nix read the
repo by the same URL, and the git that self-update runs, directly or through
nix, is limited to those transports (GIT_ALLOW_PROTOCOL). Each run:
  1. takes a lock under $XDG_STATE_HOME/valley, so runs never overlap: a run
     that finds another in progress exits 0 and says so;
  2. resolves the branch's commit with git ls-remote;
  3. exits 0 if the installed valley was built from that commit (quietly;
     -v says so), or if that commit already failed to build or test (it is
     retried when the branch moves, or with --retry);
  4. builds exactly that commit, the flake's packages.<system>.valley:
     nix build 'git+<repo>?ref=refs/heads/<branch>&rev=<sha>#...';
  5. smoke-tests the build, in an environment that carries nothing of the
     running valley's: its valley --version must report the commit, its
     valley help must exit 0, and its valley checks --names must derive
     the expected checks from the tiny policy the package ships, which
     shows the schema it carries composes a policy. A failure records the
     commit as failed, installs nothing, and exits non-zero;
  6. installs it into the user's default nix profile in place of the
     profile element named valley, leaving other elements alone. The system
     valley stays underneath as the fallback;
  7. records the current and previous store paths and commits, the time and
     any failure in $XDG_STATE_HOME/valley/self-update.json.

Only the named branch installs, pinned to the commit ls-remote returned,
and only after it builds and passes the smoke test. Before self-update has
installed anything, the running valley's version (valley --version) says
which commit is installed.

  -v, --verbose   also say so when valley is up to date
  --retry         build the branch's tip even if it failed before
  --rollback      reinstall the previous valley; again to return to the
                  newer one
  --pin           leave the current install alone until --unpin: runs while
                  pinned exit 0 with a note, so a bad tip is not reinstalled
                  while it gets fixed
  --unpin         let self-update install the branch's tip again
  --repo URL      the repository, in place of valley.selfUpdate.repo
  --branch NAME   the branch, in place of valley.selfUpdate.branch`

type selfUpdateArgs struct {
	verbose, retry, rollback, pin, unpin bool
	repo, branch                         string
	help                                 bool
}

func parseSelfUpdateArgs(args []string) (selfUpdateArgs, error) {
	var a selfUpdateArgs
	usage := "usage: valley self-update [-v] [--retry | --rollback | --pin | --unpin] [--repo <url>] [--branch <name>]"
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func(flag string) (string, error) {
			if v, ok := strings.CutPrefix(arg, flag+"="); ok {
				return v, nil
			}
			if i+1 >= len(args) {
				return "", failure(flag + " needs a value; " + usage)
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case arg == "-v" || arg == "--verbose":
			a.verbose = true
		case arg == "--retry":
			a.retry = true
		case arg == "--rollback":
			a.rollback = true
		case arg == "--pin":
			a.pin = true
		case arg == "--unpin":
			a.unpin = true
		case arg == "-h" || arg == "--help":
			a.help = true
		case arg == "--repo" || strings.HasPrefix(arg, "--repo="):
			a.repo, err = value("--repo")
		case arg == "--branch" || strings.HasPrefix(arg, "--branch="):
			a.branch, err = value("--branch")
		default:
			err = failure("unknown argument " + arg + "; " + usage)
		}
		if err != nil {
			return a, err
		}
	}
	n := 0
	for _, set := range []bool{a.retry, a.rollback, a.pin, a.unpin} {
		if set {
			n++
		}
	}
	if n > 1 {
		return a, failure("--retry, --rollback, --pin and --unpin each ask for one thing; give one of them")
	}
	return a, nil
}

// runSelfUpdate is valley self-update (../selfupdate): it installs the tip
// of a branch of the-valley into the user's nix profile, or rolls back,
// pins or unpins what it installed. The running valley's version comes
// from VALLEY_VERSION, which bin/valley passes on from its package's stamp.
func runSelfUpdate(args []string, stdout, stderr io.Writer) int {
	a, err := parseSelfUpdateArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", err)
		return 2
	}
	if a.help {
		fmt.Fprintln(stdout, selfUpdateHelp)
		return 0
	}
	if err := selfUpdate(a, stdout); err != nil {
		fmt.Fprintf(stderr, "valley: self-update: %s\n", err)
		return 1
	}
	return 0
}

func selfUpdate(a selfUpdateArgs, stdout io.Writer) error {
	dir, err := selfupdate.DefaultDir()
	if err != nil {
		return err
	}
	u := &selfupdate.Updater{Dir: dir, Version: os.Getenv("VALLEY_VERSION"), Out: stdout, Verbose: a.verbose}
	if u.Repo = a.repo; u.Repo == "" {
		if u.Repo, err = userConfig("valley.selfUpdate.repo"); err != nil {
			return err
		}
	}
	if u.Branch = a.branch; u.Branch == "" {
		if u.Branch, err = userConfig("valley.selfUpdate.branch"); err != nil {
			return err
		}
	}
	if u.Branch == "" {
		u.Branch = "main"
	}

	// A systemd stop or a ^C ends the nix build and git with it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch {
	case a.rollback:
		return u.Rollback(ctx)
	case a.pin || a.unpin:
		return u.Pin(a.pin)
	}
	if u.Repo == "" {
		return failure("it is off until a repository is named, by --repo or by\n  git config --global valley.selfUpdate.repo git@<valley host>:the-valley")
	}
	return u.Update(ctx, a.retry)
}

// userConfig reads a key of the user's own git config, "" when it is
// unset. git runs at /, with no variable naming a repository, so no
// repository's config is read, and the answer is the same from any
// directory.
func userConfig(key string) (string, error) {
	cmd := exec.Command("git", "config", "--get", key)
	cmd.Dir = "/"
	for _, kv := range gitEnvironment(os.Environ()) {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR":
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading git config %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}
