package whetstone_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/eval/runner"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// panelOf reads back the model panel the eval was actually planned with. The
// stored column is the authority — it is what the report is keyed on and what
// --resume compares against — so asserting on it proves the configured ids
// reached the panel rather than merely being stored on a struct.
func panelOf(t *testing.T, d whetstone.EvalDeps, skill string) runner.Panel {
	t.Helper()
	rec, err := d.Store.LatestEval(skill)
	if err != nil {
		t.Fatalf("no eval row for %s: %v", skill, err)
	}
	var p runner.Panel
	if err := json.Unmarshal(rec.ModelPanel, &p); err != nil {
		t.Fatalf("model_panel does not decode: %v", err)
	}
	return p
}

// The production path. Every automatic enqueue — publish and import both —
// submits an empty panel, so if the configured ids did not land here they
// would never land anywhere in production.
func TestRunEvalWith_UsesConfiguredPanelModelsWhenNoPanelRequested(t *testing.T) {
	d, req := evalHarness(t)
	d.PanelStrongModel = "anthropic/claude-opus-4"
	d.PanelFastModel = "anthropic/claude-3.5-haiku"
	d.PanelBaseURL = "https://openrouter.ai/api"

	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err != nil {
		t.Fatal(err)
	}

	p := panelOf(t, d, req.Skill)
	if len(p) != 2 {
		t.Fatalf("panel has %d members, want 2", len(p))
	}
	if p[0].Model != "anthropic/claude-opus-4" || p[1].Model != "anthropic/claude-3.5-haiku" {
		t.Errorf("panel models = %q/%q, want the configured ids", p[0].Model, p[1].Model)
	}
	// Class assignment is positional in ParsePanel; the strong member must
	// still come first or the panel minimum and RobustnessGap invert.
	if p[0].Class != spec.TierStrong || p[1].Class != spec.TierFloor {
		t.Errorf("panel classes = %q/%q, want strong then floor", p[0].Class, p[1].Class)
	}
	// A gateway that namespaces its ids never resolves the bare aliases.
	for _, m := range p {
		if m.Model == "opus" || m.Model == "haiku" {
			t.Errorf("panel still carries the shipped alias %q", m.Model)
		}
	}
}

// The backward-compatibility guarantee, asserted end to end rather than only
// in the resolver: an unconfigured deps struct must reproduce today's panel.
func TestRunEvalWith_UnconfiguredDepsKeepTheShippedPanel(t *testing.T) {
	d, req := evalHarness(t)

	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err != nil {
		t.Fatal(err)
	}

	p := panelOf(t, d, req.Skill)
	want := runner.DefaultPanel()
	if len(p) != len(want) {
		t.Fatalf("panel has %d members, want %d", len(p), len(want))
	}
	for i := range want {
		if p[i].Agent != want[i].Agent || p[i].Model != want[i].Model {
			t.Errorf("member %d = %s/%s, want %s/%s", i,
				p[i].Agent, p[i].Model, want[i].Agent, want[i].Model)
		}
	}
}

// An operator who names a panel is stating it. A flag that quietly meant
// something else would be worse than no flag.
func TestRunEvalWith_ExplicitPanelBeatsConfiguredDefaults(t *testing.T) {
	d, req := evalHarness(t)
	d.PanelStrongModel = "anthropic/claude-opus-4"
	d.PanelFastModel = "anthropic/claude-3.5-haiku"
	d.PanelBaseURL = "https://openrouter.ai/api"
	req.Agents = []string{"claude-code"}
	req.Models = []string{"opus", "haiku"}

	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err != nil {
		t.Fatal(err)
	}

	p := panelOf(t, d, req.Skill)
	if p[0].Model != "opus" || p[1].Model != "haiku" {
		t.Errorf("panel models = %q/%q, want the explicitly requested ones", p[0].Model, p[1].Model)
	}
}

// Only one of the two configured is a misconfiguration, and substituting a
// single slot would produce a run that scores, reports PanelComplete=false,
// and can never release the version it was meant to clear. Both members stay
// on the shipped aliases so the failure is caught by the health probe instead.
func TestRunEvalWith_PartialPanelConfigurationSubstitutesNeitherSlot(t *testing.T) {
	d, req := evalHarness(t)
	d.PanelStrongModel = "anthropic/claude-opus-4"
	d.PanelBaseURL = "https://openrouter.ai/api"

	if _, err := whetstone.RunEvalWith(context.Background(), d, req); err != nil {
		t.Fatal(err)
	}

	p := panelOf(t, d, req.Skill)
	for _, m := range p {
		if strings.Contains(m.Model, "/") {
			t.Errorf("half-configured panel substituted %q; one working member and one "+
				"404ing member is a complete run that can never clear a hold", m.Model)
		}
	}
}
