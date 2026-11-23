package location

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testLayout(t *testing.T) Layout {
	t.Helper()
	home := t.TempDir()
	return Layout{Home: home, StateHome: filepath.Join(home, ".local", "state")}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestParseGeneration(t *testing.T) {
	for in, want := range map[string]Generation{"": Legacy, "legacy": Legacy, "bridge": Bridge, "flow": Flow} {
		if got, err := ParseGeneration(in); err != nil || got != want {
			t.Errorf("ParseGeneration(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"Legacy", "w1", " flow"} {
		if _, err := ParseGeneration(in); err == nil {
			t.Errorf("ParseGeneration(%q) accepted it", in)
		}
	}
	if Legacy.Next() != Bridge || Bridge.Next() != Flow || Flow.Next() != "" {
		t.Errorf("Next: %q %q %q", Legacy.Next(), Bridge.Next(), Flow.Next())
	}
}

// A declaration is the file under the flow names when one is there, else
// the legacy file, one file at a time.
func TestDeclarationsFallBackPerFile(t *testing.T) {
	l := testLayout(t)
	legacy, flow := filepath.Join(l.Home, ".klaus", "config.json"), filepath.Join(l.Home, ".valley", "flow", "config.json")
	check := func(want string) {
		t.Helper()
		if got, err := l.HostConfig(); err != nil || got != want {
			t.Errorf("HostConfig() = %q, %v; want %q", got, err, want)
		}
	}
	check(legacy)
	write(t, legacy, "{}")
	check(legacy)
	write(t, flow, "{}")
	check(flow)

	repo := t.TempDir()
	write(t, filepath.Join(repo, ".klaus", "config.json"), "{}")
	write(t, filepath.Join(repo, ".klaus", "prompt.md"), "old")
	write(t, filepath.Join(repo, ".valley", "flow", "prompt.md"), "new")
	for name, want := range map[string]string{
		ProjectConfig: filepath.Join(repo, ".klaus", "config.json"),
		WorkerPrompt:  filepath.Join(repo, ".valley", "flow", "prompt.md"),
		Principles:    filepath.Join(repo, ".klaus", "principles.md"),
	} {
		if got, err := ProjectFile(repo, name); err != nil || got != want {
			t.Errorf("ProjectFile(%s) = %q, %v; want %q", name, got, err, want)
		}
	}
}

// A file under the flow names that can't be read is still the one: the
// reader's error is never a fall back to the legacy file, and a symlink
// to nothing, which a reader would take for no file, is an error.
func TestDeclarationPresentButBroken(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".klaus", "prompt.md"), "old")
	flow := filepath.Join(repo, ".valley", "flow", "prompt.md")
	mkdir(t, flow) // a directory: present, and unreadable as a file
	if got, err := ProjectFile(repo, WorkerPrompt); err != nil || got != flow {
		t.Errorf("ProjectFile = %q, %v; want %q", got, err, flow)
	}
	if err := os.Remove(flow); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, "nowhere"), flow); err != nil {
		t.Fatal(err)
	}
	if got, err := ProjectFile(repo, WorkerPrompt); err == nil {
		t.Errorf("ProjectFile with a dangling symlink = %q, want an error", got)
	}

	// A flow root that is not a directory can't be checked: an error.
	other := t.TempDir()
	write(t, filepath.Join(other, ".valley"), "a file")
	write(t, filepath.Join(other, ".klaus", "prompt.md"), "old")
	if got, err := ProjectFile(other, WorkerPrompt); err == nil {
		t.Errorf("ProjectFile under a .valley file = %q, want an error", got)
	}
}

func TestSingleValuedState(t *testing.T) {
	l := testLayout(t)
	legacy, flow := filepath.Join(l.Home, ".klaus", "projects.json"), filepath.Join(l.Home, ".valley", "flow", "projects.json")

	// With neither, new state goes where the generation puts it.
	for g, want := range map[Generation]string{Legacy: legacy, Bridge: flow, Flow: flow} {
		gl := l
		gl.Gen = g
		if got, err := gl.Registry(); err != nil || got != want {
			t.Errorf("%s: Registry() with none = %q, %v; want %q", g, got, err, want)
		}
	}
	// With one, it is the one there, whatever the generation.
	write(t, legacy, "{}")
	for _, g := range Generations {
		gl := l
		gl.Gen = g
		if got, err := gl.Registry(); err != nil || got != legacy {
			t.Errorf("%s: Registry() with the legacy file = %q, %v; want %q", g, got, err, legacy)
		}
	}
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}

	write(t, flow, "{}")
	if got, err := l.Registry(); err != nil || got != flow {
		t.Errorf("Registry() with the flow file = %q, %v; want %q", got, err, flow)
	}

	write(t, legacy, "{}")
	_, err := l.Registry()
	var conflict *ConflictError
	if !errors.As(err, &conflict) || !reflect.DeepEqual(conflict.Paths, []string{legacy, flow}) {
		t.Fatalf("Registry() with both = %v, want a ConflictError naming both", err)
	}
	if !strings.Contains(err.Error(), legacy) || !strings.Contains(err.Error(), flow) {
		t.Errorf("conflict message names neither path: %v", err)
	}

	// One file under both names is one registry.
	if err := os.Remove(flow); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(legacy, flow); err != nil {
		t.Fatal(err)
	}
	if got, err := l.Registry(); err != nil || got != legacy {
		t.Errorf("Registry() through a symlink = %q, %v; want %q", got, err, legacy)
	}
	// A symlink names the file it points at, so a save renames over the
	// file rather than the link.
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(l.Home, "real.json"), "{}")
	if err := os.Symlink(filepath.Join(l.Home, "real.json"), legacy); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(flow); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(legacy, flow); err != nil {
		t.Fatal(err)
	}
	if got, err := l.Registry(); err != nil || got != filepath.Join(l.Home, "real.json") {
		t.Errorf("Registry() through symlinks = %q, %v; want the file itself", got, err)
	}
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(l.Home, "real.json"), legacy); err != nil {
		t.Fatal(err)
	}

	// A hard link is two names a save would part: refused.
	if err := os.Remove(flow); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(legacy, flow); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Registry(); !errors.As(err, &conflict) {
		t.Errorf("Registry() through a hard link = %v, want a ConflictError", err)
	}

	// Self-update's legacy state is under the state directory.
	state := filepath.Join(l.StateHome, "klaus", "self-update.json")
	write(t, state, "{}")
	if got, err := l.SelfUpdateState(); err != nil || got != state {
		t.Errorf("SelfUpdateState() = %q, %v; want %q", got, err, state)
	}
	write(t, filepath.Join(l.Home, ".valley", "flow", "self-update.json"), "{}")
	if _, err := l.SelfUpdateState(); !errors.As(err, &conflict) {
		t.Errorf("SelfUpdateState() with both = %v, want a ConflictError", err)
	}

	mkdir(t, filepath.Join(l.Home, ".valley", "flow", "memory"))
	if got, err := l.Memory(); err != nil || got != filepath.Join(l.Home, ".valley", "flow", "memory") {
		t.Errorf("Memory() = %q, %v", got, err)
	}
}

// Legacy allocates the legacy names, and bridge and flow the flow names for
// home and project state, keeping the plumbing's legacy names; the host
// config decides the generation when the layout doesn't, and a bad one
// allocates nothing.
func TestAllocation(t *testing.T) {
	l := testLayout(t)
	repo := t.TempDir()
	home := func(name string, flow bool) string {
		if flow {
			return filepath.Join(l.Home, ".valley", "flow", name)
		}
		return filepath.Join(l.Home, ".klaus", name)
	}
	for _, g := range Generations {
		write(t, filepath.Join(l.Home, ".klaus", "config.json"), `{"naming_generation": "`+string(g)+`"}`)
		if got, err := l.Generation(); err != nil || got != g {
			t.Errorf("Generation() = %q, %v; want %q", got, err, g)
		}
		flow := g != Legacy
		if got := Allocates(g); got != map[bool]Generation{false: Legacy, true: Bridge}[flow] {
			t.Errorf("Allocates(%s) = %s", g, got)
		}
		for name, f := range map[string]func() (string, error){
			"sessions":          l.NewSessionsRoot,
			"sync.log":          l.SyncLog,
			"consult-snapshots": l.ConsultSnapshots,
			"memory":            l.Memory,
			"projects.json":     l.Registry,
		} {
			if got, err := f(); err != nil || got != home(name, flow) {
				t.Errorf("%s: %s = %q, %v; want %q", g, name, got, err, home(name, flow))
			}
		}
		if dir, err := l.NewSession("session-x"); err != nil || dir != filepath.Join(home("sessions", flow), "session-x") {
			t.Errorf("%s: NewSession = %q, %v", g, dir, err)
		}
		state := filepath.Join(l.StateHome, "klaus", "self-update.json")
		if flow {
			state = home("self-update.json", true)
		}
		if got, err := l.SelfUpdateState(); err != nil || got != state {
			t.Errorf("%s: SelfUpdateState = %q, %v; want %q", g, got, err, state)
		}
		project := filepath.Join(repo, ".klaus", "prompt.md")
		if flow {
			project = filepath.Join(repo, ".valley", "flow", "prompt.md")
		}
		if got, err := l.ScaffoldProjectFile(repo, WorkerPrompt); err != nil || got != project {
			t.Errorf("%s: ScaffoldProjectFile = %q, %v; want %q", g, got, err, project)
		}
		// The plumbing older builds share keeps its legacy names.
		if got, err := l.StandaloneStore("/repo/.git"); err != nil || got != filepath.Join("/repo/.git", "klaus") {
			t.Errorf("%s: StandaloneStore = %q, %v", g, got, err)
		}
		if got := AgentsSession("session-x"); got != "klaus-agents-session-x" {
			t.Errorf("%s: AgentsSession = %q", g, got)
		}
	}

	// A sync log under the other name stays where it is; appends go to the
	// one the generation names, and both are listed.
	write(t, home("sync.log", false), "old\n")
	if got, err := l.SyncLog(); err != nil || got != home("sync.log", true) {
		t.Errorf("SyncLog under flow with a legacy log = %q, %v", got, err)
	}
	write(t, home("sync.log", true), "new\n")
	if got := l.SyncLogs(); !reflect.DeepEqual(got, []string{home("sync.log", false), home("sync.log", true)}) {
		t.Errorf("SyncLogs = %v", got)
	}
	// So do consult snapshot directories.
	write(t, filepath.Join(home("consult-snapshots", true), "s", "f"), "")
	if got := l.ConsultSnapshotDirs(); !reflect.DeepEqual(got, []string{home("consult-snapshots", true)}) {
		t.Errorf("ConsultSnapshotDirs = %v", got)
	}

	write(t, filepath.Join(l.Home, ".valley", "flow", "config.json"), `{"naming_generation": "w2"}`)
	if _, err := l.NewSession("session-y"); err == nil || !strings.Contains(err.Error(), "w2") {
		t.Errorf("NewSession under a bad generation = %v, want its error", err)
	}
	write(t, filepath.Join(l.Home, ".valley", "flow", "config.json"), `{"naming_generation": `)
	if _, err := l.ConsultSnapshots(); err == nil {
		t.Error("ConsultSnapshots under a malformed host config succeeded")
	}
}

// What a command writes fresh outside flow's state follows the running
// user's host setting, and the legacy names when it can't be read.
func TestWritesFollowsTheHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct {
		config                    string
		gen                       Generation
		temp, base, self, command string
	}{
		{"", Legacy, "klaus-merge-*", "klaus-sessions", "klaus", "klaus ready"},
		{`{"naming_generation": "legacy"}`, Legacy, "klaus-merge-*", "klaus-sessions", "klaus", "klaus ready"},
		{`{"naming_generation": "bridge"}`, Bridge, "valley-flow-merge-*", "valley-flow-sessions", "flow", "valley flow ready"},
		{`{"naming_generation": "flow"}`, Bridge, "valley-flow-merge-*", "valley-flow-sessions", "flow", "valley flow ready"},
		{`{"naming_generation": "w2"}`, Legacy, "klaus-merge-*", "klaus-sessions", "klaus", "klaus ready"},
	}
	for _, tc := range cases {
		path := filepath.Join(home, ".klaus", "config.json")
		if tc.config == "" {
			_ = os.Remove(path)
		} else {
			write(t, path, tc.config)
		}
		if got := Writes(); got != tc.gen {
			t.Errorf("%s: Writes() = %s, want %s", tc.config, got, tc.gen)
		}
		if got := TempPattern("merge-*"); got != tc.temp {
			t.Errorf("%s: TempPattern = %q, want %q", tc.config, got, tc.temp)
		}
		if got := WorktreeBase(Writes()); got != filepath.Join(os.TempDir(), tc.base) {
			t.Errorf("%s: WorktreeBase = %q", tc.config, got)
		}
		if got := Self(); got != tc.self {
			t.Errorf("%s: Self() = %q, want %q", tc.config, got, tc.self)
		}
		if got := Command("ready"); got != tc.command {
			t.Errorf("%s: Command(ready) = %q, want %q", tc.config, got, tc.command)
		}
	}
}

func TestSessions(t *testing.T) {
	l := testLayout(t)
	oldRoot, newRoot := l.SessionRoots()[0], l.SessionRoots()[1]
	mkdir(t, filepath.Join(oldRoot, "session-1"))
	mkdir(t, filepath.Join(newRoot, "session-2"))
	write(t, filepath.Join(newRoot, "stray.txt"), "")

	got, err := l.Sessions()
	want := []Session{{"session-1", filepath.Join(oldRoot, "session-1")}, {"session-2", filepath.Join(newRoot, "session-2")}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Sessions() = %v, %v; want %v", got, err, want)
	}
	if dir, err := l.Session("session-2"); err != nil || dir != want[1].Dir {
		t.Errorf("Session(session-2) = %q, %v", dir, err)
	}

	// An unknown ID fails, and creates nothing.
	if _, err := l.Session("session-9"); !errors.Is(err, ErrUnknownSession) {
		t.Errorf("Session(unknown) = %v, want ErrUnknownSession", err)
	}
	for _, root := range l.SessionRoots() {
		if _, err := os.Stat(filepath.Join(root, "session-9")); !os.IsNotExist(err) {
			t.Errorf("looking up an unknown session made %s", root)
		}
	}
	for _, id := range []string{"", ".", "..", "../session-1", "a/b"} {
		if _, err := l.Session(id); err == nil || errors.Is(err, ErrUnknownSession) {
			t.Errorf("Session(%q) = %v, want it refused", id, err)
		}
	}
	if _, err := l.NewSession("session-2"); err == nil {
		t.Error("NewSession of an existing ID succeeded")
	}

	// The same ID under both roots names no session, and the error names both.
	mkdir(t, filepath.Join(newRoot, "session-1"))
	got, err = l.Sessions()
	var conflict *ConflictError
	if !errors.As(err, &conflict) || !reflect.DeepEqual(conflict.Paths, []string{filepath.Join(oldRoot, "session-1"), filepath.Join(newRoot, "session-1")}) {
		t.Fatalf("Sessions() with a duplicate: err = %v", err)
	}
	if !reflect.DeepEqual(got, want[1:]) {
		t.Errorf("Sessions() with a duplicate = %v, want the rest: %v", got, want[1:])
	}
	if _, err := l.Session("session-1"); !errors.As(err, &conflict) {
		t.Errorf("Session(duplicate) = %v, want a ConflictError", err)
	}

	// One directory under both roots is one session.
	if err := os.Remove(filepath.Join(newRoot, "session-1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(oldRoot, "session-1"), filepath.Join(newRoot, "session-1")); err != nil {
		t.Fatal(err)
	}
	if dir, err := l.Session("session-1"); err != nil || dir != filepath.Join(oldRoot, "session-1") {
		t.Errorf("Session through a symlink = %q, %v", dir, err)
	}
	// A symlinked session is listed as it is looked up.
	mkdir(t, filepath.Join(l.Home, "elsewhere", "session-3"))
	if err := os.Symlink(filepath.Join(l.Home, "elsewhere", "session-3"), filepath.Join(newRoot, "session-3")); err != nil {
		t.Fatal(err)
	}
	got, err = l.Sessions()
	ids := map[string]int{}
	for _, s := range got {
		ids[s.ID]++
	}
	if err != nil || ids["session-1"] != 1 || ids["session-3"] != 1 {
		t.Errorf("Sessions() = %v, %v; want session-1 once and the symlinked session-3", got, err)
	}
}

func TestScaffoldWritesWhereItReads(t *testing.T) {
	l := testLayout(t)
	repo := t.TempDir()
	if got, err := l.ScaffoldProjectFile(repo, ProjectConfig); err != nil || got != filepath.Join(repo, ".klaus", "config.json") {
		t.Errorf("ScaffoldProjectFile with none = %q, %v", got, err)
	}
	write(t, filepath.Join(repo, ".valley", "flow", "config.json"), "{}")
	if got, err := l.ScaffoldProjectFile(repo, ProjectConfig); err != nil || got != filepath.Join(repo, ".valley", "flow", "config.json") {
		t.Errorf("ScaffoldProjectFile with the flow file = %q, %v", got, err)
	}
	write(t, filepath.Join(l.Home, ".valley", "flow", "config.json"), "{}")
	if got, err := l.ScaffoldHostConfig(); err != nil || got != filepath.Join(l.Home, ".valley", "flow", "config.json") {
		t.Errorf("ScaffoldHostConfig = %q, %v", got, err)
	}
}

// New state needs only the generation: a legacy host config that can't be
// read or parsed sets none, as flow never read it to allocate, while one
// under the flow names that can't be is an error.
func TestGenerationReadsLegacyHostConfigAsBefore(t *testing.T) {
	l := testLayout(t)
	legacy, flow := filepath.Join(l.Home, ".klaus", "config.json"), filepath.Join(l.Home, ".valley", "flow", "config.json")
	write(t, legacy, "{")
	if g, err := l.Generation(); err != nil || g != Legacy {
		t.Errorf("Generation() with a malformed legacy host config = %q, %v; want legacy", g, err)
	}
	if _, err := l.NewSessionsRoot(); err != nil {
		t.Errorf("NewSessionsRoot() with a malformed legacy host config: %v", err)
	}
	write(t, legacy, `{"naming_generation": "w1"}`)
	if _, err := l.Generation(); err == nil {
		t.Error("Generation() accepted naming_generation w1")
	}
	mkdir(t, flow)
	if _, err := l.Generation(); err == nil {
		t.Error("Generation() with an unreadable host config under the flow names fell back")
	}
	if !FlowNamed(flow) || FlowNamed(legacy) {
		t.Errorf("FlowNamed: %s %v, %s %v", flow, FlowNamed(flow), legacy, FlowNamed(legacy))
	}
}
