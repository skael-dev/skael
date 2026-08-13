package provider_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/provider"
)

// gatewayWithSubscription is the combination that selects the split: a
// gateway for the judge, and a subscription token for the panel.
func gatewayWithSubscription() map[string]string {
	return map[string]string{
		provider.BaseURLEnv:    "https://openrouter.ai/api",
		provider.AuthTokenEnv:  "sk-or-test",
		provider.ModelEnv:      "anthropic/claude-sonnet-5,anthropic/claude-haiku-4.5",
		provider.OAuthTokenEnv: "sk-ant-oat-test",
	}
}

// TestResolve_AGatewayPlusASubscriptionTokenSplitsTheJudgeFromThePanel pins
// the mode itself. The judge keeps the gateway, because a published score
// must come from a metered backend. The panel does not, because the operator
// stated a subscription for it by setting the token at all.
func TestResolve_AGatewayPlusASubscriptionTokenSplitsTheJudgeFromThePanel(t *testing.T) {
	c := provider.Resolve(envOf(gatewayWithSubscription()), nil)

	if c.Kind != provider.KindAPI {
		t.Fatalf("Kind = %q, want the judge still served by the gateway", c.Kind)
	}
	if !c.PanelSubscription {
		t.Fatal("PanelSubscription = false, want the panel routed to the subscription")
	}
	if c.Key != "sk-or-test" {
		t.Errorf("Key = %q, want the gateway credential", c.Key)
	}
}

// The panel asks a subscription for the alias a subscription serves. Handing
// it the gateway's namespaced ids would 404 every member, which is not an
// error but a complete run reporting an incomplete panel.
func TestPanelModels_IsEmptyWhenThePanelRunsOnTheSubscription(t *testing.T) {
	c := provider.Resolve(envOf(gatewayWithSubscription()), nil)

	if got := c.PanelModels(); got != nil {
		t.Errorf("PanelModels() = %v, want nil so the shipped alias is used", got)
	}
}

// PanelExcludeEnv is the load-bearing half. The claude-code adapter forwards
// every credential name it declares, so without withholding these the sandbox
// still finds the gateway and the panel follows the judge onto it — the exact
// failure this mode exists to remove.
func TestPanelExcludeEnv_WithholdsTheJudgesGatewayFromTheSandbox(t *testing.T) {
	c := provider.Resolve(envOf(gatewayWithSubscription()), nil)

	got := c.PanelExcludeEnv()
	for _, name := range []string{provider.BaseURLEnv, provider.AuthTokenEnv, provider.APIKeyEnv} {
		if !slices.Contains(got, name) {
			t.Errorf("PanelExcludeEnv() = %v, want it to withhold %s", got, name)
		}
	}
	if slices.Contains(got, provider.OAuthTokenEnv) {
		t.Errorf("PanelExcludeEnv() = %v, must not withhold the panel's own credential", got)
	}
}

// Without the subscription token nothing changes. A gateway alone still
// serves both halves, which is the setup most operators run and the one a
// regression here would break silently.
func TestPanelSplit_DoesNotEngageOnAGatewayAlone(t *testing.T) {
	env := gatewayWithSubscription()
	delete(env, provider.OAuthTokenEnv)

	c := provider.Resolve(envOf(env), nil)

	if c.PanelSubscription {
		t.Error("PanelSubscription = true with no subscription token set")
	}
	if got := c.PanelModels(); len(got) != 2 {
		t.Errorf("PanelModels() = %v, want the gateway's own ids", got)
	}
	if got := c.PanelExcludeEnv(); got != nil {
		t.Errorf("PanelExcludeEnv() = %v, want nothing withheld", got)
	}
}

// A subscription token without a gateway is not this mode. Anthropic's own
// API serves the judge and the shipped panel already runs on the token, so
// withholding anything would only break the panel's own credential path.
func TestPanelSplit_DoesNotEngageWithoutAGateway(t *testing.T) {
	c := provider.Resolve(envOf(map[string]string{
		provider.APIKeyEnv:     "sk-ant-test",
		provider.OAuthTokenEnv: "sk-ant-oat-test",
	}), nil)

	if c.PanelSubscription {
		t.Error("PanelSubscription = true with no gateway configured")
	}
	if got := c.PanelExcludeEnv(); got != nil {
		t.Errorf("PanelExcludeEnv() = %v, want nothing withheld", got)
	}
}

// The split is a billing and provenance change, so it has to be legible in
// the one line `whetstone doctor` and the worker's startup both print.
func TestDetail_NamesTheSplitSoDoctorAndTheWorkerReportIt(t *testing.T) {
	c := provider.Resolve(envOf(gatewayWithSubscription()), nil)

	if !strings.Contains(c.Detail, provider.OAuthTokenEnv) {
		t.Errorf("Detail = %q, want it to name %s", c.Detail, provider.OAuthTokenEnv)
	}
}
