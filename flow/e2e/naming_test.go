//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"the-valley/flow/internal/flowenv"
	"the-valley/flow/internal/run"
	"the-valley/flow/internal/selfupdate"
)

func writeE2EFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestOperatesStateUnderFlowNames: a session, a project registry, a host
// config, a repo config and self-update state that a later build put under
// the flow names (~/.valley/flow, <repo>/.valley/flow) are found and kept
// there by every command, whatever the naming generation, so a rollback to
// this build keeps working on them.
func TestOperatesStateUnderFlowNames(t *testing.T) {
	t.Parallel()
	h := NewHarness(t)
	h.UseFlowNames()
	flow := filepath.Join(h.Home, ".valley", "flow")
	legacy := filepath.Join(h.Home, ".klaus")

	writeE2EFile(t, filepath.Join(flow, "config.json"), `{"default_agent_model": "flow-host-model"}`+"\n")
	writeE2EFile(t, filepath.Join(flow, "projects.json"), `{"projects_dir": "~/src", "projects": {"widget": "`+h.RepoDir+`"}}`+"\n")
	writeE2EFile(t, filepath.Join(flow, "self-update.json"), `{"current": {"commit": "1111111", "store_path": "/nix/store/x-klaus"}, "updated_at": "2026-11-01T00:00:00Z"}`+"\n")
	if err := os.Mkdir(filepath.Join(h.RepoDir, ".valley"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.git(h.RepoDir, "mv", ".klaus", filepath.Join(".valley", "flow"))
	h.git(h.RepoDir, "commit", "-qm", "keep flow config under .valley/flow")
	h.git(h.RepoDir, "push", "-q", "origin", "main")

	run1 := func(args ...string) RunResult {
		t.Helper()
		res := h.RunKlaus(args...)
		if res.ExitCode != 0 {
			t.Fatalf("klaus %v exited %d\nstdout:\n%s\nstderr:\n%s", args, res.ExitCode, res.Stdout, res.Stderr)
		}
		return res
	}

	if res := run1("project", "list"); !strings.Contains(res.Stdout, "widget") {
		t.Errorf("project list read no registry under the flow names:\n%s", res.Stdout)
	}
	other := filepath.Join(h.E2EDir, "other")
	h.git("", "init", "-q", "-b", "main", other)
	h.RegisterProject("acme/other", other)
	if data, err := os.ReadFile(filepath.Join(flow, "projects.json")); err != nil || !strings.Contains(string(data), "other") {
		t.Errorf("project add did not write the registry back where it read it: %s, %v", data, err)
	}

	run1("launch", "work under the flow names")
	ids := h.RunIDs()
	if len(ids) != 1 {
		t.Fatalf("runs in the session under the flow names: %v", ids)
	}
	id := ids[0]
	h.WaitForClaudeStart(15 * time.Second)
	h.ReleaseClaude()
	st := h.WaitForState(id, func(s *run.State) bool { return s.CostUSD != nil }, 60*time.Second)
	if st.Model == nil || *st.Model != "flow-host-model" {
		t.Errorf("run model %v: the host config under the flow names was not read", st.Model)
	}
	if st.TmuxSession != h.AgentsSession() {
		t.Errorf("run recorded tmux session %q, want %q", st.TmuxSession, h.AgentsSession())
	}
	if st.LogFile == nil || !strings.HasPrefix(*st.LogFile, h.SessionDir()+string(filepath.Separator)) {
		t.Errorf("run log %v is outside its session %s", st.LogFile, h.SessionDir())
	}

	if res := run1("status"); !strings.Contains(res.Stdout, id) {
		t.Errorf("status does not list %s:\n%s", id, res.Stdout)
	}

	var report struct {
		Places []struct {
			Kind  string   `json:"kind"`
			Paths []string `json:"paths"`
		} `json:"places"`
		Blockers []struct{ What string } `json:"blockers"`
	}
	res := run1("naming", "status", "--json")
	if err := json.Unmarshal([]byte(res.Stdout), &report); err != nil {
		t.Fatalf("naming status --json: %v\n%s", err, res.Stdout)
	}
	places := map[string][]string{}
	for _, p := range report.Places {
		places[p.Kind] = p.Paths
	}
	for kind, want := range map[string]string{
		"host config":       filepath.Join(flow, "config.json"),
		"project registry":  filepath.Join(flow, "projects.json"),
		"self-update state": filepath.Join(flow, "self-update.json"),
		"sessions":          filepath.Join(flow, "sessions"),
	} {
		if got := places[kind]; len(got) != 1 || got[0] != want {
			t.Errorf("naming status puts %s at %v, want %s", kind, got, want)
		}
	}

	// Every self-update operation lists the profile first: one holding klaus.
	h.WriteStub("nix", `#!/usr/bin/env bash
case "$1 $2" in
"profile list") echo '{"elements":{"klaus":{"storePaths":["/nix/store/x-klaus"]}},"version":3}';;
*) echo "nix stub: unexpected $*" >&2; exit 2;;
esac
`)
	run1("self-update", "--pin")
	if su, err := selfupdate.LoadFile(filepath.Join(flow, "self-update.json")); err != nil || !su.Pinned || su.Current == nil {
		t.Errorf("self-update --pin did not write the state where it found it: %+v, %v", su, err)
	}

	run1("cleanup", id)
	if _, err := h.ReadState(id); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("cleanup left the run's state: %v", err)
	}

	// Nothing of these moved to, or was copied under, the legacy names.
	for _, p := range []string{
		filepath.Join(legacy, "sessions"),
		filepath.Join(legacy, "projects.json"),
		filepath.Join(legacy, "config.json"),
		filepath.Join(h.Home, ".local", "state", "klaus", "self-update.json"),
		filepath.Join(h.RepoDir, ".klaus"),
	} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists: state under the flow names was copied or moved", p)
		}
	}

	// A registry under both names stops launch, naming both, rather than
	// launching as if neither were there.
	writeE2EFile(t, filepath.Join(legacy, "projects.json"), `{"projects": {}}`+"\n")
	res = h.RunKlaus("launch", "with two registries")
	if res.ExitCode == 0 || !strings.Contains(res.Stderr, filepath.Join(legacy, "projects.json")) || !strings.Contains(res.Stderr, filepath.Join(flow, "projects.json")) {
		t.Errorf("launch with a registry under both names exited %d, want a refusal naming both\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
}

// TestBridgeGeneration: with naming_generation bridge, a new coordinator
// session goes under the flow root and every command operates it, while a
// session created earlier under legacy in the same home keeps its directory
// and its recorded generation as it is listed, finalized and resumed; the
// commit a worker makes carries Klaus-Run; and children get each flow
// variable under both names with one value. Setting legacy again moves
// nothing: the bridge session's run is still found, finalized and cleaned
// up, and a new session goes under the legacy root.
func TestBridgeGeneration(t *testing.T) {
	t.Parallel()
	h := NewHarness(t)
	legacyRoot := filepath.Join(h.Home, ".klaus")
	flowRoot := filepath.Join(h.Home, ".valley", "flow")
	setGeneration := func(g string) {
		writeE2EFile(t, filepath.Join(legacyRoot, "config.json"), `{"naming_generation": "`+g+`"}`+"\n")
	}
	in := func(id, root string) { h.SessionID, h.StateRoot = id, root }
	ok := func(res RunResult, what string) RunResult {
		t.Helper()
		if res.ExitCode != 0 {
			t.Fatalf("%s exited %d\nstdout:\n%s\nstderr:\n%s", what, res.ExitCode, res.Stdout, res.Stderr)
		}
		return res
	}
	waitFile := func(path string) {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(path); err == nil {
				return
			}
		}
		t.Fatalf("%s never appeared", path)
	}

	// Coordinators record their environment and exit. Workers record theirs
	// and wait until their own run is released.
	coordinatorEnv := filepath.Join(h.E2EDir, "coordinator.env")
	h.WriteStub("codex", fmt.Sprintf("#!/bin/sh\nenv > %q\n", coordinatorEnv))
	h.WriteStub("claude", fmt.Sprintf(`#!/usr/bin/env bash
dir=%q
run=${KLAUS_RUN_ID:-none}
env > "$dir/worker.$run.env"
[ -t 0 ] || cat > /dev/null
: > "$dir/started.$run"
for _ in {1..1800}; do
  [ -f "$dir/release.$run" ] && break
  sleep 0.1
done
cat "$dir/claude.output.jsonl"
`, h.E2EDir))
	release := func(id string) { writeE2EFile(t, filepath.Join(h.E2EDir, "release."+id), "") }
	readEnv := func(path string) map[string]string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		env := map[string]string{}
		for _, line := range strings.Split(string(data), "\n") {
			if k, v, found := strings.Cut(line, "="); found {
				env[k] = v
			}
		}
		return env
	}
	// both checks that a child got each flow variable under both names with
	// one value, or under neither, and the ones in want with their values.
	both := func(what string, env map[string]string, want map[flowenv.Var]string) {
		t.Helper()
		for _, v := range flowenv.All {
			n, nok := env[v.New]
			l, lok := env[v.Legacy]
			if nok != lok || n != l {
				t.Errorf("%s got %s=%q (%v) and %s=%q (%v)", what, v.New, n, nok, v.Legacy, l, lok)
			}
			if value, set := want[v]; set && (!nok || n != value) {
				t.Errorf("%s got %s=%q (%v), want %q", what, v.New, n, nok, value)
			}
		}
	}
	sessions := func() map[string]string {
		out := map[string]string{}
		for _, root := range []string{legacyRoot, flowRoot} {
			entries, _ := os.ReadDir(filepath.Join(root, "sessions"))
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "session-") {
					out[e.Name()] = filepath.Join(root, "sessions", e.Name())
				}
			}
		}
		return out
	}
	record := func(dir string) *run.State {
		t.Helper()
		st, err := run.NewHomeDirStoreFromPath(dir).Load(filepath.Base(dir))
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	newSession := func() (string, string) {
		t.Helper()
		before := sessions()
		ok(h.RunKlausIn(h.E2EDir, "new", "--backend", "codex"), "new")
		for id, dir := range sessions() {
			if _, seen := before[id]; !seen {
				return id, dir
			}
		}
		t.Fatalf("new made no session: %v", sessions())
		return "", ""
	}
	windowName := func() string {
		return strings.TrimSpace(h.tmux("display-message", "-p", "-t", h.InitialPane, "#{window_name}"))
	}
	finalized := func(s *run.State) bool { return s.CostUSD != nil && s.TmuxPane == nil }

	// Under legacy: a session under the legacy root, whose coordinator and
	// worker get the legacy names alone.
	legacyID, legacyDir := newSession()
	if legacyDir != filepath.Join(legacyRoot, "sessions", legacyID) || record(legacyDir).NamingGeneration != "legacy" {
		t.Fatalf("legacy session at %s records %q", legacyDir, record(legacyDir).NamingGeneration)
	}
	if env := readEnv(coordinatorEnv); env[flowenv.SessionID.Legacy] != legacyID || env[flowenv.SessionID.New] != "" {
		t.Errorf("legacy coordinator got %s=%q and %s=%q", flowenv.SessionID.Legacy, env[flowenv.SessionID.Legacy], flowenv.SessionID.New, env[flowenv.SessionID.New])
	}
	if got := windowName(); got != "klaus" {
		t.Errorf("legacy window title %q", got)
	}
	in(legacyID, legacyRoot)
	legacyRun := launchOne(t, h, "legacy work")
	waitFile(filepath.Join(h.E2EDir, "started."+legacyRun.ID))
	if env := readEnv(filepath.Join(h.E2EDir, "worker."+legacyRun.ID+".env")); env[flowenv.RunID.Legacy] != legacyRun.ID {
		t.Errorf("legacy worker got %s=%q", flowenv.RunID.Legacy, env[flowenv.RunID.Legacy])
	} else {
		for _, v := range flowenv.All {
			if _, set := env[v.New]; set {
				t.Errorf("legacy worker got %s", v.New)
			}
		}
	}

	// Under bridge: a new session under the flow root, recording bridge,
	// whose children get both names.
	setGeneration("bridge")
	bridgeID, bridgeDir := newSession()
	if bridgeDir != filepath.Join(flowRoot, "sessions", bridgeID) {
		t.Fatalf("bridge session at %s, want it under %s", bridgeDir, flowRoot)
	}
	if st := record(bridgeDir); st.NamingGeneration != "bridge" || st.CreatedBy == "" {
		t.Errorf("bridge session records generation %q, creator %q", st.NamingGeneration, st.CreatedBy)
	}
	both("bridge coordinator", readEnv(coordinatorEnv), map[flowenv.Var]string{flowenv.SessionID: bridgeID, flowenv.Backend: "codex", flowenv.AgentBackend: "claude"})
	if got := windowName(); got != "flow" {
		t.Errorf("bridge window title %q", got)
	}

	in(bridgeID, flowRoot)
	bridgeRun := launchOne(t, h, "bridge work")
	waitFile(filepath.Join(h.E2EDir, "started."+bridgeRun.ID))
	if bridgeRun.LogFile == nil || !strings.HasPrefix(*bridgeRun.LogFile, bridgeDir+string(filepath.Separator)) {
		t.Errorf("bridge run's log %v is outside its session %s", bridgeRun.LogFile, bridgeDir)
	}
	// The repo config sets worktree_base: it is kept.
	if !strings.HasPrefix(bridgeRun.Worktree, filepath.Join(filepath.Dir(h.RepoDir), "worktrees")+string(filepath.Separator)) {
		t.Errorf("bridge worktree %s is outside the configured worktree_base", bridgeRun.Worktree)
	}
	both("bridge worker", readEnv(filepath.Join(h.E2EDir, "worker."+bridgeRun.ID+".env")), map[flowenv.Var]string{
		flowenv.SessionID: bridgeID,
		flowenv.RunID:     bridgeRun.ID,
		flowenv.Backend:   "claude",
		flowenv.Scratch:   filepath.Join(bridgeDir, "scratch", bridgeRun.ID),
	})
	writeE2EFile(t, filepath.Join(bridgeRun.Worktree, "bridge.txt"), "bridge\n")
	h.git(bridgeRun.Worktree, "add", "-A")
	h.git(bridgeRun.Worktree, "commit", "-qm", "Add bridge work")
	if got, want := h.GitOutput(bridgeRun.Worktree, "log", "-1", "--format=%(trailers:only,unfold)"), "Change-Id: "+bridgeRun.ChangeID+"\nKlaus-Run: "+bridgeRun.ID; got != want {
		t.Errorf("bridge worker's commit trailers %q, want %q", got, want)
	}

	// Status, queue, watch, report and the dashboard operate the session.
	if res := ok(h.RunKlaus("status"), "status"); !strings.Contains(res.Stdout, bridgeRun.ID) {
		t.Errorf("status does not list %s:\n%s", bridgeRun.ID, res.Stdout)
	}
	ok(h.RunKlaus("queue"), "queue")
	h.emitMerged(7)
	if _, err := os.Stat(filepath.Join(bridgeDir, "events.jsonl")); err != nil {
		t.Errorf("the bridge session's events are not in its directory: %v", err)
	}
	if prs, _ := h.watchOnce("valley flow watch --once --after 0"); strings.Join(prs, " ") != "7" {
		t.Errorf("watch printed PRs %v, want [7]", prs)
	}
	ok(h.RunKlaus("report", "time", "--current", "--json"), "report time")
	dash := strings.TrimSpace(h.tmux("new-session", "-d", "-s", "dash", "-x", "120", "-y", "20", "-c", h.RepoDir,
		"-P", "-F", "#{pane_id}", "env "+flowenv.SessionID.New+"="+bridgeID+" klaus dashboard"))
	var screen string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline) && !strings.Contains(screen, "bridge work"); time.Sleep(200 * time.Millisecond) {
		screen = h.tmux("capture-pane", "-p", "-t", dash)
	}
	if !strings.Contains(screen, "bridge work") {
		t.Errorf("the dashboard never showed the bridge session's run:\n%s", screen)
	}
	h.tmux("kill-session", "-t", "dash")

	// The legacy session is listed, its run finalized in its directory,
	// and resumed there, keeping its recorded generation.
	in(legacyID, legacyRoot)
	if res := ok(h.RunKlaus("status"), "legacy status"); !strings.Contains(res.Stdout, legacyRun.ID) {
		t.Errorf("status does not list %s:\n%s", legacyRun.ID, res.Stdout)
	}
	release(legacyRun.ID)
	h.WaitForState(legacyRun.ID, finalized, 60*time.Second)
	if subjects := strings.Split(h.GitOutput(h.RepoDir, "log", "--reverse", "--format=%s", "refs/klaus/data"), "\n"); subjects[0] != "Initialize flow run data" {
		t.Errorf("the data ref starts with %q", subjects[0])
	}
	ok(h.RunKlausIn(h.E2EDir, "session", "--resume", legacyID), "resume legacy session")
	if _, err := os.Stat(filepath.Join(flowRoot, "sessions", legacyID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("resuming the legacy session under bridge made it under the flow root: %v", err)
	}
	if st := record(legacyDir); st.NamingGeneration != "legacy" {
		t.Errorf("resumed legacy session records %q", st.NamingGeneration)
	}
	both("resumed legacy coordinator", readEnv(coordinatorEnv), map[flowenv.Var]string{flowenv.SessionID: legacyID})
	ok(h.RunKlaus("cleanup", legacyRun.ID), "cleanup legacy run")

	// Back to legacy: the bridge session's run finalizes and is cleaned up
	// where it is, and new sessions go under the legacy root.
	setGeneration("legacy")
	in(bridgeID, flowRoot)
	release(bridgeRun.ID)
	h.WaitForState(bridgeRun.ID, finalized, 60*time.Second)
	if res := ok(h.RunKlaus("status"), "status after legacy"); !strings.Contains(res.Stdout, bridgeRun.ID) {
		t.Errorf("status under legacy does not list %s:\n%s", bridgeRun.ID, res.Stdout)
	}
	ok(h.RunKlausIn(h.E2EDir, "session", "--resume", bridgeID), "resume bridge session")
	if st := record(bridgeDir); st.NamingGeneration != "bridge" {
		t.Errorf("resumed bridge session records %q", st.NamingGeneration)
	}
	if env := readEnv(coordinatorEnv); env[flowenv.SessionID.Legacy] != bridgeID || env[flowenv.SessionID.New] != "" {
		t.Errorf("the bridge session's coordinator under legacy got %s=%q and %s=%q", flowenv.SessionID.Legacy, env[flowenv.SessionID.Legacy], flowenv.SessionID.New, env[flowenv.SessionID.New])
	}
	ok(h.RunKlaus("cleanup", bridgeRun.ID), "cleanup bridge run")
	if _, err := h.ReadState(bridgeRun.ID); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("cleanup left the bridge run's state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacyRoot, "sessions", bridgeID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the bridge session appeared under the legacy root: %v", err)
	}
	newID, newDir := newSession()
	if newDir != filepath.Join(legacyRoot, "sessions", newID) || record(newDir).NamingGeneration != "legacy" {
		t.Errorf("a new session under legacy is at %s, recording %q", newDir, record(newDir).NamingGeneration)
	}
}
