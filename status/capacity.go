package main

// The capacity a pass builds on: one line of key=value pairs that the
// pass hands to each attest run (attest run --capacity), so that every
// check build the run records says what capacity it had. A check's build
// is compared only with builds on the same capacity (checkcost.go).
//
// The line holds what the pass is given and what nix reports to it: slots
// (--slots, or one per CPU), batches (--batches), nix's max-jobs and
// cores as nix config show reads them, and the CPU count. A setting nix
// does not report is left out.

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// measureCapacity is the capacity of a pass that builds slots checks at
// once in each batch (0 for attest's default) and batches batches:
// "slots=2 batches=2 max_jobs=2 cores=2 cpus=6".
func measureCapacity(slots, batches int) string {
	if slots == 0 {
		slots = runtime.NumCPU()
	}
	line := fmt.Sprintf("slots=%d batches=%d", slots, batches)
	for _, name := range []string{"max-jobs", "cores"} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out, err := exec.CommandContext(ctx, "nix", "--extra-experimental-features", "nix-command", "config", "show", name).Output()
		cancel()
		if n, aerr := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && aerr == nil {
			line += fmt.Sprintf(" %s=%d", strings.ReplaceAll(name, "-", "_"), n)
		}
	}
	return line + fmt.Sprintf(" cpus=%d", runtime.NumCPU())
}
