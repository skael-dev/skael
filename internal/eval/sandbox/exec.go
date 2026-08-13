package sandbox

import (
	"context"
	"io"
)

// Executor adapts a Driver to agent.Exec.
type Executor struct {
	d    Driver
	base RunSpec
}

// NewExec returns an executor that runs commands under base. base.Argv is
// ignored; network policy and auth mounts are carried through unchanged.
func NewExec(d Driver, base RunSpec) *Executor { return &Executor{d: d, base: base} }

// Workspace is the host-side directory the session runs against.
func (e *Executor) Workspace() string { return e.base.Workspace }

// WorkDir is the container-side mount point of the workspace.
func (e *Executor) WorkDir() string {
	if e.base.WorkDir == "" {
		return DefaultWorkDir
	}
	return e.base.WorkDir
}

// Env reports the environment variables the sandbox will see.
func (e *Executor) Env() []string { return e.base.Env }

// Exec runs one command and returns its exit code.
func (e *Executor) Exec(ctx context.Context, argv []string, stdout, stderr io.Writer) (int, error) {
	rs := e.base
	rs.Argv = argv
	rs.Stdout, rs.Stderr = stdout, stderr
	res, err := e.d.Run(ctx, rs)
	if err != nil {
		return res.ExitCode, err
	}
	if res.TimedOut {
		return res.ExitCode, context.DeadlineExceeded
	}
	return res.ExitCode, nil
}
