package main

import "syscall"

// noCoreDump makes this process non-dumpable, prctl(PR_SET_DUMPABLE, 0):
// it leaves no core dump, and no process of the same user may read its
// memory through ptrace or /proc, so a link's secret stays in it alone.
func noCoreDump() error {
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_DUMPABLE, 0, 0); errno != 0 {
		return errno
	}
	return nil
}
