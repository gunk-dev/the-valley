//go:build !unix

package composed

import "os/exec"

// ownGroup leaves cmd as it is where there are no process groups. A done
// context kills the program alone.
func ownGroup(cmd *exec.Cmd) {}
