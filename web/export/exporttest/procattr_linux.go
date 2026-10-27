package exporttest

import "syscall"

// processGroup puts a command in a process group of its own, and has the
// kernel kill it when the test process dies.
func processGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
