package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// restartQuiet is how long the keyboard must be quiet before the dashboard
// restarts onto a new klaus, so a restart never lands mid-interaction.
const restartQuiet = 10 * time.Second

// binCheck notices when klaus on PATH names a different file than the
// running dashboard's binary, as when self-update installs a new klaus into
// the nix profile, so the dashboard can restart onto it.
type binCheck struct {
	running string          // the running binary, symlinks resolved
	name    string          // the program name the dashboard was started as (invokedAs)
	path    func() string   // the PATH to search
	failed  map[string]bool // files an exec failed for: never tried again
	// home is the PATH directory whose valley-flow was the running binary
	// when a dashboard started as valley-flow began, else "". It tells a
	// rollback of that installation from another klaus on PATH.
	home string
	// version runs a klaus with --version; nil runs it for real.
	version func(path string) (string, error)
}

func newBinCheck() *binCheck {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return startBinCheck(exe, invokedAs(os.Args[0]), func() string { return os.Getenv("PATH") })
}

// startBinCheck is the check of a dashboard running the binary running,
// started as program name, searching path. It notes the dashboard's home
// as path is now, so call it as the dashboard starts.
func startBinCheck(running, name string, path func() string) *binCheck {
	b := &binCheck{running: running, name: name, path: path, failed: map[string]bool{}}
	if name == helperName {
		if dir, p, ok := firstOnPath(path(), name); ok {
			if r, err := filepath.EvalSymlinks(p); err == nil && r == running {
				b.home = dir
			}
		}
	}
	return b
}

// firstOnPath returns the first executable name in path's absolute
// directories, and that directory. A relative entry is skipped, as valley
// flow skips it, where exec.LookPath would stop at it.
func firstOnPath(path, name string) (dir, file string, ok bool) {
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if p, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return dir, p, true
		}
	}
	return "", "", false
}

// candidate returns the file the dashboard would restart onto, symlinks
// resolved, when that is not the running binary or a file an exec failed
// for. That is the first file on PATH with the name the dashboard was
// started as, since the restart keeps its arguments, the program name
// among them: for valley-flow, the file valley flow runs. The exception is
// a home that lost valley-flow but holds klaus, as a profile rolled back
// to a klaus that ships no valley-flow does: that klaus is the candidate,
// so a valley-flow elsewhere on PATH does not hide the rollback.
func (b *binCheck) candidate() (string, bool) {
	if b == nil {
		return "", false
	}
	p, ok := b.rolledBack()
	if !ok {
		_, p, ok = firstOnPath(b.path(), b.name)
	}
	if !ok {
		return "", false
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil || r == b.running || b.failed[r] {
		return "", false
	}
	return r, true
}

// rolledBack returns the home's klaus when the home no longer holds the
// name the dashboard was started as.
func (b *binCheck) rolledBack() (string, bool) {
	if b.home == "" {
		return "", false
	}
	if _, err := exec.LookPath(filepath.Join(b.home, b.name)); err == nil {
		return "", false
	}
	p, err := exec.LookPath(filepath.Join(b.home, "klaus"))
	return p, err == nil
}

// binUpdateMsg reports a new klaus on PATH that runs.
type binUpdateMsg struct {
	path    string
	version string // as its --version reports it, e.g. 0.4.1-1a2b3c4
}

// restartWaitMsg re-checks whether the dashboard is idle enough to restart.
type restartWaitMsg struct{}

// restartTargetMsg is what klaus on PATH names just before a restart: the
// new klaus to exec, or nil when there is none any more, as when a rollback
// put back the running binary.
type restartTargetMsg struct{ target *binUpdateMsg }

// probeBin returns the new klaus on PATH if it runs --version, else nil.
// The probe can take seconds, so PATH is resolved again after it: if it no
// longer names the probed file, as when a rollback ran meanwhile, there is
// nothing to restart onto, and the next check starts over.
func probeBin(b *binCheck) *binUpdateMsg {
	p, ok := b.candidate()
	if !ok {
		return nil
	}
	version := b.version
	if version == nil {
		version = runVersion
	}
	v, err := version(p)
	if err != nil {
		return nil // not runnable (yet): the next check looks again
	}
	if now, ok := b.candidate(); !ok || now != p {
		return nil
	}
	return &binUpdateMsg{path: p, version: v}
}

// runVersion runs a klaus with --version and returns its version.
func runVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	c := exec.CommandContext(ctx, path, "--version")
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return "", err
	}
	v, _, _ := strings.Cut(strings.TrimSpace(out.String()), "\n")
	return strings.TrimPrefix(v, "klaus version "), nil
}

// checkBinCmd looks for a new klaus on PATH and, if it runs --version,
// reports it.
func checkBinCmd(b *binCheck) tea.Cmd {
	return func() tea.Msg {
		if next := probeBin(b); next != nil {
			return *next
		}
		return nil
	}
}

func restartWaitCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return restartWaitMsg{} })
}

// restartWhenIdle waits while a key was pressed in the last restartQuiet.
// Once the keyboard is quiet, it resolves klaus on PATH again, since the
// profile may have changed while it waited.
func (m dashboardModel) restartWhenIdle(now time.Time) (dashboardModel, tea.Cmd) {
	if now.Sub(m.lastKeyAt) < restartQuiet {
		return m, restartWaitCmd()
	}
	b := m.bin
	return m, func() tea.Msg { return restartTargetMsg{target: probeBin(b)} }
}

// restartOnto quits so the dashboard can exec target, the klaus PATH names
// now. With no target, the pending restart and its notice are dropped.
func (m dashboardModel) restartOnto(target *binUpdateMsg, now time.Time) (dashboardModel, tea.Cmd) {
	if target == nil {
		m.restart, m.binNotice, m.binNoticeErr = nil, "", false
		return m, nil
	}
	m.restart = target
	m.binNotice, m.binNoticeErr = "klaus updated to "+target.version+"; restarting", false
	if now.Sub(m.lastKeyAt) < restartQuiet {
		return m, restartWaitCmd() // a key came in meanwhile
	}
	m.restarting = true
	return m, tea.Quit
}
