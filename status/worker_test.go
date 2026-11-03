package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"
)

// A pass prunes exactly the roots no batch has used for longer than the
// age it keeps outputs for, and nothing in the directory that is not a
// root symlink.
func TestPruneRootsRemovesExactlyTheRootsOlderThanTheAge(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	keep := 7 * 24 * time.Hour
	used := map[string]time.Time{
		"aaaa-stale":     now.Add(-keep - time.Hour),
		"bbbb-dangling":  now.Add(-30 * 24 * time.Hour),
		"cccc-recent":    now.Add(-keep + time.Hour),
		"dddd-today":     now,
		"eeee-not-alink": now.Add(-30 * 24 * time.Hour),
	}
	for name, at := range used {
		path := filepath.Join(dir, name)
		if name == "eeee-not-alink" {
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Symlink("/nix/store/"+name, path); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("touch", "-h", "-t", at.Local().Format("200601021504.05"), path).CombinedOutput(); err != nil {
			t.Fatalf("touch %s: %v: %s", path, err, out)
		}
	}

	if err := pruneRoots(dir, keep, now); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	sort.Strings(left)
	want := []string{"cccc-recent", "dddd-today", "eeee-not-alink"}
	if !slices.Equal(left, want) {
		t.Errorf("pruning left %v, want %v", left, want)
	}

	if err := pruneRoots(filepath.Join(dir, "missing"), keep, now); err != nil {
		t.Errorf("pruning a roots directory that does not exist yet: %v", err)
	}
}
