package whetstone

import (
	"fmt"
	"os"
	"strings"

	"github.com/skael-dev/skael/internal/eval/runner"
)

// panelModelsFromEnv resolves the eval panel's model overrides, returning
// empty strings when the shipped opus/haiku default is right.
//
// Gated on ANTHROPIC_BASE_URL — the *panel's* gateway, forwarded into the
// sandbox — not LLM_BASE_URL, which is the judge's. Only the panel's endpoint
// decides which model ids the panel may ask for, and gating this way means
// retuning the judge alone does not silently change the panel, which would
// split the score trend.
func panelModelsFromEnv() (strong, fast, baseURL string) {
	baseURL = os.Getenv(apiBaseURLEnv)
	if baseURL == "" {
		return "", "", ""
	}
	return os.Getenv(strongModelEnv), os.Getenv(fastModelEnv), baseURL
}

// warnUnconfiguredPanelModels reports a custom panel gateway configured
// without model ids. A warning rather than a refusal: a passthrough proxy
// resolves "opus" fine, so the health probe is the authority.
//
// Substitution is all-or-nothing — one slot substituted leaves a panel with
// one working member and one that 404s, which is not an error but a complete
// run that scores, reports PanelComplete=false, and can never release the
// version it was meant to clear.
func warnUnconfiguredPanelModels(strong, fast, baseURL string) string {
	if baseURL == "" || strong != "" {
		return ""
	}
	_ = fast // the floor member only exists at the deep tier
	return fmt.Sprintf(
		"%s points the eval panel at %s, but %s is not set, so the panel will ask that gateway for "+
			"Anthropic's own alias %q. A gateway that namespaces its model identifiers (OpenRouter uses "+
			"anthropic/claude-sonnet-5) rejects that and every panel member fails its health probe. "+
			"Set %s as well if you run the deep tier, which adds a floor member.",
		apiBaseURLEnv, baseURL, strongModelEnv,
		runner.DefaultPanel()[0].Model, fastModelEnv)
}

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
		fmt.Fprintf(&b, " (panel gateway %s=%s)", apiBaseURLEnv, baseURL)
	}
	b.WriteString(":")
	for _, h := range health {
		fmt.Fprintf(&b, "\n  %s/%s: %s", h.Member.Agent, h.Member.Model, h.Detail)
	}
	fmt.Fprintf(&b, "\nIf that gateway namespaces its model identifiers, set %s and %s to names it serves.",
		strongModelEnv, fastModelEnv)
	return fmt.Errorf("%s", b.String())
}
