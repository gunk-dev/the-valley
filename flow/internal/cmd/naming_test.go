package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/flow/internal/git"
	"the-valley/flow/internal/location"
	"the-valley/flow/internal/run"
	"the-valley/flow/internal/selfupdate"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// naming status reads state under either name: sessions in both roots, the
// registry and self-update state under the flow names, and reports what
// keeps the host from advancing, each with its reason.
func TestNamingReport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	l := location.Layout{Home: home, StateHome: filepath.Join(home, ".local", "state"), Gen: location.Legacy}
	oldRoot, newRoot := l.SessionRoots()[0], l.SessionRoots()[1]

	live := map[string]bool{"%1": true, "%2": true}
	seed := func(dir string, st *run.State) {
		t.Helper()
		store := run.NewHomeDirStoreFromPath(dir)
		if err := store.Save(st); err != nil {
			t.Fatal(err)
		}
	}
	p1, p2, p3 := "%1", "%2", "%3"
	seed(filepath.Join(oldRoot, "session-a"), &run.State{ID: "session-a", Type: "session", CoordinatorPane: &p1})
	seed(filepath.Join(newRoot, "session-b"), &run.State{ID: "session-b", Type: "session", CoordinatorPane: &p2, CreatedBy: "1.0-feedbee", NamingGeneration: "legacy"})
	seed(filepath.Join(oldRoot, "session-c"), &run.State{ID: "session-c", Type: "session", CoordinatorPane: &p3})

	// A registered project whose one worktree has an older hook.
	clone := filepath.Join(home, "src", "app")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", clone},
		{"-C", clone, "-c", "user.name=t", "-c", "user.email=t@example.org", "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", clone, "worktree", "add", "-q", "-b", "run", filepath.Join(home, "wt")},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	wt := filepath.Join(home, "wt")
	if err := git.InstallHooks(context.Background(), wt, git.Provenance{}, nil); err != nil {
		t.Fatal(err)
	}
	gitDir, err := exec.Command("git", "-C", wt, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strings.TrimSpace(string(gitDir)), "hooks", "commit-msg"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(home, ".valley", "flow", "projects.json")
	// Saved registries hold paths under HOME as ~/...
	writeJSON(t, registry, map[string]any{"projects": map[string]string{"app": "~/src/app", "gone": filepath.Join(home, "gone")}})

	// The previous build is a program that leaves a mark if run: the
	// report must not run it.
	previous, ran := filepath.Join(home, "previous"), filepath.Join(home, "ran")
	if err := os.MkdirAll(filepath.Join(previous, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(previous, "bin", "klaus"), []byte("#!/bin/sh\n: > "+ran+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(home, ".valley", "flow", "self-update.json")
	writeJSON(t, statePath, selfupdate.State{
		Current:  &selfupdate.Install{Commit: "1111111111", StorePath: "/nix/store/new"},
		Previous: &selfupdate.Install{Commit: "2222222222", StorePath: previous},
	})
	// Snapshots a consult under bridge left, which a legacy host no longer
	// allocates to.
	snapshots := filepath.Join(home, ".valley", "flow", "consult-snapshots")
	for _, d := range []string{filepath.Join(home, ".klaus", "memory"), filepath.Join(home, ".valley", "flow", "memory"), snapshots} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	r := buildNamingReport(context.Background(), namingInputs{
		layout: l,
		tmux: run.TmuxDeps{
			PaneExists: func(p string) bool { return live[p] },
			PaneIsIdle: func(string) bool { return false },
			PaneIsDead: func(string) bool { return false },
		},
	})

	if r.Generation != location.Legacy || r.Next != location.Bridge || r.Allocates != location.Legacy {
		t.Errorf("generation %q, next %q, allocates %q", r.Generation, r.Next, r.Allocates)
	}
	places := map[string]namingPlace{}
	for _, p := range r.Places {
		places[p.Kind] = p
	}
	if p := places["project registry"]; len(p.Paths) != 1 || p.Paths[0] != registry || !p.Exists {
		t.Errorf("registry place %+v, want %s", p, registry)
	}
	if p := places["self-update state"]; len(p.Paths) != 1 || p.Paths[0] != statePath {
		t.Errorf("self-update place %+v, want %s", p, statePath)
	}
	if p := places["sessions"]; len(p.Paths) != 2 {
		t.Errorf("sessions place %+v, want both roots", p)
	}
	if p := places["shared memory"]; p.Error == "" || len(p.Paths) != 2 {
		t.Errorf("memory under both names reported %+v, want a conflict naming both", p)
	}
	if p := places["consult snapshots"]; len(p.Paths) != 1 || p.Paths[0] != snapshots || !p.Exists {
		t.Errorf("consult snapshots place %+v, want %s, where they are", p, snapshots)
	}

	if len(r.Sessions) != 2 || r.Sessions[0].ID != "session-a" || r.Sessions[0].CreatedBy != "" ||
		r.Sessions[1].ID != "session-b" || r.Sessions[1].CreatedBy != "1.0-feedbee" {
		t.Errorf("live sessions %+v, want session-a (unrecorded) and session-b", r.Sessions)
	}
	if len(r.Projects) != 2 || len(r.Projects[0].Hooks) != 1 || r.Projects[0].Hooks[0].Current || r.Projects[1].Error == "" {
		t.Errorf("projects %+v, want app with one older hook and gone unreadable", r.Projects)
	}
	if r.SelfUpdate.Current == nil || r.SelfUpdate.Previous == nil || r.SelfUpdate.Previous.StorePath != previous {
		t.Errorf("self-update %+v", r.SelfUpdate)
	}
	if _, err := os.Stat(ran); !errors.Is(err, os.ErrNotExist) {
		t.Error("naming status ran the previous build")
	}

	blocked := map[string]bool{}
	for _, b := range r.Blockers {
		if b.Reason == "" {
			t.Errorf("blocker %q has no reason", b.What)
		}
		blocked[b.What] = true
	}
	for _, want := range []string{"this build", "shared memory", "session session-a", "project app", "project gone", "self-update's previous build 2222222"} {
		if !blocked[want] {
			t.Errorf("no blocker for %s in %+v", want, r.Blockers)
		}
	}
	if blocked["session session-b"] || blocked["session session-c"] {
		t.Errorf("a recorded or finished session blocks: %+v", r.Blockers)
	}

	var out bytes.Buffer
	printNamingReport(&out, r, home)
	for _, want := range []string{"~/.valley/flow/projects.json", "session-a", "1.0-feedbee", r.Unseen} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}

	// It changed nothing: no session, registry or lock appeared.
	for _, p := range []string{filepath.Join(home, ".klaus", "projects.json"), filepath.Join(home, ".klaus", "locks"), filepath.Join(newRoot, "session-a")} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("naming status made %s", p)
		}
	}
}

// State that can't be read blocks: a registry is not taken for one with
// no projects, a session for a finished one, whether its runs directory, a
// record or a record's contents can't be read, nor self-update state for
// one without a rollback target.
func TestNamingReportUnreadableState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for path, data := range map[string]string{
		filepath.Join(home, ".klaus", "projects.json"):                 "{",
		filepath.Join(home, "state", "klaus", "self-update.json"):      "{",
		filepath.Join(home, ".klaus", "sessions", "session-x", "runs"): "a file, not a directory",
		filepath.Join(home, ".klaus", "sessions", "session-y", "runs", "run-1.json"): "{",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A record no one can read, root included: a symlink to a directory.
	runs := filepath.Join(home, ".klaus", "sessions", "session-z", "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".", filepath.Join(runs, "run-1.json")); err != nil {
		t.Fatal(err)
	}
	r := buildNamingReport(context.Background(), namingInputs{
		layout: location.Layout{Home: home, StateHome: filepath.Join(home, "state"), Gen: location.Legacy},
		tmux:   run.TmuxDeps{PaneExists: func(string) bool { return false }, PaneIsIdle: func(string) bool { return true }, PaneIsDead: func(string) bool { return true }},
	})
	blocked := map[string]bool{}
	for _, b := range r.Blockers {
		blocked[b.What] = true
	}
	for _, want := range []string{"project registry", "self-update state", "session session-x", "session session-y", "session session-z"} {
		if !blocked[want] {
			t.Errorf("no blocker for an unreadable %s: %+v", want, r.Blockers)
		}
	}
}

// Under each generation the report says which names new state gets in
// this build, and that this build keeps the host from flow: legacy gives
// legacy names, bridge and flow bridge names, and only the state checks
// block bridge.
func TestNamingReportPerGeneration(t *testing.T) {
	for _, tc := range []struct {
		gen       location.Generation
		allocates location.Generation
		glyph     string // the verdict's: the head line's target is blocked or not
		target    location.Generation
		sessions  string // where new sessions go, under home
	}{
		{location.Legacy, location.Legacy, "✓", location.Bridge, ".klaus/sessions"},
		{location.Bridge, location.Bridge, "◇", location.Flow, ".valley/flow/sessions"},
		{location.Flow, location.Bridge, "◇", location.Flow, ".valley/flow/sessions"},
	} {
		t.Run(string(tc.gen), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			r := buildNamingReport(context.Background(), namingInputs{
				layout: location.Layout{Home: home, StateHome: filepath.Join(home, "state"), Gen: tc.gen},
				tmux:   run.TmuxDeps{PaneExists: func(string) bool { return false }, PaneIsIdle: func(string) bool { return true }, PaneIsDead: func(string) bool { return true }},
			})
			if r.Allocates != tc.allocates {
				t.Errorf("allocates %q, want %q", r.Allocates, tc.allocates)
			}
			if len(r.Blockers) != 1 || r.Blockers[0].What != "this build" || r.Blockers[0].Blocks != location.Flow {
				t.Errorf("blockers %+v, want this build alone, blocking flow", r.Blockers)
			}
			for _, p := range r.Places {
				if p.Kind == "sessions" && (len(p.Paths) != 1 || p.Paths[0] != filepath.Join(home, tc.sessions)) {
					t.Errorf("new sessions go to %v, want %s", p.Paths, filepath.Join(home, tc.sessions))
				}
			}
			if got := namingTarget(r); got != tc.target {
				t.Errorf("the report's target is %q, want %q", got, tc.target)
			}
			var out bytes.Buffer
			printNamingReport(&out, r, home)
			if first, _, _ := strings.Cut(out.String(), "\n"); !strings.HasPrefix(first, tc.glyph) ||
				!strings.Contains(first, string(tc.allocates)) || !strings.Contains(first, string(tc.target)) {
				t.Errorf("head line %q gives no %s verdict on %s with %s names", first, tc.glyph, tc.target, tc.allocates)
			}
			if !strings.Contains(out.String(), "this build") {
				t.Errorf("report does not list this build's blocker:\n%s", out.String())
			}
		})
	}
}

// An unknown session is an error and no store: a caller that checks the
// store, not the error, must find nothing to use.
func TestSessionStoreUnknownIsNil(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(sessionIDEnv, "session-unknown")
	store, err := sessionStore()
	if err == nil || !errors.Is(err, location.ErrUnknownSession) {
		t.Errorf("sessionStore() error = %v, want ErrUnknownSession", err)
	}
	if store != nil {
		t.Errorf("sessionStore() = %#v, want a nil interface", store)
	}
}
