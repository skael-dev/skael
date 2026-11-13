//go:build windows

package hooks

import (
	"os/exec"
	"syscall"
)

const (
	detachedProcess        = 0x00000008
	createBreakawayFromJob = 0x01000000
	createNoWindow         = 0x08000000
)

// detach also leaves the agent's job object, so the agent ending the hook's
// job does not kill the POST. A job that forbids breakaway refuses the start,
// and detachFallback retries without it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP | createBreakawayFromJob,
		HideWindow:    true,
	}
}

func detachFallback(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
}
