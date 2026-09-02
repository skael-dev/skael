package northflank

import (
	"context"
	"fmt"
	"os/exec"
)

// execCommand is the substitution seam for tests.
var execCommand = exec.CommandContext

// uploadWorkspace copies the session's local workspace directory into the
// running service. There is no pod-style bind mount here, so the transfer
// goes through the CLI, the way Northflank documents directory copy.
func (d *Driver) uploadWorkspace(ctx context.Context, serviceID, local, remote string) error {
	return d.runCLI(ctx, "upload", "service", "file",
		"--projectId", d.o.Project, "--service", serviceID,
		"--localPath", local, "--remotePath", remote)
}

// downloadWorkspace copies the workspace back out of the service once the
// session has finished, so its output can be graded. A failed copy back is
// indistinguishable from a skill that produced nothing, so this must return
// an error rather than silently leave the workspace empty.
func (d *Driver) downloadWorkspace(ctx context.Context, serviceID, remote, local string) error {
	return d.runCLI(ctx, "download", "service", "file",
		"--projectId", d.o.Project, "--service", serviceID,
		"--localPath", local, "--remotePath", remote)
}

// cliLogin authenticates the CLI once, at construction. Northflank documents
// no API-token environment variable; the only non-interactive login is
// `northflank login -t <TOKEN>`, which necessarily places the token in the
// child process's argument list, visible to any local user via a process
// listing. One login at worker startup is the smallest exposure this CLI
// allows, which is why uploadWorkspace and downloadWorkspace never carry the
// token: doing that instead would put it in a process listing on every
// workspace copy rather than once at startup. Do not "simplify" the token
// back into a per-transfer call.
func (d *Driver) cliLogin(ctx context.Context) error {
	return d.runCLI(ctx, "login", "-t", d.o.Token)
}

// runCLI shells out to the Northflank CLI and returns an error carrying its
// combined output on a non-zero exit, or naming the CLI binary when it is
// not on PATH at all.
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
