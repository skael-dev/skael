package sandbox

import (
	"context"
	"errors"
	"fmt"
)

// ErrUntrustedRequiresIsolation is returned when untrusted work is offered to a
// driver that shares the host kernel.
var ErrUntrustedRequiresIsolation = errors.New("sandbox: untrusted work requires a hardware-isolated driver")

// CheckPolicy gates a driver against the trust level of the work. It fails
// closed: an unrecognised or shared-kernel driver refuses untrusted work
// rather than running it with a warning, because the blast radius of an escape
// is the worker host — its LLM credentials and every other tenant's run.
func CheckPolicy(d Driver, untrusted bool) error {
	if !untrusted {
		return nil
	}
	if d.HardwareIsolated() {
		return nil
	}
	return fmt.Errorf("%w: driver %q shares the host kernel", ErrUntrustedRequiresIsolation, d.Name())
}

// Gated wraps d so the trust decision travels with the driver rather than
// with whichever caller remembered to invoke CheckPolicy. For trusted work it
// returns d unchanged. For untrusted work on a driver that shares the host
// kernel it refuses at construction, so a caller building a driver for
// untrusted work finds out immediately rather than at the first Run; for
// untrusted work on a hardware-isolated driver it returns a wrapper whose Run
// re-checks the same policy, so the gate holds even if the driver escapes
// into a struct field or is handed to code that never heard of CheckPolicy.
func Gated(d Driver, untrusted bool) (Driver, error) {
	if err := CheckPolicy(d, untrusted); err != nil {
		return nil, err
	}
	if !untrusted {
		return d, nil
	}
	return &gatedDriver{Driver: d}, nil
}

// gatedDriver re-asserts CheckPolicy on every Run, in case the underlying
// driver's isolation status is not actually fixed for its lifetime (a fake in
// a test, for instance) or the wrapper itself was constructed some other way
// than through Gated.
type gatedDriver struct{ Driver }

func (g *gatedDriver) Run(ctx context.Context, rs RunSpec) (RunResult, error) {
	if err := CheckPolicy(g.Driver, true); err != nil {
		return RunResult{}, err
	}
	return g.Driver.Run(ctx, rs)
}
