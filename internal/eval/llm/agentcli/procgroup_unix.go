//go:build unix

package agentcli

import (
	"os/exec"
	"syscall"
)

// setupProcessGroup places the CLI's process in its own process group and
// arranges for context cancellation to kill that whole group rather than
// just the direct child. A shell script's external commands run as further
// children inside the same group; killing only the direct child (what
// exec.CommandContext's default Cancel does) leaves those grandchildren
// running and holding the stdout/stderr pipes open, which blocks Wait past
// the configured Timeout.
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
