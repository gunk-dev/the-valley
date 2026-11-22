package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
)

// helperName is the program name under which this binary is the valley
// client's flow verb: `valley flow` runs valley-flow from PATH, and the
// package installs it beside klaus as the same binary.
const helperName = "valley-flow"

var rootCmd = &cobra.Command{
	Use:   "klaus",
	Short: "Multi-agent orchestrator for Claude Code, Codex, and agy",
	Long: `flow orchestrates parallel coding agents using git worktrees and tmux panes.

It launches autonomous agents in isolated worktrees, manages their lifecycle,
streams and formats their output, and tracks run state.

Running 'valley flow' with no arguments resumes the most recent coordinator session.
If no session exists, a new one is created. Use 'valley flow new' to explicitly start
a fresh session.`,
	Version: version,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSession(cmd, false)
	},
}

func Execute() error {
	nameRoot(invokedAs(os.Args[0]))
	return rootCmd.Execute()
}

// invokedAs is the program name argv0 starts this binary under:
// helperName, or klaus for any other name.
func invokedAs(argv0 string) string {
	if filepath.Base(argv0) == helperName {
		return helperName
	}
	return "klaus"
}

// nameRoot has the root command name itself as program, which invokedAs
// gave: `valley flow` in usage, help and --version under helperName, and
// klaus otherwise.
func nameRoot(program string) {
	name := "klaus"
	if program == helperName {
		name = "valley flow"
		if rootCmd.Annotations == nil {
			rootCmd.Annotations = map[string]string{}
		}
		rootCmd.Annotations[cobra.CommandDisplayNameAnnotation] = name
	} else {
		delete(rootCmd.Annotations, cobra.CommandDisplayNameAnnotation)
	}
	rootCmd.SetVersionTemplate(fmt.Sprintf("%s version %s\n", name, version))
}

// exitError is an error that sets klaus's exit status.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// ExitCode is the exit status for an error Execute returned: the one the
// error asks for, else 1.
func ExitCode(err error) int {
	var e *exitError
	if errors.As(err, &e) {
		return e.code
	}
	return 1
}

func init() {
	nameRoot("klaus")
}
