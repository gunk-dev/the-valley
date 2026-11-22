package cmd

import (
	"fmt"

	"the-valley/flow/internal/flowenv"
	"the-valley/flow/internal/run"
)

// sessionIDFromEnv returns the current session ID from VALLEY_FLOW_SESSION_ID
// or KLAUS_SESSION_ID.
func sessionIDFromEnv() string { return flowenv.SessionID.Get() }

// tmuxSessionEnvPrefix returns a shell snippet that exports the session ID
// for use in tmux pane commands. Tmux panes start fresh shells that don't
// inherit the caller's environment, so any env vars they need must be
// explicitly exported in the command string. Every process an agent starts in
// its worktree inherits the ID, `go test` included, so anything that turns it
// into a tmux target (agentsSessionName) must never be handed a real tmux
// client from a test; see docs/development.md.
func tmuxSessionEnvPrefix() string {
	sessionID := sessionIDFromEnv()
	if sessionID == "" {
		return ""
	}
	return flowenv.SessionID.Export(sessionID)
}

// sessionStore returns a HomeDirStore for the current session (from
// VALLEY_FLOW_SESSION_ID or KLAUS_SESSION_ID). This is the canonical way to get
// the store for the active session.
func sessionStore() (run.StateStore, error) {
	sessionID := sessionIDFromEnv()
	if sessionID == "" {
		// Fall back to most recent session
		sessionsDir, err := run.SessionsDir()
		if err != nil {
			return nil, fmt.Errorf("KLAUS_SESSION_ID is not set and could not find sessions directory: %w", err)
		}
		found, err := run.FindMostRecentSession(sessionsDir)
		if err != nil {
			return nil, fmt.Errorf("KLAUS_SESSION_ID is not set and no sessions found (are you inside a flow session?)")
		}
		sessionID = found
	}
	store, err := run.NewHomeDirStore(sessionID)
	if err != nil {
		return nil, err
	}
	return store, nil
}
