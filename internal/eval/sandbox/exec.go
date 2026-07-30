package sandbox

import (
	"context"
	"io"
)

// Executor adapts a Driver to agent.Exec: each call runs one command in the
// same environment, with only argv and the output writers changing.
type Executor struct {
	d    Driver
	base RunSpec
}

// NewExec returns an executor that runs commands under base. base.Argv is
// ignored — every other field, notably the network policy and the read-only
// auth mounts, is what makes the session reproducible, so it is carried
// through unchanged.
func NewExec(d Driver, base RunSpec) *Executor { return &Executor{d: d, base: base} }

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
