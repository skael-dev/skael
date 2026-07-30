//go:build !unix

package agentcli

import "os/exec"

// setupProcessGroup is a no-op on platforms without POSIX process groups
// (e.g. Windows). WaitDelay (set alongside this call) still bounds how long
// Complete can block once the context is cancelled or the process exits,
// even though an orphaned grandchild is not force-killed here.
func setupProcessGroup(*exec.Cmd) {}
