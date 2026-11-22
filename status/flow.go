package main

// valley flow: the agent-run client (flow/), a program of its own that
// valley runs rather than carries, since valley imports nothing outside
// the standard library and this repository's other modules.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// flowHelper is the program valley flow runs.
const flowHelper = "valley-flow"

// runFlow replaces this process with valley-flow, found on PATH, given the
// arguments after flow, so the stdio, terminal, signals and exit status
// are valley-flow's own. PATH alone is searched, never this binary's
// directory: the packaged valley is a wrapper, and the helper is installed
// and updated apart from valley. The helper runs in the environment valley
// was started in (callerEnvironment), not the one valley sets up for the
// git it runs. It returns only when the helper could not start: 127 when
// PATH has no executable valley-flow (flowHelperPath), 126 when exec
// fails.
func runFlow(args []string, stderr io.Writer) int {
	helper, found := flowHelperPath()
	if !found {
		fmt.Fprintf(stderr, "valley: flow runs %s, which is not on PATH\n", flowHelper)
		return 127
	}
	err := syscall.Exec(helper, append([]string{flowHelper}, args...), callerEnvironment)
	fmt.Fprintf(stderr, "valley: %s: %s\n", helper, err)
	return 126
}

// flowHelperPath is the first executable valley-flow in PATH's absolute
// directories. A relative or empty entry, which names a directory by
// where valley happens to run, is skipped: exec.LookPath would stop there
// instead, refusing it or, under GODEBUG=execerrdot=0, running it.
func flowHelperPath() (string, bool) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		if p, err := exec.LookPath(filepath.Join(dir, flowHelper)); err == nil {
			return p, true
		}
	}
	return "", false
}
