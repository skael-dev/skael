//go:build !unix

package agentcli

import "os/exec"

// setupProcessGroup is a no-op on non-POSIX platforms. WaitDelay still
// bounds how long Complete can block.
func setupProcessGroup(*exec.Cmd) {}
