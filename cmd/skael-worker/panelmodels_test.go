package main

import "testing"

// The panel and the judge have separate gateways, and wiring the panel to the
// judge's is the one mistake this whole change invites. LLM_BASE_URL is the
// judge's (a Go HTTP client in this process); ANTHROPIC_BASE_URL is the
// panel's (forwarded into the sandbox, where the agent CLI dials it). Only
// the latter says anything about which model ids the panel may ask for.
func TestPanelModels(t *testing.T) {
	const (
		strong = "anthropic/claude-opus-4"
		fast   = "anthropic/claude-3.5-haiku"
		panelG = "https://openrouter.ai/api"
		judgeG = "https://judge.example/api"
	)

	tests := []struct {
		name                 string
		cfg                  workerConfig
		wantStrong, wantFast string
	}{
		{
			// The backward-compatibility guarantee. Retuning the judge alone
			// must not change the panel: a changed panel is recorded in
			// model_panel and splits the score trend line.
			name: "judge models alone leave the panel shipped",
			cfg: workerConfig{
				LLMBaseURL: judgeG, LLMStrongModel: strong, LLMFastModel: fast,
			},
		},
		{
			name: "a custom panel gateway adopts the configured models",
			cfg: workerConfig{
				PanelBaseURL: panelG, LLMStrongModel: strong, LLMFastModel: fast,
			},
			wantStrong: strong, wantFast: fast,
		},
		{
			// The confusable case, stated explicitly: a judge pointed
			// elsewhere while the panel still talks to Anthropic directly.
			name: "the judge's gateway is not the panel's",
			cfg: workerConfig{
				LLMBaseURL: judgeG, PanelBaseURL: "", LLMStrongModel: strong, LLMFastModel: fast,
			},
		},
		{name: "nothing configured"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotStrong, gotFast := panelModels(tc.cfg)
			if gotStrong != tc.wantStrong || gotFast != tc.wantFast {
				t.Errorf("panelModels() = (%q, %q), want (%q, %q)",
					gotStrong, gotFast, tc.wantStrong, tc.wantFast)
			}
		})
	}
}

// ANTHROPIC_BASE_URL must land in its own field rather than being confused
// with the judge's LLM_BASE_URL, which is read from a different variable.
func TestConfigFromEnv_ReadsThePanelGatewaySeparatelyFromTheJudges(t *testing.T) {
	t.Setenv("SKAEL_ENDPOINT", "http://x")
	t.Setenv("SKAEL_API_KEY", "k")
	t.Setenv("ANTHROPIC_API_KEY", "a")
	t.Setenv("ANTHROPIC_BASE_URL", "https://panel.example/api")
	t.Setenv("LLM_BASE_URL", "https://judge.example/api")

	cfg, err := configFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PanelBaseURL != "https://panel.example/api" {
		t.Errorf("PanelBaseURL = %q, want the ANTHROPIC_BASE_URL value", cfg.PanelBaseURL)
	}
	if cfg.LLMBaseURL != "https://judge.example/api" {
		t.Errorf("LLMBaseURL = %q, want the LLM_BASE_URL value", cfg.LLMBaseURL)
	}
}
