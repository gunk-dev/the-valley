package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// However valley pending ends while its read of the worker's telemetry
// runs, the read's ssh ends with it, and the signal ends pending as it
// would end any process: SIGTERM and SIGKILL while pending waits on a
// stalled host for main's line, and SIGPIPE when the reader of its output
// has gone by the time it writes, with nothing waiting on the read. Each
// time the ssh is killed by the system as pending dies (dieWithParent),
// and this process, the reaper of what pending leaves, waits for it.
func TestPendingsSSHEndsWithIt(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		signal syscall.Signal // sent to pending, or SIGPIPE from its write
	}{
		{"SIGTERM", syscall.SIGTERM},
		{"SIGKILL", syscall.SIGKILL},
		{"SIGPIPE", syscall.SIGPIPE},
	} {
		t.Run(c.name, func(t *testing.T) {
			reapOrphans(t)
			dir := t.TempDir()
			real, err := exec.LookPath("git")
			if err != nil {
				t.Skip("no git to run")
			}
			toolsOnPath(t, dir, "git", "cue")
			pid, gate := filepath.Join(dir, "ssh.pid"), filepath.Join(dir, "ssh.gate")
			if err := syscall.Mkfifo(pid, 0o600); err != nil {
				t.Fatal(err)
			}
			// ssh is the host: it says its process ID, lets a git the
			// gate holds go on, says the answer's first line, and stalls.
			header := `{"kind":"header","host":"valley-host","format":"valley-telemetry/v1"}`
			ssh := fmt.Sprintf("#!/bin/sh\necho $$ > '%s'\n[ -p '%s' ] && echo go > '%s'\necho '%s'\nexec sleep 86398\n", pid, gate, gate, header)
			if err := os.WriteFile(filepath.Join(dir, "bin", "ssh"), []byte(ssh), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(self, "pending")
			cmd.Stdin = strings.NewReader("x")
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			if c.signal == syscall.SIGPIPE {
				// Nothing waits on the read, and pending's first write,
				// which finds no reader, comes once origin is listed. The
				// listing waits for ssh, so the read is running by then.
				sh, _ := policyProject(t, filepath.Join(dir, "origin"), 1)
				sh("config", "valley.telemetry.remote", "ssh://git@valley-host/origin.git")
				if err := syscall.Mkfifo(gate, 0o600); err != nil {
					t.Fatal(err)
				}
				held := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = ls-remote ] && [ ! -e '%s.passed' ]; then : > '%s.passed'; read go < '%s'; fi\nexec '%s' \"$@\"\n", gate, gate, gate, real)
				if err := os.WriteFile(filepath.Join(dir, "bin", "git"), []byte(held), 0o755); err != nil {
					t.Fatal(err)
				}
				r, w, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				r.Close()
				defer w.Close()
				cmd.Stdout = w
			} else {
				followProject(t, filepath.Join(dir, "origin"))
			}
			cmd.Env = append(os.Environ(), "VALLEY_TEST_AS_MAIN=1")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			said := make(chan int, 1)
			go func() {
				data, _ := os.ReadFile(pid)
				n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
				said <- n
			}()
			var read int
			select {
			case read = <-said:
			case err := <-exited:
				unheard(pid)
				t.Fatalf("pending ended before its read reached the host: %v\n%s", err, out.String())
			case <-time.After(time.Minute):
				cmd.Process.Kill()
				<-exited
				unheard(pid)
				t.Fatalf("pending's read did not reach the host in a minute\n%s", out.String())
			}
			if c.signal != syscall.SIGPIPE {
				cmd.Process.Signal(c.signal)
			}
			err = <-exited
			var ee *exec.ExitError
			if !errors.As(err, &ee) || !ee.Sys().(syscall.WaitStatus).Signaled() || ee.Sys().(syscall.WaitStatus).Signal() != c.signal {
				t.Errorf("pending ended with %v, not by %s\n%s", err, c.name, out.String())
			}
			if ws, ok := waitOrphan(t, read); ok && (!ws.Signaled() || ws.Signal() != syscall.SIGKILL) {
				t.Errorf("the read's ssh ended with %v, not killed as pending died", ws)
			}
		})
	}
}

// reapOrphans makes this process the reaper of the processes its children
// leave when they die (PR_SET_CHILD_SUBREAPER), until the test ends, so a
// test can wait for them.
func reapOrphans(t *testing.T) {
	t.Helper()
	const setChildSubreaper = 36
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, setChildSubreaper, 1, 0); e != 0 {
		t.Fatalf("could not reap orphans: %v", e)
	}
	t.Cleanup(func() { syscall.RawSyscall(syscall.SYS_PRCTL, setChildSubreaper, 0, 0) })
}

// waitOrphan waits for pid, a process reapOrphans makes this one's, to
// end, and says how it ended. One still running after a minute has
// outlived what started it: it is killed, and ok is false.
func waitOrphan(t *testing.T, pid int) (ws syscall.WaitStatus, ok bool) {
	t.Helper()
	ended := make(chan syscall.WaitStatus, 1)
	go func() {
		var ws syscall.WaitStatus
		syscall.Wait4(pid, &ws, 0, nil)
		ended <- ws
	}()
	select {
	case ws := <-ended:
		return ws, true
	case <-time.After(time.Minute):
		syscall.Kill(pid, syscall.SIGKILL)
		<-ended
		t.Errorf("process %d outlived what started it", pid)
		return 0, false
	}
}
