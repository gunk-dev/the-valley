package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// However attest verify ends while the cue it started to vet statements
// runs, that cue ends with it: the system kills it as attest dies
// (dieWithParent), and this process, the reaper of what attest leaves,
// waits for it. The cue is a real process attest started, not a stand-in
// for attest.
func TestVerifysCueEndsWithIt(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		t.Run(signal.String(), func(t *testing.T) {
			reapOrphans(t)
			r := newVerifyRig(t, "repo", true)
			fifo := filepath.Join(r.dir, "cue.pid")
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Fatal(err)
			}
			one := r.note("one", r.statement("one", r.tree, "", ""), r.worker)
			two := r.note("two", r.statement("two", r.tree, "", ""), r.worker)
			cmd := exec.Command(os.Args[0], "verify", "--repo", r.repo, "--known-keys", r.known,
				"--schema", r.schema, "--verdicts", "--note", one, "--note", two)
			cmd.Env = append(os.Environ(), "ATTEST_AS_MAIN=1", "ATTEST_CUE_STALL="+fifo,
				"PATH="+r.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			said := make(chan int, 1)
			go func() {
				data, _ := os.ReadFile(fifo)
				n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
				said <- n
			}()
			var vetting int
			select {
			case vetting = <-said:
			case err := <-exited:
				t.Fatalf("attest ended before it started cue: %v", err)
			case <-time.After(time.Minute):
				cmd.Process.Kill()
				<-exited
				t.Fatal("attest did not start cue in a minute")
			}
			cmd.Process.Signal(signal)
			<-exited
			if ws, ok := waitOrphan(t, vetting); ok && (!ws.Signaled() || ws.Signal() != syscall.SIGKILL) {
				t.Errorf("the cue ended with %v, not killed as attest died", ws)
			}
		})
	}
}

// A run whose verdicts cannot be written gives its reader no verdict:
// it exits non-zero, though every note verified.
func TestVerdictsThatCannotBeWrittenFailTheRun(t *testing.T) {
	r := newVerifyRig(t, "repo", true)
	one := r.note("one", r.statement("one", r.tree, "", ""), r.worker)
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no /dev/full to write to")
	}
	defer full.Close()
	cmd := exec.Command(os.Args[0], "verify", "--repo", r.repo, "--known-keys", r.known,
		"--schema", r.schema, "--verdicts", "--note", one)
	cmd.Env = append(os.Environ(), "ATTEST_AS_MAIN=1", "ATTEST_CUE_LOG="+r.log,
		"PATH="+r.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdout = full
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Errorf("attest verify --verdicts exited 0 with its verdicts unwritten: %q", stderr.String())
	}
}

// reapOrphans makes this process the reaper of the processes its children
// leave when they die (PR_SET_CHILD_SUBREAPER), until the test ends.
func reapOrphans(t *testing.T) {
	t.Helper()
	const setChildSubreaper = 36
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, setChildSubreaper, 1, 0); e != 0 {
		t.Fatalf("could not reap orphans: %v", e)
	}
	t.Cleanup(func() { syscall.RawSyscall(syscall.SYS_PRCTL, setChildSubreaper, 0, 0) })
}

// waitOrphan waits for pid, a process reapOrphans makes this one's, to
// end. One still running after a minute has outlived what started it: it
// is killed, and ok is false.
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
