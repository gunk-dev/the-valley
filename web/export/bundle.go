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
	"syscall"
	"time"
)

// GitProgram is the git this package runs: git on PATH, unless a build
// fixes it to an absolute path with
// -ldflags "-X the-valley/web/export.GitProgram=<path>".
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
//  4. the repository then holds refs/heads/main at tip, and no other ref;
//  5. git writes the repository's commit-graph, so a reader walks main's
//     history, as merge-base and log do, without parsing each commit.
//
// A refused bundle leaves no repository behind. Every refusal is an
// *Error with CodeBadBundle. Once ctx is done, git is stopped, and the
// error is no refusal.
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
// tip, fetched from source, and nothing else but main's commit-graph. It
// removes repo again on any failure, until ctx is done: a fetch ctx stopped
// may leave repo part way, for the caller to remove. A fetch ctx stopped
// is no refusal of the bundle.
func fetchMain(ctx context.Context, source, repo, tip, format string) (err error) {
	if !ValidObjectID(tip) {
		return refuse(CodeBadBundle, "%q is not an object ID", tip)
	}
	if _, err := os.Lstat(repo); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s is not a fresh repository: it exists already", repo)
	}
	defer func() {
		if err != nil {
			removeAll(ctx, repo)
		}
	}()
	if _, err := git(ctx, "", "init", "--quiet", "--bare", "--template=", "--initial-branch=main",
		"--object-format="+format, repo); err != nil {
		return err
	}
	if _, err := git(ctx, repo, "-c", "transfer.fsckObjects=true", "-c", "core.fsync=all",
		"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--end-of-options",
		source, MainRef+":"+MainRef); err != nil {
		if ctx.Err() != nil {
			return err
		}
		return refuse(CodeBadBundle, "%v", err)
	}
	refs, err := git(ctx, repo, "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return err
	}
	if got, want := strings.TrimSuffix(string(refs), "\n"), tip+" "+MainRef; got != want {
		return refuse(CodeBadBundle, "the fetched repository holds %q, not %q", got, want)
	}
	_, err = git(ctx, repo, "-c", "core.fsync=all", "commit-graph", "write", "--reachable", "--no-progress")
	return err
}

// Reachable reports whether each of commits is a commit that tip reaches
// in repo. Browsing serves a commit only when it is: a bundle can carry
// objects main does not reach, and those are never shown. An argument
// that is not an object ID, an object repo does not hold, and an object
// that is not a commit are all unreachable. So is an object ID of another
// length than tip's: git reads 40 hex digits in a SHA-256 repository as
// an abbreviation, and a commit is named by its full ID alone. Reachable
// fails when it is given no commit.
func Reachable(ctx context.Context, repo, tip string, commits ...string) (bool, error) {
	if !ValidObjectID(tip) {
		return false, fmt.Errorf("%q is not an object ID", tip)
	}
	if len(commits) == 0 {
		return false, errors.New("no commit to check")
	}
	var stdin bytes.Buffer
	for _, commit := range commits {
		if !ValidObjectID(commit) || len(commit) != len(tip) {
			return false, nil
		}
		stdin.WriteString(commit + "\n")
	}
	// --git-dir, as Git does, so git neither looks above repo nor holds
	// a repository another user owns to safe.directory.
	var out []byte
	err := run(command(ctx, "", "--git-dir="+repo, "cat-file", "--batch-check=%(objecttype)"), "cat-file",
		stdin.Bytes(), maxOutput, func(r io.Reader) (err error) {
			out, err = io.ReadAll(r)
			return err
		})
	if err != nil {
		return false, err
	}
	types := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(types) != len(commits) {
		return false, fmt.Errorf("git cat-file gave %d types for %d objects", len(types), len(commits))
	}
	for _, t := range types {
		if t != "commit" {
			return false, nil
		}
	}
	for _, commit := range commits {
		cmd := command(ctx, "", "--git-dir="+repo, "merge-base", "--is-ancestor", commit, tip)
		stderr := &head{max: maxStderr}
		cmd.Stderr = stderr
		err = cmd.Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
			continue
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			return false, nil
		}
		return false, fmt.Errorf("git merge-base: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return true, nil
}

// ErrOutputLimit is the error of a git command that writes more output
// than its caller set as the limit. git is stopped, and waited for, before
// the error is returned.
var ErrOutputLimit = errors.New("git wrote more than its output limit")

// The bounds on what this package keeps of a git command's output.
const (
	// maxStderr is the most of git's standard error kept for an error's
	// message. The rest is read and dropped.
	maxStderr = 16 << 10
	// maxOutput is the most standard output this package reads from a
	// git command it runs for itself: a ref listing or an object's type.
	maxOutput = 64 << 10
)

// Git runs git with args on the bare repository at repo and returns what
// git prints, which may be at most limit bytes. It runs git the way this
// package runs every git: replacement refs off, none of the caller's GIT_
// variables, and no system or global configuration. --git-dir names the
// repository, so git never reads a repository it finds above repo, and
// reads one another user owns without holding it to safe.directory. When
// git prints more than limit bytes, Git stops it and fails with
// ErrOutputLimit. A server reads a generation's repositories with it, and
// gives it object IDs it has checked, never a name a reader typed.
func Git(ctx context.Context, repo string, limit int64, args ...string) ([]byte, error) {
	var out []byte
	err := GitStream(ctx, repo, limit, func(r io.Reader) (err error) {
		out, err = io.ReadAll(r)
		return err
	}, args...)
	return out, err
}

// GitStream runs git as Git does, and calls read with git's standard
// output as git prints it. read may read at most limit bytes: a read past
// them fails with ErrOutputLimit. read may also return before the output
// ends, once it has what it needs. Once read returns, GitStream stops git
// if it is still running, and waits for it to exit, so no git outlives the
// call. GitStream returns read's error. When read succeeds, it returns
// git's own failure only if read reached the end of the output: a git
// stopped early is not held to how it exited.
func GitStream(ctx context.Context, repo string, limit int64, read func(io.Reader) error, args ...string) error {
	if len(args) == 0 {
		return errors.New("git needs a command")
	}
	return run(command(ctx, "", append([]string{"--git-dir=" + repo}, args...)...), args[0], nil, limit, read)
}

// run starts cmd, with stdin as its standard input when it is not nil,
// and gives read cmd's standard output, at most limit bytes of it. It
// keeps the first maxStderr bytes of cmd's standard error for its error,
// which names git's verb.
func run(cmd *exec.Cmd, verb string, stdin []byte, limit int64, read func(io.Reader) error) error {
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	stderr := &head{max: maxStderr}
	cmd.Stderr = stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git %s: %w", verb, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("git %s: %w", verb, err)
	}
	out := &bounded{r: pipe, n: limit, err: fmt.Errorf("git %s: %w", verb, ErrOutputLimit)}
	readErr := read(out)
	if readErr != nil || !out.eof {
		// git, or a process it started, may be blocked writing to a pipe
		// nobody reads any more.
		cmd.Cancel()
	}
	err = cmd.Wait()
	switch {
	case readErr != nil:
		return readErr
	case !out.eof:
		return nil
	case err != nil:
		return fmt.Errorf("git %s: %w: %s", verb, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// bounded reads r, at most n bytes of it. A read once n bytes are read
// fails with err if r holds more, and is the end of the output if it does
// not. eof is set once r's end is read.
type bounded struct {
	r    io.Reader
	n    int64
	err  error
	over bool
	eof  bool
}

func (b *bounded) Read(p []byte) (int, error) {
	switch {
	case b.over:
		return 0, b.err
	case b.eof:
		return 0, io.EOF
	case len(p) == 0:
		return 0, nil
	case b.n <= 0:
		// One byte more is past the limit, and the end is not.
		var one [1]byte
		n, err := b.r.Read(one[:])
		if n > 0 {
			b.over = true
			return 0, b.err
		}
		b.eof = err == io.EOF
		return 0, err
	}
	if int64(len(p)) > b.n {
		p = p[:b.n]
	}
	n, err := b.r.Read(p)
	b.n -= int64(n)
	b.eof = err == io.EOF
	return n, err
}

// head keeps the first max bytes written to it and drops the rest, so a
// command's standard error is read to its end and never held whole.
type head struct {
	max int
	b   bytes.Buffer
}

func (h *head) Write(p []byte) (int, error) {
	if room := h.max - h.b.Len(); room > 0 {
		h.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (h *head) String() string { return h.b.String() }

// command is git, run in dir unless it is "", with replacement refs off
// and an environment that holds none of the caller's GIT_ variables and
// reads no system or global configuration.
//
// git runs in a process group of its own, and every process git starts
// runs in that group unless it leaves it. Once ctx is done, the whole
// group is killed, so no process git started keeps running, or keeps
// git's output open, once git is stopped. A process that left the group
// can still hold the output open. Go closes the output gitWaitDelay after
// the group is killed, or after git exits once git is waited for.
func command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, GitProgram, append([]string{"--no-replace-objects"}, args...)...)
	cmd.Env = gitEnvironment(os.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// The group's ID is git's process ID.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return os.ErrProcessDone
	}
	cmd.WaitDelay = gitWaitDelay
	return cmd
}

// gitWaitDelay is how long a git command's output may stay open once git
// is stopped or has exited.
const gitWaitDelay = 2 * time.Second

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
	verb := args[0]
	for i := 0; verb == "-c" && i+2 < len(args); i += 2 {
		verb = args[i+2]
	}
	var out []byte
	err := run(command(ctx, dir, args...), verb, stdin, maxOutput, func(r io.Reader) (err error) {
		out, err = io.ReadAll(r)
		return err
	})
	return out, err
}
