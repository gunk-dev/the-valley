// Package nodump makes a process non-dumpable, so the secrets it holds in
// memory stay in it alone. The publisher's link command holds a new
// link's secret, and valley-web with readers holds the links readers
// post and their sessions' tokens.
package nodump

import "syscall"

// Set makes this process non-dumpable, prctl(PR_SET_DUMPABLE, 0): it
// leaves no core dump, and no process of the same user may read its
// memory through ptrace or /proc.
func Set() error {
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_DUMPABLE, 0, 0); errno != 0 {
		return errno
	}
	return nil
}
