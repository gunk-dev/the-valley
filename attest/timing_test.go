package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The timing log is a file other tools will read line by line, so its
// lines are pinned here: one JSON object per check, with exactly these
// fields, appended to whatever the file already holds.
func TestTimingLogAppendsOneLinePerCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "valley", "timings.jsonl")
	var warned bytes.Buffer
	log := &timingLog{path: path, warn: &warned}
	log.append(checkTiming{
		Time: "2026-10-20T12:00:00.000Z", Repository: "ssh://git@host/project.git",
		Change: "kqxmkqxmkqxmkqxmkqxmkqxmkqxmkqxm", Version: 2, Head: strings.Repeat("a", 40),
		Check: "prose-format", Runner: "nix", Cost: "quick", Result: "passed", Satisfied: satisfiedCached,
		DurationMS: 1234,
	})
	// A plain branch has no change ID or version.
	log.append(checkTiming{
		Time: "2026-10-20T12:00:01.234Z", Repository: "/srv/project", Head: strings.Repeat("b", 40),
		Check: "tree-ok", Runner: "command", Result: "failed", Satisfied: satisfiedRan,
	})
	if warned.Len() != 0 {
		t.Fatalf("a writable log warned: %s", warned.String())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("the log holds %d lines, want 2:\n%s", len(lines), data)
	}
	want := [][]string{
		{"change", "check", "cost", "duration_ms", "head", "repository", "result", "runner", "satisfied", "time", "version"},
		{"check", "duration_ms", "head", "repository", "result", "runner", "satisfied", "time"},
	}
	for i, line := range lines {
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatalf("line %d is not JSON: %v: %s", i+1, err, line)
		}
		var keys []string
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if !reflect.DeepEqual(keys, want[i]) {
			t.Errorf("line %d has the fields %v, want %v", i+1, keys, want[i])
		}
	}
	var first checkTiming
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.DurationMS != 1234 || first.Version != 2 || first.Satisfied != "cached" {
		t.Errorf("the first line reads back as %+v", first)
	}
}

// A log that cannot be written says so once and goes on: the run it
// belongs to is not failed by it, and is not made to repeat the warning
// for every check after the first.
func TestAnUnwritableTimingLogOnlyWarns(t *testing.T) {
	// A directory that is a regular file cannot be written under, whoever
	// runs the test.
	blocker := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(blocker, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var warned bytes.Buffer
	log := &timingLog{path: filepath.Join(blocker, "valley", "timings.jsonl"), warn: &warned}
	for _, check := range []string{"one", "two", "three"} {
		log.append(checkTiming{Check: check, Result: "passed"})
	}
	lines := strings.Split(strings.TrimSuffix(warned.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "attest: warning: timings not logged: ") {
		t.Fatalf("an unwritable log warned %q, want one warning line", warned.String())
	}
}

func TestTimingsPathFollowsXDGStateHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fallback := filepath.Join(home, ".local", "state", "valley", "timings.jsonl")
	for _, c := range []struct{ state, want string }{
		{"/var/lib/someone/state", "/var/lib/someone/state/valley/timings.jsonl"},
		{"", fallback},
		// The specification ignores a relative path.
		{"relative/state", fallback},
	} {
		t.Setenv("XDG_STATE_HOME", c.state)
		got, err := timingsPath()
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("XDG_STATE_HOME=%q: %s, want %s", c.state, got, c.want)
		}
	}
}

// An https remote can carry a token in its URL, and the log is not the
// place for it.
func TestTheRepositoryNameCarriesNoCredentials(t *testing.T) {
	for remote, want := range map[string]string{
		"https://someone:ghp_token@forge.example/project.git": "https://forge.example/project.git",
		"ssh://git@valley.example/project.git":                "ssh://valley.example/project.git",
		"git@valley.example:project.git":                      "git@valley.example:project.git",
		"/srv/git/project.git":                                "/srv/git/project.git",
	} {
		if got := withoutUserinfo(remote); got != want {
			t.Errorf("%s: %s, want %s", remote, got, want)
		}
	}
}
