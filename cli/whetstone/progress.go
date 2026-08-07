package whetstone

import (
	"context"
	"time"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/ui"
)

// progressGateway wraps a Gateway to print a line before and after every
// call. Authoring is otherwise silent between passes — a long one (suite
// drafting, or now a resources-pass file) can run for minutes with nothing
// printed, which reads as a hang rather than progress. ui.Info no-ops under
// ui.JSONMode, so --json output needs no extra plumbing.
type progressGateway struct{ inner llm.Gateway }

// Complete implements llm.Gateway.
func (g *progressGateway) Complete(ctx context.Context, r llm.Req) (llm.Res, error) {
	ui.Info("%s…", r.Role)
	start := time.Now()
	res, err := g.inner.Complete(ctx, r)
	if err != nil {
		return res, err
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	if res.Cached {
		ui.Info("%s done in %s (cached)", r.Role, elapsed)
	} else {
		ui.Info("%s done in %s", r.Role, elapsed)
	}
	return res, nil
}

// ModelFor implements llm.Gateway.
func (g *progressGateway) ModelFor(c llm.ModelClass) string {
	return g.inner.ModelFor(c)
}
