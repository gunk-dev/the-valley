//go:build !linux

package main

import "syscall"

// dieWithParent asks nothing of a child process where the system cannot
// kill it when its parent dies.
func dieWithParent() *syscall.SysProcAttr {
	return nil
}
