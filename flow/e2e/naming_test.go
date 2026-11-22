//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
