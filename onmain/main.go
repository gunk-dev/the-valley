// Command onmain hands a host's own unit each new tip of a project's main.
// It is the bridge behind a host's services.valley.onMain binding
// (nix/valley-host.nix), one instance per binding, and the step that
// writes the bound unit's environment.
//
// onmain watch converges on the latest tip. When main names a commit other
// than the one it last requested, it replaces its commit file with that
// commit and then creates its request file. That is all a bridge can do: it
// starts nothing itself and holds no grant. The binding's path unit, with
// PathExists= on the request file, starts the bound unit whenever the file
// exists and the unit is not running, and checks the file again each time
// the unit stops.
//
// onmain environment is the unit's first ExecStartPre=, run as root. It
// removes the request, reads the commit file, and writes the environment
// file the unit's command reads: VALLEY_REPOSITORY, from its own flag, and
// VALLEY_COMMIT, the commit. systemd imports every assignment in an
// environment file, so the bridge's user never writes one: a bridge that
// could would choose the unit's loader and interpreter settings, and the
// unit may run as root. The commit file is accepted only as one commit ID,
// so all a bridge chooses is which commit runs.
//
// The order of the two steps on each side makes the handoff lose no tip
// (see writeEnvironment). Moves made during a run leave one request and
// the newest commit, so they become one run at the newest tip. The request
// is removed before anything that can fail, so a failed run leaves nothing
// that starts the unit again: the next run is for the next tip, or the
// operator's.
//
// The tip last requested is kept in memory alone. So every start of the
// bridge requests the current tip once, and a unit must tolerate running
// again for a commit it has already run.
//
// main is read through git, and git's files ref storage is watched for its
// moves (watch_linux.go). The watch is in place before main is first read,
// and notifications are taken before each reading, so a move made at any
// moment is followed by a reading that sees it. Losing the watch, or events
// the kernel dropped, sets the watch up again and reads main again.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// gitProgram is the git the repository is read with. The package fixes it
// at build time to an absolute store path (nix/packages.nix), so no PATH
// changes which git that is. Unset, as the tests run it, git is looked up
// on PATH.
var gitProgram = "git"

// watcher is a watch on a repository's ref storage.
type watcher interface {
	// events is sent to, without blocking, when main's loose ref or
	// packed-refs changed. Its capacity is one, so any number of changes
	// wait as one.
	events() <-chan struct{}
	// failed is sent the reason once the watch must be set up again to see
	// every change: a watched directory went or appeared, or the kernel
	// dropped events. The bridge then sets up a new watch and reads main
	// again.
	failed() <-chan error
	close()
}

type bridge struct {
	repo    string
	commit  string // the commit file
	request string // the request file
	log     io.Writer

	// watch sets up a watch on the ref storage at the git directory given
	// (watch_linux.go). Tests may replace it.
	watch func(gitDir string) (watcher, error)
	// stop ends run when it is closed, for tests; nil never ends it.
	stop <-chan struct{}
	// waiting, when set, is called each time run waits for a change, and
	// requested each time it has requested a run, for tests.
	waiting   func()
	requested func(tip string)
}

const usage = `usage: onmain watch --repo DIR --commit-file FILE --request-file FILE
       onmain environment --repo DIR --commit-file FILE --request-file FILE --out FILE
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	flags := flag.NewFlagSet("onmain "+os.Args[1], flag.ContinueOnError)
	flags.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	repo := flags.String("repo", "", "the repository whose refs/heads/main is followed")
	commit := flags.String("commit-file", "", "the file the bridge writes the commit to run to")
	request := flags.String("request-file", "", "the file whose existence requests a run")
	var out *string
	switch os.Args[1] {
	case "watch":
	case "environment":
		out = flags.String("out", "", "the environment file to write for the unit")
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	err := flags.Parse(os.Args[2:])
	if err == nil && (*repo == "" || *commit == "" || *request == "" || flags.NArg() != 0 ||
		out != nil && *out == "") {
		flags.Usage()
		err = flag.ErrHelp
	}
	if err != nil {
		os.Exit(2)
	}
	if out != nil {
		err = writeEnvironment(*repo, *commit, *request, *out)
	} else {
		b := &bridge{
			repo:    *repo,
			commit:  *commit,
			request: *request,
			log:     os.Stdout,
			watch:   watchRefs,
		}
		err = b.run()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "onmain %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

// run follows main until the watch cannot be set up again, or until stop.
func (b *bridge) run() error {
	gitDir, err := refStorage(b.repo)
	if err != nil {
		return err
	}
	w, err := b.watch(gitDir)
	if err != nil {
		return fmt.Errorf("watching %s: %w", gitDir, err)
	}
	defer func() { w.close() }()

	var last string
	for {
		// Taken before main is read, so a change after this reading is a
		// notification waiting for the next.
		select {
		case <-w.events():
		default:
		}
		tip, ok, err := readMain(b.repo)
		switch {
		case err != nil:
			fmt.Fprintf(b.log, "reading main: %v; read again at its next change\n", err)
		case ok && tip != last:
			if err := requestRun(b.commit, b.request, tip); err != nil {
				fmt.Fprintf(b.log, "requesting a run for %s: %v; requested again at the next change\n", tip, err)
				break
			}
			last = tip
			fmt.Fprintf(b.log, "requested a run for %s\n", tip)
			if b.requested != nil {
				b.requested(tip)
			}
		}
		if b.waiting != nil {
			b.waiting()
		}
		select {
		case <-w.events():
		case why := <-w.failed():
			w.close()
			fmt.Fprintf(b.log, "watching again and reading main: %v\n", why)
			next, err := b.watch(gitDir)
			if err != nil {
				w = closed{}
				return fmt.Errorf("watching %s again: %w", gitDir, err)
			}
			w = next
		case <-b.stop:
			return nil
		}
	}
}

// errDropped is what a watch fails with when the kernel dropped events.
var errDropped = errors.New("the kernel dropped events")

// closed is the watch left once one could not be set up again.
type closed struct{}

func (closed) events() <-chan struct{} { return nil }
func (closed) failed() <-chan error    { return nil }
func (closed) close()                  {}

// refStorage returns the directory git keeps the repository's refs in, and
// refuses any ref storage but files, the loose refs and packed-refs the
// watch understands.
func refStorage(repo string) (string, error) {
	out, err := git(repo, "rev-parse", "--show-ref-format", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return "", fmt.Errorf("git rev-parse printed %q, not the ref format and the git directory", out)
	}
	if lines[0] != "files" {
		return "", fmt.Errorf("%s keeps its refs in %s storage; onmain follows main only in files storage, as loose refs and packed-refs", repo, lines[0])
	}
	return lines[1], nil
}

var commitID = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// readMain returns the commit refs/heads/main names, and false when there
// is none.
func readMain(repo string) (string, bool, error) {
	out, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/main^{commit}")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	tip := strings.TrimSpace(out)
	if !commitID.MatchString(tip) {
		return "", false, fmt.Errorf("git rev-parse printed %q, not a commit ID", out)
	}
	return tip, true, nil
}

func git(repo string, args ...string) (string, error) {
	cmd := exec.Command(gitProgram, append([]string{"-C", repo}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return string(out), nil
}

// requestRun requests a run at commit: it replaces the commit file whole,
// by a rename, and then creates the request file. The order is the
// bridge's half of the handoff writeEnvironment describes.
func requestRun(commitFile, requestFile, commit string) error {
	if err := replace(commitFile, commit+"\n"); err != nil {
		return err
	}
	f, err := os.OpenFile(requestFile, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// afterStep, when set, is called with the name of each step of
// writeEnvironment as it completes, so tests can make the bridge write
// between steps.
var afterStep func(step string)

func stepped(step string) {
	if afterStep != nil {
		afterStep(step)
	}
}

// writeEnvironment writes the environment file the unit's command reads,
// from the commit file the bridge wrote, and consumes the bridge's request.
//
// The bridge replaces the commit file and then creates the request; this
// removes the request and then reads the commit file. So a commit the
// bridge writes at any moment is either the one this run reads, or leaves
// a request that starts the unit again after this run:
//
//   - written before the read: this run reads it, or a newer one. Were its
//     request created after the removal, it also starts one more run,
//     which reads it again.
//   - written after the read, while this step writes the environment, the
//     unit's command runs, or the unit fails or stops: its request was
//     created after the removal, so it remains. The path unit holds no
//     watch while the unit is active, but looks for the file again when
//     the unit becomes inactive or failed, and starts it again.
//
// Read before removed, a commit written between the two would be lost:
// this run would use the older commit, and the removal would take the
// newer one's request.
//
// The request is removed before anything that can fail, so a run that
// fails, here or in the unit's command, leaves nothing to start the unit
// again.
//
// It runs as root, and the bridge's user owns both files and their
// directory. So the commit file is opened without following a symbolic
// link or waiting on a FIFO, must be a regular file, and is accepted only
// as one commit ID and a newline. What it holds otherwise is never
// printed. The repository is the flag's, never the bridge's.
func writeEnvironment(repo, commitFile, requestFile, out string) error {
	if err := os.Remove(requestFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing the bridge's request: %w", err)
	}
	stepped("removed")
	if !environmentSafe(repo) {
		return fmt.Errorf("%q holds a space, a quote, a backslash, a $, a backtick or a character that is not printable ASCII, which the environment file cannot carry as written", repo)
	}
	commit, err := readCommit(commitFile)
	stepped("read")
	if err != nil {
		return err
	}
	return replace(out, fmt.Sprintf("VALLEY_REPOSITORY=%s\nVALLEY_COMMIT=%s\n", repo, commit))
}

// readCommit returns the one commit ID the commit file holds.
func readCommit(commitFile string) (string, error) {
	f, err := os.OpenFile(commitFile, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", fmt.Errorf("reading the commit the bridge wrote: %w", err)
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", commitFile)
	}
	// One byte more than the longest commit ID and its newline, so a
	// longer file is seen to be one.
	data, err := io.ReadAll(io.LimitReader(f, 66))
	if err != nil {
		return "", fmt.Errorf("reading the commit the bridge wrote: %w", err)
	}
	commit, ok := strings.CutSuffix(string(data), "\n")
	if !ok || !commitID.MatchString(commit) {
		return "", fmt.Errorf("%s holds something other than one commit ID, so no environment is written for it", commitFile)
	}
	return commit, nil
}

// environmentSafe says whether s can stand unquoted as a value in a
// systemd environment file and be read back as written.
func environmentSafe(s string) bool {
	for _, r := range s {
		if r <= ' ' || r > '~' || strings.ContainsRune("\"'\\$`", r) {
			return false
		}
	}
	return s != ""
}

// replace replaces the file at path whole with content, by a rename, so a
// reader never sees part of it.
func replace(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".onmain-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(content)
	if err == nil {
		err = tmp.Chmod(0o644)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
