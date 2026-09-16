package northflank

import (
	"context"
	"fmt"
	"os/exec"
)

// execCommand is the substitution seam for tests.
var execCommand = exec.CommandContext

// uploadWorkspace copies the local workspace into the running service. There is
// no bind mount here, so the transfer goes through the CLI.
func (d *Driver) uploadWorkspace(ctx context.Context, serviceID, local, remote string) error {
	return d.runCLI(ctx, "upload", "service", "file",
		"--projectId", d.o.Project, "--service", serviceID,
		"--localPath", local, "--remotePath", remote)
}

// downloadWorkspace copies the workspace back out for grading. A failed copy is
// indistinguishable from a skill that produced nothing, so it must error rather
// than leave the workspace empty.
func (d *Driver) downloadWorkspace(ctx context.Context, serviceID, remote, local string) error {
	return d.runCLI(ctx, "download", "service", "file",
		"--projectId", d.o.Project, "--service", serviceID,
		"--localPath", local, "--remotePath", remote)
}

// cliLogin authenticates once, at construction. Northflank documents no token
// environment variable, so the only non-interactive login puts the token in an
// argument list, visible to any local user. Once at startup is the smallest
// exposure this CLI allows — do not "simplify" the token into a per-transfer
// call, which puts it in a process listing on every workspace copy.
func (d *Driver) cliLogin(ctx context.Context) error {
	return d.runCLI(ctx, "login", "-t", d.o.Token)
}

// runCLI returns an error carrying the CLI's output, or naming the binary when
// it is not on PATH.
func (d *Driver) runCLI(ctx context.Context, args ...string) error {
	cmd := execCommand(ctx, d.o.CLI, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if _, lookErr := exec.LookPath(d.o.CLI); lookErr != nil {
			return fmt.Errorf("northflank: %s: %w", d.o.CLI, lookErr)
		}
		return fmt.Errorf("northflank: %s %v: %w: %s", d.o.CLI, args, err, out)
	}
	return nil
}
