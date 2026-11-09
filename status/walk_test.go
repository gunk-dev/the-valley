package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The configuration a step borrows goes after the entries the caller's
// environment gives git already, which are kept, and the count covers
// both.
func TestWithConfigAppendsToTheCallersEntries(t *testing.T) {
	env := []string{"HOME=/home/operator", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.pager", "GIT_CONFIG_VALUE_0=cat"}
	got := withConfig(env, [][2]string{{"valley.principal", "alice"}, {"user.name", "alice"}})
	want := []string{"HOME=/home/operator", "GIT_CONFIG_KEY_0=core.pager", "GIT_CONFIG_VALUE_0=cat",
		"GIT_CONFIG_KEY_1=valley.principal", "GIT_CONFIG_VALUE_1=alice",
		"GIT_CONFIG_KEY_2=user.name", "GIT_CONFIG_VALUE_2=alice", "GIT_CONFIG_COUNT=3"}
	if !slices.Equal(got, want) {
		t.Fatalf("withConfig gave\n%q\nwant\n%q", got, want)
	}
	if got := withConfig(env, nil); !slices.Equal(got, env) {
		t.Fatalf("withConfig with nothing to borrow changed the environment: %q", got)
	}
}

// An outcome file that is missing, names no outcome, or gives an outcome
// the wrong fields is no outcome, so the walk counts the step an error.
func TestOutcomeFileReadsOnlyAKnownOutcome(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name, content string
		ok            bool
	}{
		{"approved", "approved\tkmkmid\tkmkm\t2\t0123abcd\n", true},
		{"skipped", "skipped\n", true},
		{"not-reviewed", "not-reviewed\tit is a draft\n", true},
		{"unknown", "landed\n", false},
		{"short", "approved\tkmkmid\tkmkm\n", false},
		{"empty", "", false},
	} {
		path := filepath.Join(dir, c.name)
		if err := os.WriteFile(path, []byte(c.content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := readOutcome(path); ok != c.ok {
			t.Errorf("%s: read as an outcome: %v, want %v", c.name, ok, c.ok)
		}
	}
	if _, ok := readOutcome(filepath.Join(dir, "missing")); ok {
		t.Error("a missing outcome file read as an outcome")
	}
}
