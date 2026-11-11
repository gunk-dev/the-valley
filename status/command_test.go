package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A record names the verb, a known subverb and known flags, and no value:
// not a flag's, not a positional one, not one shaped like a flag, and
// nothing after --.
func TestCommandWordsKeepNoValue(t *testing.T) {
	verb, sub, flags := commandWords([]string{"knot", "comment", "oc-1", "-m", "zx81q", "--body=zx81q", "--zx81q", "--", "--json"})
	if verb != "knot" || sub != "comment" || !slices.Equal(flags, []string{"-m", "--body"}) {
		t.Fatalf("got %q %q %q", verb, sub, flags)
	}
	if verb, sub, _ := commandWords([]string{"zx81q", "show"}); verb != "unknown" || sub != "" {
		t.Fatalf("an unknown verb read as %q %q", verb, sub)
	}
	if verb, _, flags := commandWords([]string{"walk", "-v", "--repo", "app"}); verb != "review" || !slices.Equal(flags, []string{"-v", "--repo"}) {
		t.Fatalf("review's walk read as %q %q", verb, flags)
	}
}

// Telemetry is off without asking git only where no configuration file
// mentions it or includes another.
func TestTelemetryMentioned(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Chdir(repo)
	if !telemetryMentioned(git{env: os.Environ()}) {
		t.Fatal("outside a repository, git must answer")
	}
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("[core]\n\tbare = false\n"), 0o644)
	if telemetryMentioned(git{env: os.Environ()}) {
		t.Fatal("no file mentions telemetry, yet git would be asked")
	}
	for _, text := range []string{"[valley]\n\tTelemetry = true\n", "[include]\n\tpath = elsewhere\n"} {
		os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(text), 0o644)
		if !telemetryMentioned(git{env: os.Environ()}) {
			t.Fatalf("%q is not read as a possible yes", text)
		}
	}
	// A walk's step asks about its own repository, wherever the process is.
	os.Remove(filepath.Join(home, ".gitconfig"))
	step := t.TempDir()
	os.MkdirAll(filepath.Join(step, ".git"), 0o755)
	os.WriteFile(filepath.Join(step, ".git", "config"), []byte("[valley]\n\ttelemetry = true\n"), 0o644)
	if !telemetryMentioned(git{env: os.Environ(), dir: step}) {
		t.Fatal("the step's repository mentions telemetry, and it was not read")
	}
	if telemetryMentioned(git{env: os.Environ()}) {
		t.Fatal("the working directory's repository mentions no telemetry, yet git would be asked")
	}
}
