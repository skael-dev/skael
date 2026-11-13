//go:build !windows

package hooks

import (
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func detachFallback(cmd *exec.Cmd) {}

func hideWindow(cmd *exec.Cmd) {}
