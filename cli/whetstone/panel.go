package whetstone

import (
	"fmt"
	"os"
	"strings"

	"github.com/skael-dev/skael/internal/eval/runner"
)

// panelModelsFromEnv resolves the eval panel's model overrides, returning
// empty strings when the shipped opus/haiku default is the right one.
//
// The override is gated on ANTHROPIC_BASE_URL rather than applied whenever
// the model variables happen to be set, and the distinction matters. There
// are two independent gateways: LLM_BASE_URL is the *judge's* (a Go HTTP
// client in this process), while ANTHROPIC_BASE_URL is the *panel's* — the
// claude-code adapter forwards it into the sandbox, where the agent CLI
// dials it. Only the latter decides which model identifiers the panel's
// endpoint will accept, so only the latter is evidence that opus/haiku are
// the wrong thing to ask for.
//
// Gating this way also keeps a promise to everyone already running: someone
// who set LLM_STRONG_MODEL purely to pick a cheaper judge keeps their
// existing panel. That is not cosmetic — a changed panel is recorded in
// model_panel and splits the score trend line, so silently changing it would
// break comparability for a change the operator never asked for.
//
// baseURL is returned alongside for diagnostics, so an unhealthy panel can
// name the endpoint that rejected it.
func panelModelsFromEnv() (strong, fast, baseURL string) {
	baseURL = os.Getenv(apiBaseURLEnv)
	if baseURL == "" {
		return "", "", ""
	}
	return os.Getenv(strongModelEnv), os.Getenv(fastModelEnv), baseURL
}

// warnUnconfiguredPanelModels reports whether a custom panel gateway was
// configured without telling it which models to ask for. Empty means there is
// nothing to say.
//
// A warning rather than a refusal because it is a guess: a passthrough proxy
// in front of Anthropic resolves "opus" perfectly well, and refusing to start
// would break that setup for no reason. The health probe is the authority —
// see checkPanelHealth, which turns the real failure into something
// actionable.
//
// Note that substitution is all-or-nothing: setting only one of the two
// leaves BOTH members on the shipped aliases. Substituting one slot would be
// worse than substituting neither. A panel with one working member and one
// that 404s is not an error — it is a *complete run* that scores, reports
// PanelComplete=false, and therefore can never release the version it was
// meant to clear, having spent a full tier to get there. Keeping both members
// on the same footing means a misconfiguration fails both probes and is
// caught immediately instead.
func warnUnconfiguredPanelModels(strong, fast, baseURL string) string {
	if baseURL == "" || (strong != "" && fast != "") {
		return ""
	}
	missing := fmt.Sprintf("neither %s nor %s is", strongModelEnv, fastModelEnv)
	switch {
	case strong != "":
		missing = fmt.Sprintf("%s is set but %s is not", strongModelEnv, fastModelEnv)
	case fast != "":
		missing = fmt.Sprintf("%s is set but %s is not", fastModelEnv, strongModelEnv)
	}
	return fmt.Sprintf(
		"%s points the eval panel at %s, but %s, so the panel will ask that gateway for "+
			"Anthropic's own aliases %q and %q — both of them, since the two are set together "+
			"or not at all. A gateway that namespaces its model identifiers (OpenRouter uses "+
			"anthropic/claude-opus-4) rejects those and every panel member fails its health probe.",
		apiBaseURLEnv, baseURL, missing,
		runner.DefaultPanel()[0].Model, runner.DefaultPanel()[1].Model)
}

// checkPanelHealth fails an eval whose panel has no healthy member at all.
//
// A *partially* unhealthy panel is deliberately allowed through — see
// TestProbePanel_AnUnhealthyMemberMakesThePanelIncompleteRatherThanZero — it
// degrades to an incomplete panel rather than a zero, which is a real and
// reportable measurement.
//
// A panel where nothing is healthy is different in kind — though not for the
// reason it first looks. Very little compute is at stake: the runner already
// skips every run and probe belonging to an unhealthy member
// (internal/eval/runner/runner.go:188, 212, 229), so no task session executes.
// What happens instead is that the run walks all the way to score.Headline,
// which fails with "no panel member produced a result" — an error naming
// neither the model ids that were asked for nor the endpoint that refused
// them, which is almost always the actual cause. By then CreateEval has
// written a row that nothing ever moves out of "running", since FinishEval is
// reached only on the success path.
//
// So this does not save a wasted panel run; it converts an undiagnosable
// error at the end into a diagnosis at the start, and leaves no orphaned
// "running" eval behind.
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
