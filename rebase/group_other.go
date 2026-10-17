//go:build !unix

package rebase

import "os/exec"

// ownGroup leaves cmd as it is where there are no process groups. A
// cancelled context kills git alone.
func ownGroup(cmd *exec.Cmd) {}
