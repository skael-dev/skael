package northflank

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
//
// The copy lands in a fresh directory that then replaces the workspace's
// contents, so a file the run deleted does not survive locally. A check run
// stages this copy into a fresh sandbox.
func (d *Driver) downloadWorkspace(ctx context.Context, serviceID, remote, local string) error {
	fresh, err := os.MkdirTemp(filepath.Dir(local), ".nf-download-*")
	if err != nil {
		return fmt.Errorf("northflank: preparing the download: %w", err)
	}
	defer os.RemoveAll(fresh)
	if err := d.runCLI(ctx, "download", "service", "file",
		"--projectId", d.o.Project, "--service", serviceID,
		"--localPath", fresh, "--remotePath", remote); err != nil {
		return err
	}
	return replaceContents(local, fresh)
}

// replaceContents empties dst and moves every entry of src into it.
func replaceContents(dst, src string) error {
	old, err := os.ReadDir(dst)
	if err != nil {
		return fmt.Errorf("northflank: reading the workspace: %w", err)
	}
	for _, e := range old {
		if err := os.RemoveAll(filepath.Join(dst, e.Name())); err != nil {
			return fmt.Errorf("northflank: clearing the workspace: %w", err)
		}
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("northflank: reading the download: %w", err)
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return fmt.Errorf("northflank: moving the download into the workspace: %w", err)
		}
	}
	return nil
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
