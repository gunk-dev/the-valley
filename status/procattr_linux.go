package main

import "syscall"

// dieWithParent makes a child process be killed when this process dies,
// however it dies: a signal, SIGPIPE from a reader that went away, or
// SIGKILL. It is attest's own (attest/procattr_linux.go). Linux sends the
// signal when the thread that started the child ends. Go ends a thread
// only when a goroutine locked to it ends still locked, and nothing here
// locks one, so the thread lasts as long as the process.
func dieWithParent() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
