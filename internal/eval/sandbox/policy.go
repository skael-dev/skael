package sandbox

import (
	"context"
	"errors"
	"fmt"
)

var ErrUntrustedRequiresIsolation = errors.New("sandbox: untrusted work requires a hardware-isolated driver")

// CheckPolicy fails closed: a shared-kernel driver refuses untrusted work.
func CheckPolicy(d Driver, untrusted bool) error {
	if !untrusted {
		return nil
	}
	if d.HardwareIsolated() {
		return nil
	}
	return fmt.Errorf("%w: driver %q shares the host kernel", ErrUntrustedRequiresIsolation, d.Name())
}

// Gated wraps d so the trust gate re-checks on every Run, not only at
// construction. Trusted work passes d through unchanged.
func Gated(d Driver, untrusted bool) (Driver, error) {
	if err := CheckPolicy(d, untrusted); err != nil {
		return nil, err
	}
	if !untrusted {
		return d, nil
	}
	return &gatedDriver{Driver: d}, nil
}

type gatedDriver struct{ Driver }

func (g *gatedDriver) Run(ctx context.Context, rs RunSpec) (RunResult, error) {
	if err := CheckPolicy(g.Driver, true); err != nil {
		return RunResult{}, err
	}
	return g.Driver.Run(ctx, rs)
}
