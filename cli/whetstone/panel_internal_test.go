// Package-internal because panelModelsFromEnv, warnUnconfiguredPanelModels
// and checkPanelHealth are unexported. Most of this package's tests are
// whetstone_test; see cli/client for the other precedent.
package whetstone

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/runner"
)

func TestPanelModelsFromEnv(t *testing.T) {
	tests := []struct {
		name                         string
		baseURL, strongEnv, fastEnv  string
		wantStrong, wantFast, wantBU string
	}{
		{
			// The backward-compatibility guarantee. Someone who set
			// LLM_STRONG_MODEL only to pick a cheaper judge keeps the panel
			// they already had — a changed panel splits the score trend.
			name:      "model vars alone do not touch the panel",
			strongEnv: "anthropic/claude-opus-4", fastEnv: "anthropic/claude-3.5-haiku",
		},
		{
			name:      "a custom gateway with both models overrides the panel",
			baseURL:   "https://openrouter.ai/api",
			strongEnv: "anthropic/claude-opus-4", fastEnv: "anthropic/claude-3.5-haiku",
			wantStrong: "anthropic/claude-opus-4", wantFast: "anthropic/claude-3.5-haiku",
			wantBU: "https://openrouter.ai/api",
		},
		{
			// The base URL still comes back so an unhealthy panel can name
			// the endpoint, even though there is nothing to substitute.
			name:    "a custom gateway with no models still reports the gateway",
			baseURL: "https://openrouter.ai/api",
			wantBU:  "https://openrouter.ai/api",
		},
		{name: "nothing set"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(apiBaseURLEnv, tc.baseURL)
			t.Setenv(strongModelEnv, tc.strongEnv)
			t.Setenv(fastModelEnv, tc.fastEnv)

			strong, fast, baseURL := panelModelsFromEnv()
			if strong != tc.wantStrong || fast != tc.wantFast || baseURL != tc.wantBU {
				t.Errorf("panelModelsFromEnv() = (%q, %q, %q), want (%q, %q, %q)",
					strong, fast, baseURL, tc.wantStrong, tc.wantFast, tc.wantBU)
			}
		})
	}
}

func TestWarnUnconfiguredPanelModels(t *testing.T) {
	const bu = "https://openrouter.ai/api"

	t.Run("silent when there is nothing wrong", func(t *testing.T) {
		for _, tc := range []struct{ name, strong, fast, baseURL string }{
			{name: "nothing set"},
			{name: "gateway and both models", strong: "a", fast: "b", baseURL: bu},
			// The ordinary metered worker: models set for the judge, no
			// custom panel gateway. Warning here would fire on every start.
			{name: "models but no gateway", strong: "a", fast: "b"},
		} {
			if got := warnUnconfiguredPanelModels(tc.strong, tc.fast, tc.baseURL); got != "" {
				t.Errorf("%s: warned when it should not have: %s", tc.name, got)
			}
		}
	})

	// An operator can only act on a warning that names the variable to set.
	t.Run("names what is missing", func(t *testing.T) {
		for _, tc := range []struct {
			name, strong, fast string
			wantNamed          []string
		}{
			{name: "neither set", wantNamed: []string{strongModelEnv, fastModelEnv}},
			{name: "only strong set", strong: "a", wantNamed: []string{fastModelEnv}},
			{name: "only fast set", fast: "b", wantNamed: []string{strongModelEnv}},
		} {
			got := warnUnconfiguredPanelModels(tc.strong, tc.fast, bu)
			if got == "" {
				t.Fatalf("%s: did not warn", tc.name)
			}
			for _, want := range append(tc.wantNamed, apiBaseURLEnv, bu) {
				if !strings.Contains(got, want) {
					t.Errorf("%s: warning does not name %q: %s", tc.name, want, got)
				}
			}
		}
	})
}

func TestCheckPanelHealth(t *testing.T) {
	strong := runner.Member{Agent: "claude-code", Model: "opus"}
	floor := runner.Member{Agent: "claude-code", Model: "haiku"}

	t.Run("an empty probe result is not a failure", func(t *testing.T) {
		// Vacuously "no member is OK" — must not read as a refusal.
		if err := checkPanelHealth(nil, ""); err != nil {
			t.Errorf("empty health refused the run: %v", err)
		}
	})

	// The guard for the existing degrade-to-incomplete behaviour: see
	// TestProbePanel_AnUnhealthyMemberMakesThePanelIncompleteRatherThanZero.
	t.Run("a partially healthy panel still runs", func(t *testing.T) {
		health := []runner.Health{
			{Member: strong, OK: true},
			{Member: floor, OK: false, Detail: "404 no endpoints found"},
		}
		if err := checkPanelHealth(health, "https://openrouter.ai/api"); err != nil {
			t.Errorf("a partially healthy panel was refused, which turns an incomplete "+
				"panel into a failed run: %v", err)
		}
	})

	t.Run("an all-unhealthy panel fails and says why", func(t *testing.T) {
		health := []runner.Health{
			{Member: strong, OK: false, Detail: "404 no endpoints found"},
			{Member: floor, OK: false, Detail: "404 no endpoints found"},
		}
		err := checkPanelHealth(health, "https://openrouter.ai/api")
		if err == nil {
			t.Fatal("an all-unhealthy panel was allowed to run")
		}
		// The three facts that separate "wrong model id for this gateway"
		// from "expired credentials": the models, the reason, the endpoint.
		for _, want := range []string{
			"opus", "haiku", "404 no endpoints found",
			"https://openrouter.ai/api", strongModelEnv, fastModelEnv,
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error does not mention %q: %v", want, err)
			}
		}
	})

	t.Run("omits the gateway clause when there is no custom gateway", func(t *testing.T) {
		health := []runner.Health{{Member: strong, OK: false, Detail: "expired token"}}
		err := checkPanelHealth(health, "")
		if err == nil {
			t.Fatal("an all-unhealthy panel was allowed to run")
		}
		if strings.Contains(err.Error(), apiBaseURLEnv) {
			t.Errorf("named a panel gateway that was never configured: %v", err)
		}
	})
}
