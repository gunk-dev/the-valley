package run

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"the-valley/flow/internal/location"
)

func TestHomeDirStore_SaveLoadListDelete(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewHomeDirStoreFromPath(tmpDir)

	if err := store.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	// Verify directories created
	if _, err := os.Stat(store.StateDir()); err != nil {
		t.Fatalf("StateDir not created: %v", err)
	}
	if _, err := os.Stat(store.LogDir()); err != nil {
		t.Fatalf("LogDir not created: %v", err)
	}

	// Save
	state := &State{
		ID:        "20260307-1200-abcd",
		Prompt:    "Fix the bug",
		Branch:    "agent/20260307-1200-abcd",
		Worktree:  "/tmp/worktree",
		CreatedAt: "2026-03-07T12:00:00Z",
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists on disk
	stateFile := filepath.Join(store.StateDir(), state.ID+".json")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("state file not on disk: %v", err)
	}

	// Load
	loaded, err := store.Load(state.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID != state.ID {
		t.Errorf("ID mismatch: got %q, want %q", loaded.ID, state.ID)
	}
	if loaded.Prompt != state.Prompt {
		t.Errorf("Prompt mismatch: got %q, want %q", loaded.Prompt, state.Prompt)
	}

	// Save a second state
	state2 := &State{
		ID:        "20260307-1201-bcde",
		Prompt:    "Add tests",
		Branch:    "agent/20260307-1201-bcde",
		Worktree:  "/tmp/worktree2",
		CreatedAt: "2026-03-07T12:01:00Z",
	}
	if err := store.Save(state2); err != nil {
		t.Fatalf("Save second: %v", err)
	}

	// List
	states, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("List: got %d states, want 2", len(states))
	}
	// Sorted by CreatedAt descending
	if states[0].ID != state2.ID {
		t.Errorf("List order: first should be newest, got %q", states[0].ID)
	}

	// Delete
	if err := store.Delete(state.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify deleted
	_, err = store.Load(state.ID)
	if err == nil {
		t.Error("Load after Delete should fail")
	}

	states, err = store.List()
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(states) != 1 {
		t.Errorf("List after delete: got %d states, want 1", len(states))
	}
}

func TestHomeDirStore_LoadNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewHomeDirStoreFromPath(tmpDir)
	if err := store.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}

	_, err := store.Load("nonexistent")
	if err == nil {
		t.Error("Load nonexistent should return error")
	}
}

func TestHomeDirStore_ListEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewHomeDirStoreFromPath(tmpDir)
	// Don't create dirs — should return nil, nil
	states, err := store.List()
	if err != nil {
		t.Fatalf("List on missing dir: %v", err)
	}
	if states != nil {
		t.Errorf("List on missing dir: got %v, want nil", states)
	}
}

func TestHomeDirStore_Paths(t *testing.T) {
	store := NewHomeDirStoreFromPath("/home/user/.klaus/sessions/session-123")
	if got := store.StateDir(); got != "/home/user/.klaus/sessions/session-123/runs" {
		t.Errorf("StateDir: got %q", got)
	}
	if got := store.LogDir(); got != "/home/user/.klaus/sessions/session-123/logs" {
		t.Errorf("LogDir: got %q", got)
	}
	if got := store.BaseDir(); got != "/home/user/.klaus/sessions/session-123" {
		t.Errorf("BaseDir: got %q", got)
	}
}

func TestMostRecentSession(t *testing.T) {
	t.Run("returns latest session under either root", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		dirs := []string{
			filepath.Join(home, ".klaus", "sessions", "session-20260301-0900-aaaa"),
			filepath.Join(home, ".valley", "flow", "sessions", "session-20260310-1000-cccc"),
			filepath.Join(home, ".klaus", "sessions", "session-20260305-1400-bbbb"),
			filepath.Join(home, ".klaus", "sessions", "not-a-session"),
		}
		for _, d := range dirs {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
		}
		got, err := MostRecentSession()
		if err != nil {
			t.Fatalf("MostRecentSession: %v", err)
		}
		if got != "session-20260310-1000-cccc" {
			t.Errorf("got %q, want session-20260310-1000-cccc", got)
		}
		store, err := OpenSession(got)
		if err != nil || store.BaseDir() != dirs[1] {
			t.Errorf("OpenSession(%s) = %v, %v; want %s", got, store, err, dirs[1])
		}
	})

	t.Run("error when no sessions exist", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if _, err := MostRecentSession(); err == nil {
			t.Error("expected error with no sessions")
		}
	})

	t.Run("error when one ID is under both roots", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		for _, d := range []string{
			filepath.Join(home, ".klaus", "sessions", "session-20260301-0900-aaaa"),
			filepath.Join(home, ".valley", "flow", "sessions", "session-20260301-0900-aaaa"),
		} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		var conflict *location.ConflictError
		if _, err := MostRecentSession(); !errors.As(err, &conflict) {
			t.Errorf("MostRecentSession() = %v, want a ConflictError", err)
		}
	})
}

// A sessions root that can't be read is not the absence of sessions: a
// caller that would start a new session on ErrNoSessions must not.
func TestMostRecentSessionUnreadableRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".klaus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".klaus", "sessions"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MostRecentSession(); err == nil || errors.Is(err, ErrNoSessions) {
		t.Errorf("MostRecentSession() over an unreadable root = %v, want an error other than ErrNoSessions", err)
	}
	t.Setenv("HOME", t.TempDir())
	if _, err := MostRecentSession(); !errors.Is(err, ErrNoSessions) {
		t.Errorf("MostRecentSession() with no roots = %v, want ErrNoSessions", err)
	}
}

// An unknown session ID fails lookup, and makes no directory.
func TestOpenSessionUnknown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := OpenSession("session-20260301-0900-aaaa"); !errors.Is(err, location.ErrUnknownSession) {
		t.Errorf("OpenSession(unknown) = %v, want ErrUnknownSession", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("an unknown session's lookup wrote %v", entries)
	}
	store, err := CreateSession("session-20260301-0900-aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".klaus", "sessions", "session-20260301-0900-aaaa"); store.BaseDir() != want {
		t.Errorf("CreateSession made %s, want %s", store.BaseDir(), want)
	}
	if _, err := OpenSession("session-20260301-0900-aaaa"); err != nil {
		t.Errorf("OpenSession after CreateSession: %v", err)
	}
}
