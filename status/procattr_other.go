//go:build !linux

package main

import "syscall"

// dieWithParent asks nothing of a child process where the system cannot
// kill it when its parent dies. There, a telemetry request's ssh that a
// command leaves behind ends when the host ends the request, or when its
// keepalive gives up on a dead connection (docs/reference.md), and an
// attest verify ends when its verification does.
func dieWithParent() *syscall.SysProcAttr {
	return nil
}
