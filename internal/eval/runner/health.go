package runner

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/sandbox"
)

// healthProbePrompt is a trivial request: a member is healthy only if its
// adapter can complete a full round trip through the CLI and its own parser.
const healthProbePrompt = "Reply with the single word: ready."

// healthProbeTimeout is short on purpose — a health probe is meant to fail
// fast on a churned CLI or an expired token, not to wait out a real session.
const healthProbeTimeout = 2 * time.Minute

// ProbePanel runs one trivial session against every panel member and reports
// whether each came back healthy. A member is healthy only when both the
// invocation and the parse succeed: an adapter that invokes and cannot parse
// produces a trajectory of nothing, which would otherwise score as a session
// in which the agent did nothing.
func (r *Runner) ProbePanel(ctx context.Context, p Panel, image sandbox.ImageRef) ([]Health, error) {
	out := make([]Health, 0, len(p))
	for _, m := range p {
		out = append(out, r.probeMember(ctx, m, image))
	}
	return out, nil
}

func (r *Runner) probeMember(ctx context.Context, m Member, image sandbox.ImageRef) Health {
	a, ok := r.o.Adapters(m.Agent)
	if !ok {
		return Health{Member: m, OK: false, Detail: fmt.Sprintf("no adapter registered for %q", m.Agent)}
	}

	ws, err := stageProbeWorkspace()
	if err != nil {
		return Health{Member: m, OK: false, Detail: fmt.Sprintf("staging probe workspace: %v", err)}
	}
	defer func() {
		if rmErr := os.RemoveAll(ws); rmErr != nil {
			r.o.Logger("runner: removing health-probe workspace %s: %v", ws, rmErr)
		}
	}()

	// The probe authenticates exactly the way a real session does. It used to
	// mount credential directories and forward nothing, so a worker configured
	// with environment credentials failed every probe — marking every panel
	// member unhealthy and turning every evaluation into an incomplete panel,
	// with no indication that authentication was the cause.
	mounts, authVars, err := resolveAuth(a, r.o.Logger)
	if err != nil {
		return Health{Member: m, OK: false, Detail: err.Error()}
	}

	exec := sandbox.NewExec(r.o.Driver, sandbox.RunSpec{
		Image:     image,
		Workspace: ws,
		Mounts:    mounts,
		Env:       authVars,
		Network:   sandbox.NetAllowlist,
		Allow:     allowWith(r.o.AllowDomains, gatewayHosts(authVars)),
		Timeout:   healthProbeTimeout,
	})

	stream, err := a.Invoke(ctx, agent.InvokeSpec{
		Prompt: healthProbePrompt,
		Model:  m.Model,
		Exec:   exec,
	})
	if err != nil {
		return Health{Member: m, OK: false, Detail: fmt.Sprintf("invoke: %v", err)}
	}
	if _, err := a.Parse(stream); err != nil {
		return Health{Member: m, OK: false, Detail: fmt.Sprintf("parse: %v", err)}
	}
	return Health{Member: m, OK: true}
}

// memberHealthy reports whether m may run. A member absent from h is treated
// as healthy — Healthy is only ever populated from a prior ProbePanel call,
// and an eval run without one (e.g. a resumed run whose caller re-probes
// separately) must not silently skip every session.
func memberHealthy(h map[Member]bool, m Member) bool {
	ok, known := h[m]
	if !known {
		return true
	}
	return ok
}
