package sandbox

import (
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
