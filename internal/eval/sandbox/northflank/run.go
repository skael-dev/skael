package northflank

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/skael-dev/skael/internal/eval/sandbox"
)

// sessionDeleteTimeout bounds the cleanup delete, which runs on a context of
// its own so a cancelled run still frees the sandbox it created.
const sessionDeleteTimeout = 30 * time.Second

// minDownloadTimeout floors the workspace-download budget so a very short
// run timeout does not starve the transfer that carries its only result.
const minDownloadTimeout = time.Minute

// downloadTimeout scales the workspace-download budget with the run's own
// timeout, at a quarter of it, floored at minDownloadTimeout. There is no
// direct measurement of workspace size at this point, so the run's timeout
// is the best available proxy: a session given more time is likely to have
// produced more to copy back. Matches kubernetes/run.go's collectOutTimeout.
func downloadTimeout(runTimeout time.Duration) time.Duration {
	t := runTimeout / 4
	if t < minDownloadTimeout {
		return minDownloadTimeout
	}
	return t
}

// Run creates one sandbox service, runs argv inside it, and deletes it. The
// sandbox is deleted on every exit path: Northflank documents no idle
// auto-stop, and a paused service still bills for storage, so cleanup is a
// delete rather than a pause.
func (d *Driver) Run(ctx context.Context, rs sandbox.RunSpec) (sandbox.RunResult, error) {
	if err := rs.Validate(); err != nil {
		return sandbox.RunResult{}, err
	}
	if len(rs.Mounts) > 0 {
		return sandbox.RunResult{}, fmt.Errorf(
			"northflank: run declares %d host mounts, and a remote sandbox has no host to mount from. Supply agent credentials as environment variables instead (CLAUDE_CODE_OAUTH_TOKEN)", len(rs.Mounts))
	}
	if err := d.o.CheckNetwork(rs.Network, rs.Allow); err != nil {
		return sandbox.RunResult{}, err
	}

	id, err := d.c.createSandbox(ctx, sessionName(), rs.Env)
	if err != nil {
		return d.classify(ctx, sandbox.RunResult{}, fmt.Errorf("northflank: creating the sandbox: %w", err))
	}
	// Deletion runs on its own context: the run's ctx is often already
	// cancelled by the time this fires, and a leaked sandbox keeps billing
	// until someone notices.
	defer func() {
		delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionDeleteTimeout)
		defer cancel()
		if err := d.c.deleteSandbox(delCtx, id); err != nil {
			d.o.Logger("northflank: deleting sandbox %s: %v", id, err)
		}
	}()

	if err := d.waitRunning(ctx, id, rs.Timeout); err != nil {
		return d.classify(ctx, sandbox.RunResult{}, err)
	}

	workdir := rs.WorkDir
	if workdir == "" {
		workdir = sandbox.DefaultWorkDir
	}
	if err := d.uploadWorkspace(ctx, id, rs.Workspace, workdir); err != nil {
		return d.classify(ctx, sandbox.RunResult{}, err)
	}

	runCtx, cancel := context.WithTimeout(ctx, rs.Timeout)
	defer cancel()

	start := time.Now()
	code, execErr := d.c.execSandbox(runCtx, id, rs.Argv, rs.Stdout, rs.Stderr)
	res := sandbox.RunResult{ExitCode: code, Duration: time.Since(start)}

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		res.TimedOut = true
		return res, nil
	case ctx.Err() != nil:
		res.Cancelled = true
		return res, fmt.Errorf("northflank: run cancelled: %w", ctx.Err())
	case execErr != nil:
		return res, execErr
	}

	// The download runs on a context of its own: the run is over, and its
	// outputs are the only record of what happened. A hung transfer must not
	// hang the session forever, and a cancellation arriving during collection
	// must not destroy the only record of the run.
	dlCtx, dlCancel := context.WithTimeout(context.WithoutCancel(ctx), downloadTimeout(rs.Timeout))
	defer dlCancel()
	if err := d.downloadWorkspace(dlCtx, id, workdir, rs.Workspace); err != nil {
		return res, err
	}
	return res, nil
}

// waitRunning polls the service until its deployment completes rollout or
// the run's timeout expires.
func (d *Driver) waitRunning(ctx context.Context, id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		running, err := d.c.sandboxRunning(ctx, id)
		if err != nil {
			return err
		}
		if running {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("northflank: sandbox %s was not running after %s", id, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.waitInterval):
		}
	}
}

// classify turns a setup failure into the right RunResult. A cancelled run
// must never be recorded as a failed one.
func (d *Driver) classify(ctx context.Context, res sandbox.RunResult, err error) (sandbox.RunResult, error) {
	if ctx.Err() != nil {
		res.Cancelled = true
	}
	return res, err
}

// sessionName is the service name for one run. Northflank names must be
// unique within the project, so the id incorporates the current time.
func sessionName() string {
	return fmt.Sprintf("whetstone-run-%d", time.Now().UnixNano())
}

// Sweep removes every sandbox this driver owns, whether or not a Run is
// still tracking it. A worker calls it at startup to reclaim what a prior
// crash left running; it never returns an error, because a sweep that fails
// must not stop a worker from starting.
func (d *Driver) Sweep(ctx context.Context) {
	refs, err := d.c.listSandboxes(ctx)
	if err != nil {
		d.o.Logger("northflank: sweep: listing sandboxes: %v", err)
		return
	}
	for _, ref := range refs {
		if err := d.c.deleteSandbox(ctx, ref.ID); err != nil {
			d.o.Logger("northflank: sweep: deleting orphan %s (%s): %v", ref.Name, ref.ID, err)
			continue
		}
		d.o.Logger("northflank: sweep: deleted orphan %s (%s)", ref.Name, ref.ID)
	}
}
