// Package-internal because checkPanelHealth is unexported. Most of this
// package's tests are whetstone_test; see cli/client for the other precedent.
package whetstone

import (
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/provider"
	"github.com/skael-dev/skael/internal/eval/runner"
)

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
			"https://openrouter.ai/api", provider.ModelEnv,
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
		if strings.Contains(err.Error(), provider.BaseURLEnv) {
			t.Errorf("named a gateway that was never configured: %v", err)
		}
	})
}
