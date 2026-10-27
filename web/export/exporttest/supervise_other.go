//go:build !linux

package exporttest

import "syscall"

// sysProcAttr puts a program in a process group of its own.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killSession kills the process group sid.
func killSession(sid int) { syscall.Kill(-sid, syscall.SIGKILL) }

// watch and unwatch do nothing here: there is no watchdog.
func watch(sid int)   {}
func unwatch(sid int) {}
