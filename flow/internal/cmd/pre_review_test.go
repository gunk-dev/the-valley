package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeveritiesAtOrAbove(t *testing.T) {
	tests := []struct {
		level    string
		included []string
		excluded []string
	}{
		{
			level:    "critical",
			included: []string{"critical"},
			excluded: []string{"high", "medium", "low"},
		},
		{
			level:    "high",
			included: []string{"high", "critical"},
			excluded: []string{"medium", "low"},
		},
		{
			level:    "medium",
			included: []string{"medium", "high", "critical"},
			excluded: []string{"low"},
		},
		{
			level:    "low",
			included: []string{"low", "medium", "high", "critical"},
			excluded: []string{},
		},
		{
			level:    "unknown",
			included: []string{"critical"},
			excluded: []string{"high", "medium", "low"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			result := severitiesAtOrAbove(tt.level)
			for _, s := range tt.included {
				if !result[s] {
					t.Errorf("expected %q to be included for level %q", s, tt.level)
				}
			}
			for _, s := range tt.excluded {
				if result[s] {
					t.Errorf("expected %q to be excluded for level %q", s, tt.level)
				}
			}
		})
	}
}

// TestPreReviewDiffsAgainstOriginsDefaultBranch runs _pre-review in a clone
// whose local main lags origin's, with a stub reviewer that saves its
// prompt: the reviewer gets the commit the work adds on origin's main, not
// the commit that landed since the local main.
func TestPreReviewDiffsAgainstOriginsDefaultBranch(t *testing.T) {
	home, repo, bin := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KLAUS_BACKEND", "claude")
	t.Setenv(runIDEnv, "")
	t.Setenv(runEventsEnv, "")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".klaus", "config.json"), `{"backends": {"claude": {"review_backend": "claude"}}, "pre_review": {"prose": {"enabled": false}}}`)
	prompt := filepath.Join(bin, "prompt")
	write(filepath.Join(bin, "claude"), "#!/bin/sh\ncat > '"+prompt+"'\necho '{\"findings\": [], \"summary\": \"clean\"}'\n")

	commit := func(name string) string {
		write(filepath.Join(repo, name), name+"\n")
		snapshotGit(t, repo, "add", name)
		snapshotGit(t, repo, "commit", "-qm", name)
		return snapshotGit(t, repo, "rev-parse", "HEAD")
	}
	snapshotGit(t, repo, "init", "-q")
	base := commit("base.txt")
	landed := commit("landed.txt")
	snapshotGit(t, repo, "update-ref", "refs/remotes/origin/main", landed)
	snapshotGit(t, repo, "checkout", "-qb", "work", "origin/main")
	snapshotGit(t, repo, "update-ref", "refs/heads/main", base)
	commit("work.txt")

	rootCmd.SetArgs([]string{"_pre-review", "--dir", repo})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(prompt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "+++ b/work.txt") || strings.Contains(string(data), "landed.txt") {
		t.Errorf("the reviewer did not get the work's own diff:\n%s", data)
	}
}
