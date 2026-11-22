package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/flow/internal/event"
	"the-valley/flow/internal/git"
	"the-valley/flow/internal/run"
)

const testDataRef = "refs/klaus/data"

// finalizePRLog is a clean run that opened a PR, so _finalize takes the
// normal path (no salvage) through the data-ref sync. Its runs record PR
// delivery, as launch does for a repo on GitHub, since the bare origin's path
// isn't a GitHub URL.
const finalizePRLog = `{"type":"assistant","message":{"content":[{"type":"text","text":"Opened https://github.com/acme/widget/pull/9"}]}}
{"type":"result","subtype":"success","total_cost_usd":1,"duration_ms":1000}
`

// TestFinalizeKeepsDataRefLocalByDefault runs the real _finalize against a
// clone with a bare origin: the run lands on the local data ref, and origin
// never receives it.
func TestFinalizeKeepsDataRefLocalByDefault(t *testing.T) {
	origin, repo, worktree, branch := setupBareRemote(t)
	commitAndPush(t, worktree, branch)

	_, state, _ := finalizeRealRepo(t, repo, worktree, branch, finalizePRLog, run.DeliveryPR)

	files, err := gitOut(t, repo, "ls-tree", "-r", "--name-only", testDataRef)
	if err != nil {
		t.Fatalf("local %s missing after finalize: %v %s", testDataRef, err, files)
	}
	for _, want := range []string{"runs/" + state.ID + ".json", "logs/" + state.ID + ".jsonl"} {
		if !strings.Contains(files, want) {
			t.Errorf("local %s missing %s; has:\n%s", testDataRef, want, files)
		}
	}
	if sha, err := gitOut(t, origin, "rev-parse", "--verify", "--quiet", testDataRef); err == nil {
		t.Fatalf("origin has %s at %s; the default config must not push it", testDataRef, sha)
	}
	local, _ := gitOut(t, repo, "rev-parse", testDataRef)
	if want := (run.DataRefPush{Ref: testDataRef, Commit: local, Status: run.DataRefNotAttempted}); state.DataRefPush == nil || *state.DataRefPush != want {
		t.Errorf("DataRefPush = %+v, want %+v", state.DataRefPush, want)
	}
}

// TestFinalizePushesDataRefWhenEnabled: push_data_ref: true restores the push.
func TestFinalizePushesDataRefWhenEnabled(t *testing.T) {
	origin, repo, worktree, branch := setupBareRemote(t)
	commitAndPush(t, worktree, branch)
	if err := os.MkdirAll(filepath.Join(repo, ".klaus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".klaus", "config.json"), []byte(`{"push_data_ref": true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, state, _ := finalizeRealRepo(t, repo, worktree, branch, finalizePRLog, run.DeliveryPR)

	local, err := gitOut(t, repo, "rev-parse", testDataRef)
	if err != nil {
		t.Fatalf("local %s missing after finalize: %v", testDataRef, err)
	}
	if remote, err := gitOut(t, origin, "rev-parse", "--verify", testDataRef); err != nil || remote != local {
		t.Fatalf("origin %s = %q (%v), want the local commit %s", testDataRef, remote, err, local)
	}
	if want := (run.DataRefPush{Ref: testDataRef, Commit: local, Status: run.DataRefPublished}); state.DataRefPush == nil || *state.DataRefPush != want {
		t.Errorf("DataRefPush = %+v, want %+v", state.DataRefPush, want)
	}
}

// TestFinalizeRecordsRefusedDataRefPush: a receive hook that refuses the data
// ref leaves the run's outcome and local commit as they were, and the run
// records the refusal in the hook's words.
func TestFinalizeRecordsRefusedDataRefPush(t *testing.T) {
	origin, repo, worktree, branch := setupBareRemote(t)
	commitAndPush(t, worktree, branch)
	if err := os.MkdirAll(filepath.Join(repo, ".klaus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".klaus", "config.json"), []byte(`{"push_data_ref": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	refuseDataRef(t, origin, "echo 'policy denies refs/klaus/data' >&2")

	store, state, _ := finalizeRealRepo(t, repo, worktree, branch, finalizePRLog, run.DeliveryPR)

	if state.PRURL == nil || state.FailureReason != nil || state.NeedsAttention != nil {
		t.Errorf("run outcome changed: pr=%v failure=%v attention=%v", state.PRURL, state.FailureReason, state.NeedsAttention)
	}
	local, _ := gitOut(t, repo, "rev-parse", testDataRef)
	rec := state.DataRefPush
	if rec == nil || rec.Status != run.DataRefFailed || rec.Commit != local || rec.Class != git.PushRefused ||
		!strings.Contains(rec.Message, "remote: policy denies refs/klaus/data") {
		t.Fatalf("DataRefPush = %+v, want refused at %s with the hook's line", rec, local)
	}
	evts := runEvents(t, store.BaseDir(), state.ID)
	if e := evts[event.AgentDataRefPushFailed]; e == nil || e["class"] != git.PushRefused || e["commit"] != local {
		t.Errorf("agent:data-ref-push-failed = %v", e)
	}
	if _, ok := evts[event.AgentNeedsAttention]; ok {
		t.Error("a refused data ref push emitted agent:needs-attention")
	}
}

// refuseDataRef installs a pre-receive hook on origin that runs say and
// refuses the push.
func refuseDataRef(t *testing.T, origin, say string) {
	t.Helper()
	hook := "#!/bin/sh\n" + say + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(origin, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
}

// secretPushClient fails every data ref push as a remote does that echoes
// its address, credentials and all, in the ref's status, while git's own
// lines carry destination and a remote helper's command line.
type secretPushClient struct {
	*git.ExecClient
	destination string
}

func (c secretPushClient) PushDataRef(_ context.Context, _, dataRef string) (string, error) {
	return "", git.ParsePushError(dataRef, "To "+c.destination+"\n"+
		"!\tHEAD:refs/klaus/data\t[remote rejected] (permission denied for https://user:secret@example.org/x?token=secret)\n"+
		"Done\n",
		"fatal: unable to access 'https://user:secret@example.org/x?token=secret': retrying\n"+
			"error: helper ext::ssh -i /srv/secret-key example.org %S repo exited\n"+
			"remote: policy denies refs/klaus/data\n"+
			"error: failed to push some refs to 'ext::ssh -i /srv/secret-key example.org %S repo'\n")
}

// TestDataRefPushFailureKeepsAddressesOut: of a failed push's output, only
// the remote's lines and the ref's status reach the run record, the event,
// finalize's stderr, status, watch, notifications and the dashboard, and
// those that hold an address are withheld whole.
func TestDataRefPushFailureKeepsAddressesOut(t *testing.T) {
	for _, tc := range []struct {
		name    string
		client  git.Client
		origin  string // origin's URL, when the push is real
		message string // the stored message
		kept    []string
	}{
		{"an address in the remote's reason", secretPushClient{git.NewExecClient(), "https://user:secret@example.org/x?token=secret"}, "",
			"remote: policy denies refs/klaus/data\n[remote rejected] " + git.PushWithheld,
			[]string{"policy denies refs/klaus/data", "[remote rejected]"}},
		{"a destination that forges a status line", secretPushClient{git.NewExecClient(), "\n!\tx:y\t[https://example.org/x?token=secret]\n"}, "",
			"remote: policy denies refs/klaus/data\n[remote rejected] " + git.PushWithheld,
			[]string{"policy denies refs/klaus/data", "[remote rejected]"}},
		{"a destination path made to look like a remote line", git.NewExecClient(),
			"/srv/x\nremote: see user:secret@example.org/x?token=secret", git.PushWithheld, []string{git.PushWithheld}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			_, repo, _, _ := setupBareRemote(t)
			if tc.origin != "" {
				runGitCmd(t, repo, "remote", "set-url", "origin", tc.origin)
			}
			store := run.NewHomeDirStoreFromPath(filepath.Join(t.TempDir(), "session"))
			if err := store.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			st := finishedClaudeRun(t, store, home, "20261004-1200-aaaa", "6623bdcf-1dce-4da0-86b3-a46743d208e8")

			stderr := captureStderr(t, func() {
				syncRunToDataRef(context.Background(), repo, store, tc.client, testDataRef, true, st)
			})
			if rec := st.DataRefPush; rec == nil || rec.Status != run.DataRefFailed || rec.Message != tc.message {
				t.Errorf("DataRefPush = %+v, want a failure with message %q", rec, tc.message)
			}
			record, err := os.ReadFile(filepath.Join(store.StateDir(), st.ID+".json"))
			if err != nil {
				t.Fatal(err)
			}
			events, err := os.ReadFile(filepath.Join(store.BaseDir(), "events.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			states, _ := store.List()
			var status bytes.Buffer
			if _, err := runStatuses(context.Background(), store, states, &status, nil); err != nil {
				t.Fatal(err)
			}
			evts, _ := event.NewLog(store.BaseDir()).Read()
			var watch strings.Builder
			for _, e := range evts {
				watch.WriteString(formatEvent(e) + "\n")
			}
			notifications := captureStdout(t, func() { printSummary(evts, nil) })
			dashboard := dashboardModel{states: states}.dataRefWarning()

			for name, out := range map[string]string{"record": string(record), "events": string(events), "stderr": stderr,
				"status": status.String(), "watch": watch.String(), "notifications": notifications, "dashboard": dashboard} {
				for _, want := range tc.kept {
					if !strings.Contains(out, want) {
						t.Errorf("%s lacks %q:\n%s", name, want, out)
					}
				}
				for _, leak := range []string{"secret", "token", "user:", "ext::", "https://", "example.org"} {
					if strings.Contains(out, leak) {
						t.Errorf("%s holds %q:\n%s", name, leak, out)
					}
				}
			}
			if !strings.Contains(stderr, "git push origin "+testDataRef) || !strings.Contains(status.String(), "git push origin "+testDataRef) {
				t.Errorf("stderr and status should say how to publish by hand:\n%s\n%s", stderr, status.String())
			}
		})
	}
}

// TestInterleavedFinalizesPublishBothRuns: two finalizes in one clone build
// their commits on the same tip. The first moves the ref while the second is
// about to; the second then moves and publishes it before the first pushes.
// Both commits stay on the ref's history, origin ends up with both, and each
// run records published.
func TestInterleavedFinalizesPublishBothRuns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	origin, repo, _, _ := setupBareRemote(t)
	store := run.NewHomeDirStoreFromPath(filepath.Join(t.TempDir(), "session"))
	if err := store.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	gc := git.NewExecClient()
	if err := gc.EnsureDataRef(ctx, repo, testDataRef); err != nil {
		t.Fatal(err)
	}
	first := finishedClaudeRun(t, store, home, "20261004-1600-aaaa", "6623bdcf-1dce-4da0-86b3-a46743d208e8")
	second := finishedClaudeRun(t, store, home, "20261004-1600-bbbb", "11111111-2222-4333-8444-555555555555")

	var firstCommit string
	fired := false
	t.Cleanup(git.SetBeforeDataRefUpdate(func() {
		if fired {
			return
		}
		fired = true
		var err error
		firstCommit, err = gc.SyncToDataRef(ctx, repo, testDataRef, "Run "+first.ID,
			map[string]string{"runs/" + first.ID + ".json": filepath.Join(store.StateDir(), first.ID+".json")})
		if err != nil {
			t.Fatalf("first run's sync: %v", err)
		}
	}))
	syncRunToDataRef(ctx, repo, store, gc, testDataRef, true, second)
	recordDataRefPush(ctx, store, gc, repo, testDataRef, firstCommit, true, first)

	tip, err := gitOut(t, origin, "rev-parse", "--verify", testDataRef)
	if err != nil {
		t.Fatalf("origin lacks %s: %v", testDataRef, err)
	}
	for _, st := range []*run.State{first, second} {
		rec := st.DataRefPush
		if rec == nil || rec.Status != run.DataRefPublished || rec.Commit == "" {
			t.Fatalf("%s DataRefPush = %+v, want published", st.ID, rec)
		}
		if _, err := gitOut(t, origin, "merge-base", "--is-ancestor", rec.Commit, tip); err != nil {
			t.Errorf("%s recorded published, but origin's %s at %s lacks its commit %s", st.ID, testDataRef, tip, rec.Commit)
		}
	}
	if first.DataRefPush.Commit == second.DataRefPush.Commit {
		t.Errorf("both runs recorded commit %s", first.DataRefPush.Commit)
	}
}

// historyRewriter rewinds the data ref past the run's commit before each
// push, as a writer that ignores the ref's old value can, so the pushed
// history lacks that commit.
type historyRewriter struct{ *git.ExecClient }

func (c historyRewriter) PushDataRef(ctx context.Context, repoDir, dataRef string) (string, error) {
	if out, err := exec.Command("git", "-C", repoDir, "update-ref", dataRef, dataRef+"^").CombinedOutput(); err != nil {
		return "", fmt.Errorf("rewinding %s: %v: %s", dataRef, err, out)
	}
	return c.ExecClient.PushDataRef(ctx, repoDir, dataRef)
}

// TestFinalizeRecordsCommitMissingFromPushedHistory: a push that succeeds
// with a history lacking the run's commit records a failure, not a
// publication.
func TestFinalizeRecordsCommitMissingFromPushedHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	_, repo, _, _ := setupBareRemote(t)
	store := run.NewHomeDirStoreFromPath(filepath.Join(t.TempDir(), "session"))
	if err := store.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st := finishedClaudeRun(t, store, home, "20261004-1700-aaaa", "6623bdcf-1dce-4da0-86b3-a46743d208e8")

	stderr := captureStderr(t, func() {
		syncRunToDataRef(context.Background(), repo, store, historyRewriter{git.NewExecClient()}, testDataRef, true, st)
	})
	if rec := st.DataRefPush; rec == nil || rec.Status != run.DataRefFailed || rec.Class != git.PushOther || rec.Message != dataRefNotInHistory {
		t.Fatalf("DataRefPush = %+v, want a failure saying the commit is not in the pushed history", rec)
	}
	if !strings.Contains(stderr, dataRefNotInHistory) {
		t.Errorf("finalize's warning lacks the failure:\n%s", stderr)
	}
}

// TestOversizedRefusalKeepsNotificationsReadable: a hook that prints more
// than the event log reader's 1 MiB line limit leaves a bounded message, and
// the event log still reads.
func TestOversizedRefusalKeepsNotificationsReadable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	origin, repo, _, _ := setupBareRemote(t)
	refuseDataRef(t, origin, `line=$(printf '%01024d' 0); i=0; while [ $i -lt 1300 ]; do echo "$line" >&2; i=$((i+1)); done`)
	store := run.NewHomeDirStoreFromPath(filepath.Join(t.TempDir(), "session"))
	if err := store.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st := finishedClaudeRun(t, store, home, "20261004-1500-aaaa", "6623bdcf-1dce-4da0-86b3-a46743d208e8")
	captureStderr(t, func() {
		syncRunToDataRef(context.Background(), repo, store, git.NewExecClient(), testDataRef, true, st)
	})

	if rec := st.DataRefPush; rec == nil || len(rec.Message) > git.MaxPushMessage || !strings.HasSuffix(rec.Message, git.PushMessageCut) {
		t.Fatalf("DataRefPush message is not bounded and marked: %+v", rec)
	}
	emitEvent(store.BaseDir(), st.ID, event.AgentCompleted, nil)
	evts, err := event.NewLog(store.BaseDir()).Read()
	if err != nil || len(evts) < 2 || evts[len(evts)-1].Type != event.AgentCompleted {
		t.Fatalf("event log after the refusal = %d events, %v; want it to read through the next event", len(evts), err)
	}
	if out := captureStdout(t, func() { printSummary(evts, nil) }); !strings.Contains(out, "1 data ref push(es) failed at finalize") {
		t.Errorf("notifications summary lacks the failure:\n%s", out)
	}
}

// captureStderr returns what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = f
	defer func() { os.Stderr = old }()
	fn()
	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// finishedClaudeRun saves a finished run whose log and resumable conversation
// exist on disk under home, as _finalize sees one.
func finishedClaudeRun(t *testing.T, store run.StateStore, home, id, sessionID string) *run.State {
	t.Helper()
	worktree := filepath.Join(home, "worktrees", id)
	conv := claudeConversationPath(worktree, sessionID)
	if err := os.MkdirAll(filepath.Dir(conv), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conv, []byte(makeConversationJSONL(sessionID, worktree)), 0o600); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(store.LogDir(), id+".jsonl")
	if err := os.WriteFile(logFile, []byte(`{"type":"result","subtype":"success"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sid := sessionID
	st := &run.State{
		ID:              id,
		Branch:          "feature-x",
		Worktree:        worktree,
		LogFile:         &logFile,
		CreatedAt:       "2026-10-04T" + id[9:11] + ":" + id[11:13] + ":00Z", // id is YYYYMMDD-HHMM-xxxx
		ClaudeSessionID: &sid,
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return st
}

// TestReplayReadsUnpushedDataRef: trajectory replay needs nothing on origin.
// It reads the local data ref finalize committed to, and its best-effort
// fetch neither fails the replay nor rewinds a local ref that is ahead of a
// copy published before push_data_ref existed.
func TestReplayReadsUnpushedDataRef(t *testing.T) {
	setup := func(t *testing.T) (home, origin, repo string, store run.StateStore) {
		home = t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		origin, repo, _, _ = setupBareRemote(t)
		s := run.NewHomeDirStoreFromPath(filepath.Join(t.TempDir(), "session"))
		if err := s.EnsureDirs(); err != nil {
			t.Fatalf("EnsureDirs: %v", err)
		}
		return home, origin, repo, s
	}
	replay := func(store run.StateStore, repo, home string) replayDecision {
		return resolveReplay(context.Background(), replayParams{
			GitClient:   git.NewExecClient(),
			Store:       store,
			RepoRoot:    repo,
			DataRef:     testDataRef,
			Worktree:    filepath.Join(home, "worktrees", "resumed"),
			PRBranch:    "feature-x",
			PRNumber:    "7",
			ForceReplay: true, // skip the gh paused-label check
			ThresholdKB: 300,
		})
	}
	ctx := context.Background()

	t.Run("origin never had the ref", func(t *testing.T) {
		home, origin, repo, store := setup(t)
		const sessionID = "6623bdcf-1dce-4da0-86b3-a46743d208e8"
		st := finishedClaudeRun(t, store, home, "20261004-1000-aaaa", sessionID)
		syncRunToDataRef(ctx, repo, store, git.NewExecClient(), testDataRef, false, st)

		if d := replay(store, repo, home); d.SessionUUID != sessionID {
			t.Fatalf("SessionUUID = %q, want %q (%s)", d.SessionUUID, sessionID, d.Reason)
		}
		if _, err := gitOut(t, origin, "rev-parse", "--verify", "--quiet", testDataRef); err == nil {
			t.Errorf("origin gained %s", testDataRef)
		}
	})

	t.Run("origin holds a stale published ref", func(t *testing.T) {
		home, origin, repo, store := setup(t)
		gc := git.NewExecClient()
		old := finishedClaudeRun(t, store, home, "20261004-0900-aaaa", "11111111-2222-4333-8444-555555555555")
		syncRunToDataRef(ctx, repo, store, gc, testDataRef, true, old)
		published, err := gitOut(t, origin, "rev-parse", "--verify", testDataRef)
		if err != nil {
			t.Fatalf("setup: first run not published: %v", err)
		}

		const sessionID = "99999999-8888-4777-8666-555555555555"
		newer := finishedClaudeRun(t, store, home, "20261004-1000-bbbb", sessionID)
		syncRunToDataRef(ctx, repo, store, gc, testDataRef, false, newer)
		local, _ := gitOut(t, repo, "rev-parse", testDataRef)

		d := replay(store, repo, home)
		if d.SessionUUID != sessionID || d.SourceRunID != newer.ID {
			t.Fatalf("resumed %q from %q, want %q from %q (%s)", d.SessionUUID, d.SourceRunID, sessionID, newer.ID, d.Reason)
		}
		if got, _ := gitOut(t, repo, "rev-parse", testDataRef); got != local {
			t.Errorf("local %s moved from %s to %s during replay's fetch", testDataRef, local, got)
		}
		if got, _ := gitOut(t, origin, "rev-parse", testDataRef); got != published {
			t.Errorf("origin %s = %q, want it left at the published %s", testDataRef, got, published)
		}
	})
}

// TestPushLogPushesOnlyWhenAsked: push-log commits a held-back log to the
// local data ref, and publishes it only with --push.
func TestPushLogPushesOnlyWhenAsked(t *testing.T) {
	origin, repo, _, _ := setupBareRemote(t)
	t.Setenv("HOME", t.TempDir())
	const sessionID = "20261004-1100-pushlog-session"
	t.Setenv(sessionIDEnv, sessionID)
	store, err := run.NewHomeDirStore(sessionID)
	if err != nil {
		t.Fatalf("NewHomeDirStore: %v", err)
	}
	if err := store.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	const id = "20261004-1100-aaaa"
	logFile := filepath.Join(store.LogDir(), id+".jsonl")
	if err := os.WriteFile(logFile, []byte(`{"type":"result","subtype":"success"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(&run.State{ID: id, Branch: "b", CreatedAt: "2026-10-04T11:00:00Z", LogFile: &logFile, CloneDir: &repo}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Cleanup(func() { pushLogPush = false })
	pushLog := func(push bool) error {
		pushLogPush = push
		pushLogCmd.SetContext(context.Background())
		return pushLogCmd.RunE(pushLogCmd, []string{id})
	}

	if err := pushLog(false); err != nil {
		t.Fatalf("push-log: %v", err)
	}
	if files, _ := gitOut(t, repo, "ls-tree", "-r", "--name-only", testDataRef); !strings.Contains(files, "logs/"+id+".jsonl") {
		t.Fatalf("local %s missing the log; has:\n%s", testDataRef, files)
	}
	if _, err := gitOut(t, origin, "rev-parse", "--verify", "--quiet", testDataRef); err == nil {
		t.Fatalf("push-log without --push published %s", testDataRef)
	}

	if err := pushLog(true); err != nil {
		t.Fatalf("push-log --push: %v", err)
	}
	local, _ := gitOut(t, repo, "rev-parse", testDataRef)
	if remote, err := gitOut(t, origin, "rev-parse", "--verify", testDataRef); err != nil || remote != local {
		t.Fatalf("origin %s = %q (%v), want %s", testDataRef, remote, err, local)
	}

	runGitCmd(t, repo, "remote", "set-url", "origin", origin+"-gone")
	if err := pushLog(true); err == nil {
		t.Error("push-log --push must fail when the push fails")
	}
}

// TestSyncScansAgainstOwnRemote: a valley repo's origin is a private-IP
// scp-style remote. A log naming that origin is stored on the data ref; a log
// naming any other private address is kept off it.
func TestSyncScansAgainstOwnRemote(t *testing.T) {
	for _, tc := range []struct {
		name, log string
		stored    bool
	}{
		{"own origin", `{"type":"assistant","text":"To 10.0.2.1:the-valley.git"}`, true},
		{"other address", `{"type":"assistant","text":"To 10.0.2.2:the-valley.git"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			_, repo, _, _ := setupBareRemote(t)
			runGitCmd(t, repo, "remote", "set-url", "origin", "git@10.0.2.1:the-valley.git")
			store := run.NewHomeDirStoreFromPath(filepath.Join(t.TempDir(), "session"))
			if err := store.EnsureDirs(); err != nil {
				t.Fatal(err)
			}
			st := finishedClaudeRun(t, store, home, "20261015-1000-aaaa", "6623bdcf-1dce-4da0-86b3-a46743d208e8")
			if err := os.WriteFile(*st.LogFile, []byte(tc.log+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			syncRunToDataRef(context.Background(), repo, store, git.NewExecClient(), testDataRef, false, st)
			files, err := gitOut(t, repo, "ls-tree", "-r", "--name-only", testDataRef)
			if err != nil {
				t.Fatalf("ls-tree: %v %s", err, files)
			}
			if got := strings.Contains(files, "logs/"+st.ID+".jsonl"); got != tc.stored {
				t.Errorf("log stored = %v, want %v; files:\n%s", got, tc.stored, files)
			}
		})
	}
}
