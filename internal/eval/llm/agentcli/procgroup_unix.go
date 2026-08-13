//go:build unix

package agentcli

import (
	"os/exec"
	"syscall"
)

// setupProcessGroup places the CLI in its own process group so cancellation
// kills grandchildren too, preventing orphaned pipes from blocking Wait.
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
