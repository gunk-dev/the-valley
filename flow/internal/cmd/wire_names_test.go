package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/flow/internal/backend"
	"the-valley/flow/internal/config"
	"the-valley/flow/internal/consult"
	"the-valley/flow/internal/event"
	"the-valley/flow/internal/flowenv"
	"the-valley/flow/internal/run"
	"github.com/spf13/cobra"
)

// The names flow sets for the processes it starts, as tests set them.
var (
	sessionIDEnv = flowenv.SessionID.Legacy
	runIDEnv     = flowenv.RunID.Legacy
	runEventsEnv = flowenv.RunEvents.Legacy
	scratchEnv   = flowenv.Scratch.Legacy
	socketEnv    = flowenv.TmuxSocket.Legacy
)

func unsetFlowEnv(t *testing.T, v flowenv.Var) {
	t.Helper()
	t.Setenv(v.New, "")
	t.Setenv(v.Legacy, "")
	_ = os.Unsetenv(v.New)
	_ = os.Unsetenv(v.Legacy)
}

func TestFinalizeFromLog_ExitEventBothSpellings(t *testing.T) {
	tests := []struct {
		name       string
		logContent string
		wantReason string
		wantSub    string
	}{
		{
			name:       "legacy klaus_exit nonzero",
			logContent: `{"type":"klaus_exit","exit_code":2}` + "\n",
			wantReason: "codex exited with status 2",
			wantSub:    "error_during_execution",
		},
		{
			name:       "new flow_exit nonzero",
			logContent: `{"type":"flow_exit","exit_code":2}` + "\n",
			wantReason: "codex exited with status 2",
			wantSub:    "error_during_execution",
		},
		{
			name:       "both klaus_exit and flow_exit normalize to one result",
			logContent: "{\"type\":\"klaus_exit\",\"exit_code\":2}\n{\"type\":\"flow_exit\",\"exit_code\":2}\n",
			wantReason: "codex exited with status 2",
			wantSub:    "error_during_execution",
		},
		{
			name:       "flow_exit zero after result leaves no failure",
			logContent: "{\"type\":\"result\",\"subtype\":\"success\"}\n{\"type\":\"flow_exit\",\"exit_code\":0}\n",
			wantReason: "",
			wantSub:    "success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, store := setupFinalizeTest(t, tt.logContent)
			state.Backend = string(backend.Codex)
			sub, err := finalizeFromLog(store, state)
			if err != nil {
				t.Fatalf("finalizeFromLog: %v", err)
			}
			if sub != tt.wantSub {
				t.Errorf("subtype = %q, want %q", sub, tt.wantSub)
			}
			gotReason := ""
			if state.FailureReason != nil {
				gotReason = *state.FailureReason
			}
			if gotReason != tt.wantReason {
				t.Errorf("FailureReason = %q, want %q", gotReason, tt.wantReason)
			}
		})
	}
}

func TestSessionIDReadersAcceptBothSpellings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Prepare two sessions so sessionStore can open either.
	for _, id := range []string{"sess-old", "sess-new"} {
		st, err := run.CreateSession(id)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.EnsureDirs(); err != nil {
			t.Fatal(err)
		}
		pane := "%9"
		if err := st.Save(&run.State{ID: id, Type: "session", CoordinatorPane: &pane}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("legacy only", func(t *testing.T) {
		unsetFlowEnv(t, flowenv.SessionID)
		t.Setenv(flowenv.SessionID.Legacy, "sess-old")

		if got := agentsSessionName(); got != "klaus-agents-sess-old" {
			t.Errorf("agentsSessionName() = %q", got)
		}
		if got := tmuxSessionEnvPrefix(); got != "unset VALLEY_FLOW_SESSION_ID; export KLAUS_SESSION_ID='sess-old'; " {
			t.Errorf("tmuxSessionEnvPrefix() = %q", got)
		}
		st, err := sessionStore()
		if err != nil {
			t.Fatalf("sessionStore(): %v", err)
		}
		if got := filepath.Base(st.(*run.HomeDirStore).BaseDir()); got != "sess-old" {
			t.Errorf("sessionStore base = %q, want sess-old", got)
		}
		if !defaultIsRunActive(&run.State{ID: "sess-old", Type: "session"}, &fakeTmux{}) {
			t.Error("defaultIsRunActive should see current session from KLAUS_SESSION_ID")
		}
		m := dashboardModel{store: st}
		if got := m.coordinatorPane(); got != "%9" {
			t.Errorf("coordinatorPane() = %q, want %%9", got)
		}
	})

	t.Run("new only", func(t *testing.T) {
		unsetFlowEnv(t, flowenv.SessionID)
		t.Setenv(flowenv.SessionID.New, "sess-new")

		if got := agentsSessionName(); got != "klaus-agents-sess-new" {
			t.Errorf("agentsSessionName() = %q", got)
		}
		// Writers export KLAUS_SESSION_ID, with the value read from VALLEY_FLOW_SESSION_ID, and drop the new spelling.
		if got := tmuxSessionEnvPrefix(); got != "unset VALLEY_FLOW_SESSION_ID; export KLAUS_SESSION_ID='sess-new'; " {
			t.Errorf("tmuxSessionEnvPrefix() = %q", got)
		}
		st, err := sessionStore()
		if err != nil {
			t.Fatalf("sessionStore(): %v", err)
		}
		if got := filepath.Base(st.(*run.HomeDirStore).BaseDir()); got != "sess-new" {
			t.Errorf("sessionStore base = %q, want sess-new", got)
		}
		if !defaultIsRunActive(&run.State{ID: "sess-new", Type: "session"}, &fakeTmux{}) {
			t.Error("defaultIsRunActive should see current session from VALLEY_FLOW_SESSION_ID")
		}
		m := dashboardModel{store: st}
		if got := m.coordinatorPane(); got != "%9" {
			t.Errorf("coordinatorPane() = %q, want %%9", got)
		}
	})

	t.Run("new empty overrides legacy", func(t *testing.T) {
		unsetFlowEnv(t, flowenv.SessionID)
		t.Setenv(flowenv.SessionID.Legacy, "sess-old")
		t.Setenv(flowenv.SessionID.New, "")

		if got := agentsSessionName(); got != "klaus-agents" {
			t.Errorf("agentsSessionName() = %q, want klaus-agents", got)
		}
		if got := tmuxSessionEnvPrefix(); got != "" {
			t.Errorf("tmuxSessionEnvPrefix() = %q, want empty", got)
		}
	})
}

func TestResolveAgentBackend_EnvSpellings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unsetFlowEnv(t, flowenv.SessionID)

	newCmd := func() *cobra.Command {
		c := &cobra.Command{}
		c.Flags().String("backend", "", "")
		return c
	}

	t.Run("legacy only", func(t *testing.T) {
		unsetFlowEnv(t, flowenv.AgentBackend)
		t.Setenv(flowenv.AgentBackend.Legacy, "codex")
		got, err := resolveAgentBackend(newCmd(), config.Config{})
		if err != nil || got != backend.Codex {
			t.Errorf("resolveAgentBackend() = %q, %v; want codex", got, err)
		}
	})

	t.Run("new only", func(t *testing.T) {
		unsetFlowEnv(t, flowenv.AgentBackend)
		t.Setenv(flowenv.AgentBackend.New, "agy")
		got, err := resolveAgentBackend(newCmd(), config.Config{})
		if err != nil || got != backend.Agy {
			t.Errorf("resolveAgentBackend() = %q, %v; want agy", got, err)
		}
	})

	t.Run("both set prefers new", func(t *testing.T) {
		unsetFlowEnv(t, flowenv.AgentBackend)
		t.Setenv(flowenv.AgentBackend.Legacy, "codex")
		t.Setenv(flowenv.AgentBackend.New, "agy")
		got, err := resolveAgentBackend(newCmd(), config.Config{})
		if err != nil || got != backend.Agy {
			t.Errorf("resolveAgentBackend() = %q, %v; want agy", got, err)
		}
	})

	t.Run("new empty overrides legacy and falls through to config", func(t *testing.T) {
		unsetFlowEnv(t, flowenv.AgentBackend)
		t.Setenv(flowenv.AgentBackend.Legacy, "codex")
		t.Setenv(flowenv.AgentBackend.New, "")
		got, err := resolveAgentBackend(newCmd(), config.Config{DefaultAgentBackend: "agy"})
		if err != nil || got != backend.Agy {
			t.Errorf("resolveAgentBackend() = %q, %v; want agy", got, err)
		}
	})
}

func TestRunIDAndRunEventsReadersAcceptBothSpellings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unsetFlowEnv(t, flowenv.SessionID)
	t.Setenv(flowenv.SessionID.New, "sess-run-test")

	st, err := run.CreateSession("sess-run-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	t.Run("currentPaneRun reads VALLEY_FLOW_RUN_ID", func(t *testing.T) {
		unsetFlowEnv(t, flowenv.RunID)
		t.Setenv(flowenv.RunID.New, "run-new")
		p := currentPaneRun()
		if p == nil || p.id != "run-new" {
			t.Fatalf("currentPaneRun() = %+v, want id=run-new", p)
		}
	})

	t.Run("currentPaneRun reads KLAUS_RUN_ID", func(t *testing.T) {
		unsetFlowEnv(t, flowenv.RunID)
		t.Setenv(flowenv.RunID.Legacy, "run-old")
		p := currentPaneRun()
		if p == nil || p.id != "run-old" {
			t.Fatalf("currentPaneRun() = %+v, want id=run-old", p)
		}
	})

	t.Run("startPreReviewCheck routes sandbox output with VALLEY_FLOW_*", func(t *testing.T) {
		unsetFlowEnv(t, flowenv.RunID)
		unsetFlowEnv(t, flowenv.RunEvents)
		t.Setenv(flowenv.RunID.New, "run-sandbox")
		t.Setenv(flowenv.RunEvents.New, runEventsOutput)
		c := startPreReviewCheck()
		if c.report == nil {
			t.Error("expected sandbox report writer when VALLEY_FLOW_RUN_ID and VALLEY_FLOW_RUN_EVENTS=output are set")
		}
	})

	t.Run("startPreReviewCheck empty VALLEY_FLOW_RUN_EVENTS overrides KLAUS_RUN_EVENTS", func(t *testing.T) {
		unsetFlowEnv(t, flowenv.RunID)
		unsetFlowEnv(t, flowenv.RunEvents)
		t.Setenv(flowenv.RunID.Legacy, "run-sandbox")
		t.Setenv(flowenv.RunEvents.Legacy, runEventsOutput)
		t.Setenv(flowenv.RunEvents.New, "")
		c := startPreReviewCheck()
		if c.report != nil {
			t.Error("expected empty VALLEY_FLOW_RUN_EVENTS to override KLAUS_RUN_EVENTS=output")
		}
	})
}

func TestConsultSkipsBackendFromValleyFlowEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unsetFlowEnv(t, flowenv.SessionID)
	unsetFlowEnv(t, flowenv.Backend)
	t.Setenv(flowenv.Backend.Legacy, "claude")
	t.Setenv(flowenv.Backend.New, "codex")

	stubDir := t.TempDir()
	claudeStub := filepath.Join(stubDir, "claude")
	codexStub := filepath.Join(stubDir, "codex")
	if err := os.WriteFile(claudeStub, []byte("#!/bin/sh\necho '{\"type\":\"result\",\"result\":\"from-claude\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexStub, []byte("#!/bin/sh\necho from-codex\n"), 0700); err != nil {
		t.Fatal(err)
	}
	oldLookup := consult.LookupBinary
	t.Cleanup(func() { consult.LookupBinary = oldLookup })
	consult.LookupBinary = func(name string) (string, error) {
		switch name {
		case "claude":
			return claudeStub, nil
		case "codex":
			return codexStub, nil
		default:
			return "", os.ErrNotExist
		}
	}

	c := newConsultCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"--dir", t.TempDir(), "hello"})
	if err := c.Execute(); err != nil {
		t.Fatalf("consult execute: %v", err)
	}
	if !strings.Contains(out.String(), "from-claude") {
		t.Errorf("consult output = %q, want claude (codex excluded by VALLEY_FLOW_BACKEND)", out.String())
	}
}

func TestWatchReadsValleyFlowSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unsetFlowEnv(t, flowenv.SessionID)
	t.Setenv(flowenv.SessionID.New, "sess-watch-new")

	st, err := run.CreateSession("sess-watch-new")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	evLog := event.NewLog(st.BaseDir())
	if err := evLog.Emit(event.New("run-1", event.AgentNeedsAttention, map[string]interface{}{"reason": "test"})); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	watchCmd.SetOut(&out)
	watchCmd.SetErr(&bytes.Buffer{})
	t.Cleanup(func() {
		watchCmd.SetOut(os.Stdout)
		watchCmd.SetErr(os.Stderr)
		_ = watchCmd.Flags().Set("once", "false")
		_ = watchCmd.Flags().Set("since-start", "false")
		_ = watchCmd.Flags().Set("session-id", "")
	})
	_ = watchCmd.Flags().Set("once", "true")
	_ = watchCmd.Flags().Set("since-start", "true")
	_ = watchCmd.Flags().Set("session-id", "")
	watchCmd.SetContext(context.Background())
	if err := watchCmd.RunE(watchCmd, nil); err != nil {
		t.Fatalf("watchCmd: %v", err)
	}
	if !strings.Contains(out.String(), "run-1") {
		t.Errorf("watch output = %q, want run-1 event", out.String())
	}
}
