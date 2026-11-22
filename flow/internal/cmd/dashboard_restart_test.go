package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// profileFixture is a PATH with a nix profile's bin ahead of the system
// klaus. Its klaus is a symlink into a store of klaus builds, each a script
// that answers --version.
type profileFixture struct {
	t       *testing.T
	root    string
	profile string // the profile's bin dir
	system  string // the system klaus
	path    string // the PATH: a relative entry, the profile, the system bin
}

func newProfileFixture(t *testing.T) *profileFixture {
	f := &profileFixture{t: t, root: t.TempDir()}
	f.profile = filepath.Join(f.root, "profile", "bin")
	if err := os.MkdirAll(f.profile, 0o755); err != nil {
		t.Fatal(err)
	}
	f.system = f.build("system", "0.4.0-0000000")
	sep := string(filepath.ListSeparator)
	f.path = "relative/bin" + sep + f.profile + sep + filepath.Dir(f.system)
	return f
}

// build makes a klaus build in the store and returns its bin/klaus.
func (f *profileFixture) build(name, version string) string {
	f.t.Helper()
	p := filepath.Join(f.root, "store", name, "bin", "klaus")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho 'klaus version "+version+"'\n"), 0o755); err != nil {
		f.t.Fatal(err)
	}
	return p
}

// buildWithHelper makes a klaus build that also ships valley-flow, a
// symlink to its klaus, as the package does, and returns its bin/klaus.
func (f *profileFixture) buildWithHelper(name, version string) string {
	f.t.Helper()
	p := f.build(name, version)
	f.shipHelper(p)
	return p
}

// shipHelper adds valley-flow beside klaus, a symlink to it.
func (f *profileFixture) shipHelper(klaus string) {
	f.t.Helper()
	if err := os.Symlink("klaus", filepath.Join(filepath.Dir(klaus), helperName)); err != nil {
		f.t.Fatal(err)
	}
}

// link points the profile's klaus at target, as a profile install does.
func (f *profileFixture) link(target string) {
	f.t.Helper()
	f.linkAs("klaus", target)
}

// linkAs points the profile's program name at target; "" removes it.
func (f *profileFixture) linkAs(name, target string) {
	f.t.Helper()
	_ = os.Remove(filepath.Join(f.profile, name))
	if target == "" {
		return
	}
	if err := os.Symlink(target, filepath.Join(f.profile, name)); err != nil {
		f.t.Fatal(err)
	}
}

// install links every program of the build whose bin/klaus is klaus into
// the profile, and removes the profile's valley-flow when the build has
// none, as a profile install of that build does.
func (f *profileFixture) install(klaus string) {
	f.t.Helper()
	f.link(klaus)
	helper := filepath.Join(filepath.Dir(klaus), helperName)
	if _, err := os.Lstat(helper); err != nil {
		helper = ""
	}
	f.linkAs(helperName, helper)
}

func (f *profileFixture) check(running string) *binCheck {
	return f.checkAs("klaus", running)
}

// checkAs is the check of a dashboard started as program name, which notes
// its home from PATH as it is now.
func (f *profileFixture) checkAs(name, running string) *binCheck {
	return startBinCheck(running, name, func() string { return f.path })
}

// TestBinCheckFindsChangedKlaus drives the check with an injected PATH and
// running executable.
func TestBinCheckFindsChangedKlaus(t *testing.T) {
	f := newProfileFixture(t)
	oldStore := f.build("old", "0.4.1-1111111")
	newStore := f.build("new", "0.4.1-2222222")
	b := f.check(oldStore)

	// Nothing in the profile yet: PATH finds the system klaus, which is not
	// what runs.
	if got, ok := b.candidate(); !ok || got != f.system {
		t.Errorf("empty profile: got %q, %v; want the system klaus", got, ok)
	}
	f.link(oldStore)
	if got, ok := b.candidate(); ok {
		t.Errorf("profile links the running klaus: got %q, want no change", got)
	}
	f.link(newStore)
	if got, ok := b.candidate(); !ok || got != newStore {
		t.Errorf("profile links a new klaus: got %q, %v; want %q", got, ok, newStore)
	}
	b.failed[newStore] = true
	if got, ok := b.candidate(); ok {
		t.Errorf("exec of %s failed before: got %q, want it left alone", newStore, got)
	}
}

// TestBinCheckFollowsTheStartedName: a dashboard started as valley-flow
// restarts onto valley-flow on PATH, and onto klaus from its home when the
// home has no valley-flow any more, as after a rollback to a build without
// one, even when a later directory has a valley-flow. One started as klaus
// looks for klaus alone.
func TestBinCheckFollowsTheStartedName(t *testing.T) {
	f := newProfileFixture(t)
	running := f.buildWithHelper("old", "0.4.1-1111111")
	newStore := f.buildWithHelper("new", "0.4.1-2222222")
	before := f.build("before", "0.4.0-3333333")
	f.install(running)
	asHelper, asKlaus := f.checkAs(helperName, running), f.checkAs("klaus", running)

	for _, b := range []*binCheck{asHelper, asKlaus} {
		if got, ok := b.candidate(); ok {
			t.Errorf("started as %s, the profile holds the running build: got %q, want no change", b.name, got)
		}
	}
	f.install(newStore)
	for _, b := range []*binCheck{asHelper, asKlaus} {
		if got, ok := b.candidate(); !ok || got != newStore {
			t.Errorf("started as %s, the profile holds a new build: got %q, %v; want %q", b.name, got, ok, newStore)
		}
	}
	// Each name finds its own program.
	f.link(running)
	if got, ok := asHelper.candidate(); !ok || got != newStore {
		t.Errorf("started as valley-flow, PATH's valley-flow is new: got %q, %v; want %q", got, ok, newStore)
	}
	if got, ok := asKlaus.candidate(); ok {
		t.Errorf("started as klaus, PATH's klaus is the running one: got %q, want no change", got)
	}
	f.install(before)
	if err := os.Symlink(filepath.Join(filepath.Dir(newStore), helperName), filepath.Join(filepath.Dir(f.system), helperName)); err != nil {
		t.Fatal(err)
	}
	for _, b := range []*binCheck{asHelper, asKlaus} {
		if got, ok := b.candidate(); !ok || got != before {
			t.Errorf("started as %s, the profile rolled back to a build without valley-flow: got %q, %v; want %q", b.name, got, ok, before)
		}
	}
}

// TestBinCheckBesideAnotherInstallation: with two installations on PATH,
// a dashboard started as valley-flow restarts onto what valley flow runs,
// and one started as klaus onto the first klaus, as before valley-flow.
func TestBinCheckBesideAnotherInstallation(t *testing.T) {
	sep := string(filepath.ListSeparator)
	t.Run("an older klaus ahead", func(t *testing.T) {
		// The system's klaus, from before valley-flow, comes first on PATH.
		f := newProfileFixture(t)
		f.path = filepath.Dir(f.system) + sep + f.profile
		running, newStore := f.buildWithHelper("old", "0.4.1-1111111"), f.buildWithHelper("new", "0.4.1-2222222")
		f.install(running)
		b := f.checkAs(helperName, running)
		if got, ok := b.candidate(); ok {
			t.Errorf("valley flow runs the running build: got %q, want no change", got)
		}
		f.install(newStore)
		if got, ok := b.candidate(); !ok || got != newStore {
			t.Errorf("the profile holds a new build: got %q, %v; want %q", got, ok, newStore)
		}
	})
	t.Run("a new build lands ahead", func(t *testing.T) {
		// The dashboard runs the system's build; self-update fills the
		// profile, which comes first on PATH.
		f := newProfileFixture(t)
		f.path = f.profile + sep + filepath.Dir(f.system)
		f.shipHelper(f.system)
		newStore := f.buildWithHelper("new", "0.4.1-2222222")
		b := f.checkAs(helperName, f.system)
		f.install(newStore)
		if got, ok := b.candidate(); !ok || got != newStore {
			t.Errorf("got %q, %v; want %q", got, ok, newStore)
		}
	})
	t.Run("started as klaus", func(t *testing.T) {
		// A valley-flow ahead goes unseen; a klaus ahead is taken, although
		// the dashboard's klaus is further down.
		f := newProfileFixture(t)
		f.path = f.profile + sep + filepath.Dir(f.system)
		newStore := f.buildWithHelper("new", "0.4.1-2222222")
		b := f.checkAs("klaus", f.system)
		f.linkAs(helperName, filepath.Join(filepath.Dir(newStore), helperName))
		if got, ok := b.candidate(); ok {
			t.Errorf("a valley-flow ahead: got %q, want no change", got)
		}
		f.link(newStore)
		if got, ok := b.candidate(); !ok || got != newStore {
			t.Errorf("a klaus ahead: got %q, %v; want %q", got, ok, newStore)
		}
	})
}

// pendingRestart returns a dashboard that found newStore on PATH just after
// a key press, so its restart waits.
func pendingRestart(t *testing.T, b *binCheck, newStore, version string) dashboardModel {
	t.Helper()
	m := dashboardModel{bin: b}
	next, _ := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(dashboardModel)
	next, cmd := m.update(binUpdateMsg{path: newStore, version: version})
	m = next.(dashboardModel)
	if m.binNotice != "klaus updated to "+version+"; restarting" {
		t.Errorf("notice = %q", m.binNotice)
	}
	if isQuit(cmd) {
		t.Fatal("restarted at once, without showing the notice")
	}
	next, cmd = m.update(restartWaitMsg{})
	m = next.(dashboardModel)
	if isQuit(cmd) || m.restarting {
		t.Fatal("restarted a moment after a key press")
	}
	return m
}

// quietRestart lets the keyboard go quiet and runs the restart's last check.
func quietRestart(t *testing.T, m dashboardModel) (dashboardModel, bool) {
	t.Helper()
	m.lastKeyAt = time.Now().Add(-restartQuiet)
	next, cmd := m.update(restartWaitMsg{})
	m = next.(dashboardModel)
	if cmd == nil {
		t.Fatal("a quiet keyboard did not re-check PATH")
	}
	msg := cmd()
	if _, ok := msg.(restartTargetMsg); !ok {
		t.Fatalf("a quiet keyboard sent %T, want the re-check of PATH", msg)
	}
	next, cmd = m.update(msg)
	return next.(dashboardModel), isQuit(cmd)
}

// TestDashboardRestartWaitsForQuietKeyboard: a new klaus restarts the
// dashboard only once no key has been pressed for restartQuiet.
func TestDashboardRestartWaitsForQuietKeyboard(t *testing.T) {
	f := newProfileFixture(t)
	running, newStore := f.build("old", "0.4.1-1111111"), f.build("new", "0.4.1-2222222")
	f.link(newStore)
	m := pendingRestart(t, f.check(running), newStore, "0.4.1-2222222")

	m, quit := quietRestart(t, m)
	if !quit || !m.restarting || m.restart == nil || m.restart.path != newStore {
		t.Fatalf("quiet keyboard: quit=%v restarting=%v restart=%+v; want a restart onto %s", quit, m.restarting, m.restart, newStore)
	}
}

// TestDashboardRestartRechecksPath: the profile changes while the restart
// waits for the keyboard. The dashboard execs what PATH names at the end,
// or drops the restart when that is the running klaus again.
func TestDashboardRestartRechecksPath(t *testing.T) {
	t.Run("a newer klaus", func(t *testing.T) {
		f := newProfileFixture(t)
		running, first, second := f.build("old", "0.4.1-1111111"), f.build("first", "0.4.1-2222222"), f.build("second", "0.4.1-3333333")
		f.link(first)
		m := pendingRestart(t, f.check(running), first, "0.4.1-2222222")

		f.link(second)
		m, quit := quietRestart(t, m)
		if !quit || !m.restarting || m.restart == nil || m.restart.path != second || m.restart.version != "0.4.1-3333333" {
			t.Fatalf("quit=%v restarting=%v restart=%+v; want a restart onto %s, 0.4.1-3333333", quit, m.restarting, m.restart, second)
		}
		if m.binNotice != "klaus updated to 0.4.1-3333333; restarting" {
			t.Errorf("notice = %q, want the version restarted onto", m.binNotice)
		}
	})
	t.Run("rolled back to the running klaus", func(t *testing.T) {
		f := newProfileFixture(t)
		running, newStore := f.build("old", "0.4.1-1111111"), f.build("new", "0.4.1-2222222")
		f.link(newStore)
		m := pendingRestart(t, f.check(running), newStore, "0.4.1-2222222")

		f.link(running)
		m, quit := quietRestart(t, m)
		if quit || m.restarting || m.restart != nil {
			t.Fatalf("quit=%v restarting=%v restart=%+v; want the restart dropped", quit, m.restarting, m.restart)
		}
		if m.binNotice != "" {
			t.Errorf("notice = %q, want it cleared", m.binNotice)
		}
	})
}

// TestDashboardRestartPathChangesDuringProbe: a rollback while --version
// runs on the new klaus means the dashboard execs nothing; the pending
// restart is dropped, and a periodic check reports nothing.
func TestDashboardRestartPathChangesDuringProbe(t *testing.T) {
	f := newProfileFixture(t)
	running, newStore := f.build("old", "0.4.1-1111111"), f.build("new", "0.4.1-2222222")
	b := f.check(running)
	rollBackDuringProbe := func(path string) (string, error) {
		f.link(running)
		return "0.4.1-2222222", nil
	}

	f.link(newStore)
	b.version = rollBackDuringProbe
	if msg := checkBinCmd(b)(); msg != nil {
		t.Errorf("periodic check reported %+v; PATH named the running klaus again after the probe", msg)
	}

	f.link(newStore)
	b.version = nil
	m := pendingRestart(t, b, newStore, "0.4.1-2222222")
	b.version = rollBackDuringProbe
	m, quit := quietRestart(t, m)
	if quit || m.restarting || m.restart != nil || m.binNotice != "" {
		t.Fatalf("quit=%v restarting=%v restart=%+v notice=%q; want the restart dropped", quit, m.restarting, m.restart, m.binNotice)
	}
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}
