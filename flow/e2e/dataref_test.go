//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"the-valley/flow/internal/run"
)

const dataRef = "refs/klaus/data"

// refSHA returns the sha ref resolves to in the git dir, or "" if absent.
func (h *Harness) refSHA(gitDir, ref string) string {
	h.t.Helper()
	cmd := exec.Command("git", "--git-dir", gitDir, "rev-parse", "--verify", "--quiet", ref)
	cmd.Env = append(os.Environ(), "HOME="+h.Home, "GIT_CONFIG_GLOBAL="+filepath.Join(h.Home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// launchAndFinalize launches an agent, lets the fake claude finish, and waits
// for _finalize to complete (cost recorded, pane released). It returns the
// run id.
func (h *Harness) launchAndFinalize(prompt string) string {
	h.t.Helper()
	res := h.RunKlaus("launch", prompt)
	if res.ExitCode != 0 {
		h.t.Fatalf("launch exited %d\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	ids := h.RunIDs()
	if len(ids) != 1 {
		h.t.Fatalf("expected 1 run, got %d: %v", len(ids), ids)
	}
	h.WaitForClaudeStart(30 * time.Second)
	h.ReleaseClaude()
	h.WaitForState(ids[0], func(s *run.State) bool {
		return s.CostUSD != nil && s.TmuxPane == nil
	}, 30*time.Second)
	return ids[0]
}

// TestDataRefStaysLocalByDefault: with the default config, finalize commits
// the run to the sandbox repo's data ref and origin never receives it.
// push-log publishes it only when given --push.
func TestDataRefStaysLocalByDefault(t *testing.T) {
	t.Parallel()
	h := NewHarness(t)
	localGitDir := filepath.Join(h.RepoDir, ".git")

	runID := h.launchAndFinalize("add a health check endpoint")

	if h.refSHA(localGitDir, dataRef+":runs/"+runID+".json") == "" {
		t.Fatalf("local %s has no runs/%s.json after finalize", dataRef, runID)
	}
	if sha := h.refSHA(h.OriginDir, dataRef); sha != "" {
		t.Fatalf("origin has %s at %s after finalize; the default config must not push it", dataRef, sha)
	}

	res := h.RunKlaus("push-log", runID)
	if res.ExitCode != 0 {
		t.Fatalf("push-log exited %d\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "Not pushed") {
		t.Errorf("push-log should say it did not push; stdout:\n%s", res.Stdout)
	}
	if sha := h.refSHA(h.OriginDir, dataRef); sha != "" {
		t.Fatalf("push-log without --push published %s", dataRef)
	}

	res = h.RunKlaus("push-log", "--push", runID)
	if res.ExitCode != 0 {
		t.Fatalf("push-log --push exited %d\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "Pushed "+dataRef+" to origin") {
		t.Errorf("push-log --push should report the push; stdout:\n%s", res.Stdout)
	}
	local := h.refSHA(localGitDir, dataRef)
	if got := h.refSHA(h.OriginDir, dataRef); got == "" || got != local {
		t.Errorf("origin %s = %q after push-log --push, want %s", dataRef, got, local)
	}
}

// TestDataRefPushedWhenEnabled: push_data_ref: true makes finalize push the
// data ref to origin. It is set globally because a host-repo run's finalize
// reads the repo config from the agent's worktree, which holds the committed
// copy.
func TestDataRefPushedWhenEnabled(t *testing.T) {
	t.Parallel()
	h := NewHarness(t)
	if err := os.MkdirAll(filepath.Join(h.Home, ".klaus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Home, ".klaus", "config.json"), []byte(`{"push_data_ref": true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runID := h.launchAndFinalize("add a health check endpoint")

	local := h.refSHA(filepath.Join(h.RepoDir, ".git"), dataRef)
	if local == "" {
		t.Fatalf("local %s missing after finalize of %s", dataRef, runID)
	}
	if got := h.refSHA(h.OriginDir, dataRef); got != local {
		t.Errorf("origin %s = %q, want the local commit %s", dataRef, got, local)
	}
}

// TestDataRefPushRefusedAtFinalize: when origin's receive hook refuses the
// data ref, the run finalizes with its own outcome and records the refusal,
// status shows it, and the conversation replays from the clone. Once the hook
// is gone, the next run's finalize in the same clone publishes the ref, and
// with it the first run's commit.
func TestDataRefPushRefusedAtFinalize(t *testing.T) {
	t.Parallel()
	h := NewHarness(t)
	if err := os.MkdirAll(filepath.Join(h.Home, ".klaus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Home, ".klaus", "config.json"), []byte(`{"push_data_ref": true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const hookReason = "policy denies refs/klaus/data"
	hookPath := filepath.Join(h.OriginDir, "hooks", "pre-receive")
	hook := "#!/bin/sh\nwhile read old new name; do\n  if [ \"$name\" = \"" + dataRef + "\" ]; then\n    echo \"" + hookReason + "\" >&2\n    exit 1\n  fi\ndone\n"
	if err := os.WriteFile(hookPath, []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	h.recordConversation()
	localGitDir := filepath.Join(h.RepoDir, ".git")

	first, _ := h.launchRun("add a health check endpoint")
	h.WaitForClaudeStart(30 * time.Second)
	h.pushWork(first, "health.go", "Add a health check")
	first = h.finishRun(first.ID)

	if first.FailureReason != nil || first.NeedsAttention != nil || first.PushedSHA == nil {
		t.Fatalf("run outcome changed by the refused push: failure=%v attention=%v pushed=%v", first.FailureReason, first.NeedsAttention, first.PushedSHA)
	}
	rec := first.DataRefPush
	if rec == nil || rec.Status != run.DataRefFailed || rec.Class != "refused" || !strings.Contains(rec.Message, hookReason) || rec.Commit != h.refSHA(localGitDir, dataRef) {
		t.Fatalf("DataRefPush = %+v, want refused at the local tip with %q", rec, hookReason)
	}
	if sha := h.refSHA(h.OriginDir, dataRef); sha != "" {
		t.Fatalf("origin has %s at %s though its hook refused it", dataRef, sha)
	}

	st := h.RunKlaus("status")
	for _, want := range []string{"awaiting-integration", "failed at finalize", first.ID + " publishing " + dataRef, hookReason, "git push origin " + dataRef} {
		if !strings.Contains(st.Stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, st.Stdout)
		}
	}
	if watch := h.RunKlaus("watch", "--since-start", "--once"); !strings.Contains(watch.Stdout, "agent:data-ref-push-failed") || !strings.Contains(watch.Stdout, hookReason) {
		t.Errorf("the default watch stream lacks the failure:\n%s", watch.Stdout)
	}

	if err := os.Remove(hookPath); err != nil {
		t.Fatal(err)
	}
	h.resetWorker()
	follow, res := h.launchRun("--change", first.ChangeID, "follow up on the health check")
	if !strings.Contains(res.Stdout, "resuming conversation from run "+first.ID) {
		t.Errorf("the follow-up did not replay the conversation from the clone:\n%s", res.Stdout)
	}
	h.WaitForClaudeStart(30 * time.Second)
	h.pushWork(follow, "health_test.go", "Test the health check")
	follow = h.finishRun(follow.ID)

	if follow.DataRefPush == nil || follow.DataRefPush.Status != run.DataRefPublished {
		t.Fatalf("follow-up DataRefPush = %+v, want published", follow.DataRefPush)
	}
	if h.refSHA(h.OriginDir, rec.Commit+"^{commit}") == "" || h.refSHA(h.OriginDir, dataRef+":sessions/"+first.ID+".jsonl") == "" {
		t.Errorf("origin lacks the first run's commit %s after the follow-up published", rec.Commit)
	}
	if again, err := h.ReadState(first.ID); err != nil {
		t.Errorf("reading the first run: %v", err)
	} else if again.DataRefPush == nil || *again.DataRefPush != *rec {
		t.Errorf("the first run's record changed after finalize: %+v", again.DataRefPush)
	}
}
