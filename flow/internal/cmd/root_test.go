package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// execRoot runs the root command with args and returns what it printed. The
// flags a run sets stay set on cobra's commands, so it clears the help and
// version flags of the command it ran afterwards.
func execRoot(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	ran, err := rootCmd.ExecuteC()
	for _, name := range []string{"help", "version"} {
		if f := ran.Flags().Lookup(name); f != nil {
			_ = f.Value.Set("false")
			f.Changed = false
		}
	}
	rootCmd.SetOut(nil)
	rootCmd.SetErr(nil)
	rootCmd.SetArgs(nil)
	if err != nil {
		t.Fatalf("%s: %v", strings.Join(args, " "), err)
	}
	return out.String()
}

// The root command names itself by the program name it runs under:
// `valley flow` as valley-flow, wherever that is, and klaus under any other
// name, with klaus's --version line exactly as its template has always
// printed it.
func TestRootNamesItselfByInvocation(t *testing.T) {
	t.Cleanup(func() { nameRoot("klaus") })
	for _, tc := range []struct{ argv0, name string }{
		{"valley-flow", "valley flow"},
		{"/nix/store/0000000000000000000000000000000-klaus-0.1.0/bin/valley-flow", "valley flow"},
		{"klaus", "klaus"},
		{"/home/operator/.nix-profile/bin/klaus", "klaus"},
		{"./valley-flow-dev", "klaus"},
	} {
		nameRoot(invokedAs(tc.argv0))
		if got, want := execRoot(t, "--version"), tc.name+" version "+version+"\n"; got != want {
			t.Errorf("%s --version: printed %q, want %q", tc.argv0, got, want)
		}
		help := execRoot(t, "--help")
		for _, want := range []string{"\n  " + tc.name + " [command]\n", `Use "` + tc.name + ` [command] --help"`} {
			if !strings.Contains(help, want) {
				t.Errorf("%s --help: no %q in:\n%s", tc.argv0, want, help)
			}
		}
		if sub := execRoot(t, "status", "--help"); !strings.Contains(sub, "\n  "+tc.name+" status") {
			t.Errorf("%s status --help: no usage line naming %q in:\n%s", tc.argv0, tc.name+" status", sub)
		}
	}
}
