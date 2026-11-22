package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"the-valley/flow/internal/config"
	"the-valley/flow/internal/flowenv"
	"the-valley/flow/internal/location"
	"the-valley/flow/internal/run"
	"the-valley/flow/internal/tmux"
)

// agentsSessionName is the detached tmux session holding this klaus session's
// agent panes. It is per-klaus-session so concurrent coordinators never share
// windows, and it is owned by the tmux server — so agents survive the
// coordinator exiting.
//
// Careful: agent panes export KLAUS_SESSION_ID (see tmuxSessionEnvPrefix), so
// every process an agent starts inside its worktree — including `go test` —
// inherits the coordinator's session id and resolves this to the live agents
// session. Tests must never let a real tmux client see this name; use a fake
// tmux client and t.Setenv(sessionIDEnv, ...) to pin it.
func agentsSessionName() string {
	return location.AgentsSession(sessionIDFromEnv())
}

// workerDirs are a local worker's own directories, which launch creates.
type workerDirs struct {
	tmux    string // TMUX_TMPDIR (workerTmuxDir)
	scratch string // flowenv.Scratch (workerScratchDir)
}

// workerTmuxDir is the TMUX_TMPDIR a run's worker gets: the run's own, beside
// the store's logs/, where no server runs until the worker starts one.
func workerTmuxDir(store run.StateStore, runID string) string {
	return filepath.Join(filepath.Dir(store.LogDir()), "tmux", runID)
}

// workerScratchDir is where a run's worker puts build output and other
// scratch files, beside the store's logs/ and so outside the worktree, where
// finalize would salvage them into a commit. cleanupWorktree removes it with
// the worktree.
func workerScratchDir(store run.StateStore, runID string) string {
	return filepath.Join(filepath.Dir(store.LogDir()), "scratch", runID)
}

// workerEnv starts a local worker cut off from the tmux server its pane runs
// in, with dirs.scratch as its scratch directory (flowenv.Scratch). In a pane
// $TMUX names that server and overrides TMUX_TMPDIR, so a worker's
// `TMUX_TMPDIR=<test dir> tmux kill-server` once killed the shared server: the
// operator's session, the coordinator and every agent's pane. The worker gets
// no $TMUX or $TMUX_PANE, and dirs.tmux as TMUX_TMPDIR, so a bare tmux there
// finds no server. klaus commands the worker runs (_pre-review, status, logs)
// reach the shared server through flowenv.TmuxSocket instead, which holds the
// socket flow found or nothing. _finalize, budget pause included, runs after
// the worker in the pane's own shell and keeps the pane's $TMUX.
func workerEnv(dirs workerDirs) string {
	env := "env -u TMUX -u TMUX_PANE TMUX_TMPDIR=" + shellQuote(dirs.tmux) + " " + flowenv.Scratch.EnvPrefix(dirs.scratch)
	if sock := tmux.SocketPath(); sock != "" {
		return env + flowenv.TmuxSocket.EnvPrefix(sock)
	}
	return env + flowenv.TmuxSocket.UnsetPrefix()
}

// startAgentPane creates the tmux pane an agent runs in and applies the
// standard title/rename settings.
//
// In detached mode (the default) the pane is a window in the agents session,
// named in tmuxSession for the run to record, so the coordinator's window is
// left alone and splitFrom comes back empty. In "pane" mode the coordinator's
// window is split and splitFrom is the pane that was split, so the caller can
// re-pin its layout.
func startAgentPane(ctx context.Context, tc tmux.Client, mode, runID, dir, paneCmd, title string) (paneID, splitFrom, tmuxSession string, err error) {
	if mode == config.AgentDisplayPane {
		splitFrom = os.Getenv("TMUX_PANE")
		paneID, err = tc.SplitWindow(ctx, splitFrom, dir, paneCmd)
	} else {
		tmuxSession = agentsSessionName()
		paneID, err = tc.NewDetachedWindow(ctx, tmuxSession, runID, dir, paneCmd)
	}
	if err != nil {
		return "", "", "", fmt.Errorf("creating tmux pane: %w", err)
	}

	if err := tc.SetPaneTitle(ctx, paneID, title); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to set pane title: %v\n", err)
	}
	if err := tc.SetWindowOption(ctx, paneID, "automatic-rename", "off"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to disable automatic rename: %v\n", err)
	}
	if err := tc.LockPaneTitle(ctx, paneID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to lock pane title: %v\n", err)
	}

	if splitFrom != "" {
		if err := tc.RebalanceLayout(ctx, splitFrom); err != nil {
			return paneID, splitFrom, tmuxSession, fmt.Errorf("rebalancing tmux layout: %w", err)
		}
	}
	return paneID, splitFrom, tmuxSession, nil
}

// killEmptyAgentsSession drops each detached agents session the runs of
// before were in once no run the store holds now has a pane in it: the
// session a run recorded, when it is a name location gives the run's
// session, and the computed legacy name for runs that recorded none or
// another. before is the runs as they were before cleanup removed any,
// whose records no longer say which session they were in. tmux already
// destroys a session whose last window closes; this covers leftovers (e.g. a
// window whose command exited into a shell) so `klaus cleanup --all` leaves
// nothing behind.
//
// The store is only advisory — it can be stale, or belong to a different
// klaus session entirely — so the tmux server gets the final say: a session
// with any pane still running a command is never killed.
func killEmptyAgentsSession(ctx context.Context, before []*run.State, store run.StateStore, tc tmux.Client) {
	states, err := store.List()
	if err != nil {
		return
	}
	// The runs are the store's session's: this process's, unless cleanup
	// fell back to the latest session.
	id := sessionIDFromEnv()
	if hs, ok := store.(*run.HomeDirStore); ok {
		id = filepath.Base(hs.BaseDir())
	}
	for _, name := range agentsSessionNames(id, append(before, states...)) {
		killIfEmpty(ctx, name, states, tc)
	}
}

// agentsSessionNames are the agents sessions the runs of states, all of the
// flow session id, put their panes in: the computed legacy name, and each
// name a run recorded that location gives session id under some
// generation. Any other recorded name is not a session flow made for these
// runs: it gets one warning and is never killed.
func agentsSessionNames(id string, states []*run.State) []string {
	names := []string{agentsSessionName()}
	ours := location.AgentsSessions(id)
	warned := map[string]bool{}
	for _, s := range states {
		switch name := s.TmuxSession; {
		case name == "" || slices.Contains(names, name):
		case slices.Contains(ours, name):
			names = append(names, name)
		case !warned[s.ID+"\x00"+name]:
			warned[s.ID+"\x00"+name] = true
			fmt.Printf("  warning: run %s records tmux session %s, not one flow names for session %s; ignoring it\n", s.ID, name, id)
		}
	}
	return names
}

func killIfEmpty(ctx context.Context, name string, states []*run.State, tc tmux.Client) {
	panes, err := tc.SessionPanes(ctx, name)
	if err != nil {
		return // session does not exist — nothing to do
	}

	// The tmux server is the truth: a pane whose command is still running
	// belongs to a live agent, whatever the store says about it.
	for _, p := range panes {
		if tc.PaneExists(ctx, p) && !tc.PaneIsIdle(ctx, p) {
			fmt.Printf("  keeping agents tmux session %s (pane %s still running)\n", name, p)
			return
		}
	}

	held := make(map[string]bool, len(states))
	for _, s := range states {
		if s.TmuxPane != nil {
			held[*s.TmuxPane] = true
		}
	}
	for _, p := range panes {
		if held[p] {
			fmt.Printf("  keeping agents tmux session %s (pane %s still held by a run)\n", name, p)
			return
		}
	}

	if err := tc.KillSession(ctx, name); err == nil {
		fmt.Printf("  killed agents tmux session %s\n", name)
	}
}
