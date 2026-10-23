//go:build windows

package seekdb

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// configureCommand starts the server without a console window and in its own
// process group, so console control events for Core do not reach it.
func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}
}
