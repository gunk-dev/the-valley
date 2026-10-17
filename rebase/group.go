//go:build unix

package rebase

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ownGroup runs cmd in a process group of its own, and has a cancelled
// context kill the whole group.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
