package whetstone

import (
	"fmt"
	"strings"

	"github.com/skael-dev/skael/internal/eval/provider"
	"github.com/skael-dev/skael/internal/eval/runner"
)

// checkPanelHealth fails an eval whose panel has no healthy member at all. A
// *partially* unhealthy panel is deliberately allowed through — it degrades to
// an incomplete panel, which is still a measurement.
//
// This saves little compute (the runner already skips an unhealthy member's
// sessions). It converts what would otherwise be score.Headline's "no panel
// member produced a result" — naming neither the models nor the endpoint that
// refused them — into a diagnosis, before CreateEval writes a row that nothing
// moves out of "running".
func checkPanelHealth(health []runner.Health, baseURL string) error {
	if len(health) == 0 {
		return nil
	}
	for _, h := range health {
		if h.OK {
			return nil
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "whetstone eval: every panel member failed its health probe, so this run can measure nothing")
	if baseURL != "" {
		fmt.Fprintf(&b, " (gateway %s=%s)", provider.BaseURLEnv, baseURL)
	}
	b.WriteString(":")
	for _, h := range health {
		fmt.Fprintf(&b, "\n  %s/%s: %s", h.Member.Agent, h.Member.Model, h.Detail)
	}
	fmt.Fprintf(&b, "\nIf that gateway namespaces its model identifiers, set %s to names it serves.",
		provider.ModelEnv)
	return fmt.Errorf("%s", b.String())
}
