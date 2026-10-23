//go:build !windows

package seekdb

import (
	"os/exec"
	"syscall"
)

// configureCommand keeps the server out of the terminal's process group, so a
// Ctrl+C meant for Core does not reach it; it stops when Core lets go of the
// clients lock.
func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
