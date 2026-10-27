package exporttest

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// waitDelay is how long a program a test runs may hold its output open
// once it has exited or been killed, as exec.Cmd.WaitDelay bounds it.
const waitDelay = 5 * time.Second

// Start starts cmd, a program a test runs, so that no process of it
// outlives the test. cmd runs in a session of its own, which every process
// it starts stays in unless it leaves the session. Where the kernel has no
// sessions to list, cmd runs in a process group of its own instead.
//
// Start returns kill, which kills every process in the session, even once
// cmd has exited, and registers a cleanup that calls it when the test
// ends. If the test process dies first, as on a timeout or an external
// kill, the kernel kills cmd, and on Linux a watchdog kills the rest of
// the session (see watchdog).
func Start(t testing.TB, cmd *exec.Cmd) (kill func(), err error) {
	cmd.SysProcAttr = sysProcAttr()
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = waitDelay
	}
	if err := cmd.Start(); err != nil {
		return func() {}, err
	}
	sid := cmd.Process.Pid
	watch(sid)
	kill = func() { killSession(sid) }
	t.Cleanup(func() {
		kill()
		unwatch(sid)
	})
	return kill, nil
}

// Run runs cmd as Start does and waits for it, for at most d, and then
// kills whatever cmd left running in its session. A cmd that has not
// exited within d is killed, with its session, and fails the test. Run
// returns cmd's error, as cmd.Run does.
func Run(t testing.TB, cmd *exec.Cmd, d time.Duration) error {
	t.Helper()
	kill, err := Start(t, cmd)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		kill()
		return err
	case <-time.After(d):
	}
	// Once the session is killed, nothing holds cmd's output open but a
	// process that left it, and cmd.WaitDelay bounds that.
	kill()
	<-done
	t.Fatalf("%s did not exit within %v", strings.Join(cmd.Args, " "), d)
	return nil
}
