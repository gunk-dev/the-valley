package main

import "syscall"

// dieWithParent makes a child process be killed when attest dies, however
// it dies, so an evaluation never outlives the run that admitted it.
func dieWithParent() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
