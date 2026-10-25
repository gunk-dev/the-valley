package export

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitProgram is the git this package runs: git on PATH, unless a build
// fixes it to an absolute path with
// -ldflags "-X the-valley/webui/export.GitProgram=<path>".
var GitProgram = "git"

// MainRef is the one ref a bundle carries, and the one a generation's
// repository holds.
const MainRef = "refs/heads/main"

// maxBundleHeader is the most bytes of a bundle's header read: its version
// line, capabilities, prerequisites and refs.
const maxBundleHeader = 64 << 10

// AcceptBundle checks the bundle at path and fetches it into repo, a fresh
// bare repository it creates, in this order:
//
//  1. the bundle's header has no prerequisite, so the bundle is complete;
//  2. it carries exactly one ref, refs/heads/main, at tip;
//  3. git fetches it into the fresh repository with the explicit refspec
//     refs/heads/main:refs/heads/main, no tags, and
//     transfer.fsckObjects=true, so every object is checked as it is
//     indexed;
//  4. the repository then holds refs/heads/main at tip, and no other ref.
//
// A refused bundle leaves no repository behind. Every refusal is an
// *Error with CodeBadBundle.
func AcceptBundle(ctx context.Context, path, repo, tip string) error {
	format, err := checkBundleHeader(path, tip)
	if err != nil {
		return err
	}
	// git fetches in repo, so the bundle is named from the root.
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return fetchMain(ctx, abs, repo, tip, format)
}

// CarryRepo fetches main from from, a repository the receiver holds at tip,
// into repo, a fresh bare repository, by the same fetch and checks a
// bundle is fetched by. A new generation carries a project's repository
// this way when its source tip has not moved, so the project's bundle need
// not be sent again.
func CarryRepo(ctx context.Context, from, repo, tip string) error {
	format := "sha1"
	if len(tip) == 64 {
		format = "sha256"
	}
	abs, err := filepath.Abs(from)
	if err != nil {
		return err
	}
	return fetchMain(ctx, abs, repo, tip, format)
}

// checkBundleHeader reads the bundle's header, as gitformat-bundle defines
// it, and holds it to steps 1 and 2. It returns the object format.
func checkBundleHeader(path, tip string) (format string, err error) {
	bad := func(format string, args ...any) error { return refuse(CodeBadBundle, format, args...) }
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// The bound is on the file, not on the lines read from it, so a line
	// with no end is never buffered past it.
	r := bufio.NewReader(io.LimitReader(f, maxBundleHeader+1))
	read := 0
	line := func() (string, error) {
		s, err := r.ReadString('\n')
		read += len(s)
		if read > maxBundleHeader {
			return "", bad("the bundle's header is longer than %d bytes", maxBundleHeader)
		}
		if err != nil {
			return "", bad("the bundle's header ends before its blank line")
		}
		return strings.TrimSuffix(s, "\n"), nil
	}
	version, err := line()
	if err != nil {
		return "", err
	}
	if version != "# v2 git bundle" && version != "# v3 git bundle" {
		return "", bad("the bundle opens with %q, which is not a v2 or v3 bundle", version)
	}
	format = "sha1"
	var refs []string
	for {
		s, err := line()
		if err != nil {
			return "", err
		}
		switch {
		case s == "":
			if len(refs) != 1 {
				return "", bad("the bundle carries %d refs; it carries %s alone", len(refs), MainRef)
			}
			oid, name, _ := strings.Cut(refs[0], " ")
			if name != MainRef {
				return "", bad("the bundle carries %s; it carries %s alone", name, MainRef)
			}
			if oid != tip {
				return "", bad("the bundle carries %s at %s; the manifest declares %s", MainRef, oid, tip)
			}
			if want := map[string]int{"sha1": 40, "sha256": 64}[format]; len(tip) != want {
				return "", bad("the bundle is %s, and the tip %s is not a %s object ID", format, tip, format)
			}
			return format, nil
		case strings.HasPrefix(s, "@"):
			if version != "# v3 git bundle" {
				return "", bad("a v2 bundle has no capability line, and this one has %q", s)
			}
			switch s {
			case "@object-format=sha1":
				format = "sha1"
			case "@object-format=sha256":
				format = "sha256"
			default:
				return "", bad("the bundle has the capability %q; a bundle is a full, unfiltered one", s)
			}
		case strings.HasPrefix(s, "-"):
			return "", bad("the bundle has a prerequisite, %q; a bundle carries main's whole history", strings.TrimPrefix(s, "-"))
		default:
			oid, name, ok := strings.Cut(s, " ")
			if !ok || !ValidObjectID(oid) || name == "" {
				return "", bad("the bundle's header holds %q, which is not a ref line", s)
			}
			refs = append(refs, s)
		}
	}
}

// fetchMain makes repo a fresh bare repository holding refs/heads/main at
// tip, fetched from source, and nothing else. It removes repo again on
// any failure.
func fetchMain(ctx context.Context, source, repo, tip, format string) (err error) {
	if !ValidObjectID(tip) {
		return refuse(CodeBadBundle, "%q is not an object ID", tip)
	}
	if _, err := os.Lstat(repo); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s is not a fresh repository: it exists already", repo)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(repo)
		}
	}()
	if _, err := git(ctx, "", "init", "--quiet", "--bare", "--template=", "--initial-branch=main",
		"--object-format="+format, repo); err != nil {
		return err
	}
	if _, err := git(ctx, repo, "-c", "transfer.fsckObjects=true", "-c", "core.fsync=all",
		"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--end-of-options",
		source, MainRef+":"+MainRef); err != nil {
		return refuse(CodeBadBundle, "%v", err)
	}
	refs, err := git(ctx, repo, "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return err
	}
	if got, want := strings.TrimSuffix(string(refs), "\n"), tip+" "+MainRef; got != want {
		return refuse(CodeBadBundle, "the fetched repository holds %q, not %q", got, want)
	}
	return nil
}

// Reachable reports whether commit is a commit that tip reaches in repo.
// Browsing serves a commit only when it is: a bundle can carry objects
// main does not reach, and those are never shown. An argument that is not
// an object ID, an object repo does not hold, and an object that is not a
// commit are all unreachable.
func Reachable(ctx context.Context, repo, tip, commit string) (bool, error) {
	if !ValidObjectID(tip) {
		return false, fmt.Errorf("%q is not an object ID", tip)
	}
	if !ValidObjectID(commit) {
		return false, nil
	}
	out, err := gitInput(ctx, repo, []byte(commit+"\n"), "cat-file", "--batch-check=%(objecttype)")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(string(out)) != "commit" {
		return false, nil
	}
	cmd := command(ctx, repo, "merge-base", "--is-ancestor", commit, tip)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, fmt.Errorf("git merge-base: %w: %s", err, strings.TrimSpace(stderr.String()))
}

// command is git, run in dir unless it is "", with replacement refs off
// and an environment that holds none of the caller's GIT_ variables and
// reads no system or global configuration.
func command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, GitProgram, append([]string{"--no-replace-objects"}, args...)...)
	cmd.Env = gitEnvironment(os.Environ())
	return cmd
}

func gitEnvironment(inherited []string) []string {
	var env []string
	for _, kv := range inherited {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return gitInput(ctx, dir, nil, args...)
}

func gitInput(ctx context.Context, dir string, stdin []byte, args ...string) ([]byte, error) {
	cmd := command(ctx, dir, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		verb := args[0]
		for i := 0; verb == "-c" && i+2 < len(args); i += 2 {
			verb = args[i+2]
		}
		return out, fmt.Errorf("git %s: %w: %s", verb, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
