//go:build !linux

package exporttest

import "syscall"

// processGroup puts a command in a process group of its own.
func processGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
