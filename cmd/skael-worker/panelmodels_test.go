package main

import (
	"reflect"
	"testing"
)

// The judge and the panel now share one gateway variable, ANTHROPIC_BASE_URL.
// What survives from the two-variable era is the rule that decides whether the
// panel adopts the configured models at all: only a custom gateway does.
// Naming a model to pick a cheaper judge against Anthropic's own API must
// leave the panel alone, because a changed panel is recorded in model_panel
// and splits the score trend line.
func TestConfigFromEnv_PanelModels(t *testing.T) {
	tests := []struct {
		name            string
		baseURL, models string
		want            []string
	}{
		{
			name:   "models alone leave the panel shipped",
			models: "anthropic/claude-opus-4,anthropic/claude-3.5-haiku",
		},
		{
			name:    "a custom gateway adopts the configured models",
			baseURL: "https://openrouter.ai/api",
			models:  "anthropic/claude-opus-4,anthropic/claude-3.5-haiku",
			want:    []string{"anthropic/claude-opus-4", "anthropic/claude-3.5-haiku"},
		},
		{
			name:    "a custom gateway with no models substitutes nothing",
			baseURL: "https://openrouter.ai/api",
		},
		{name: "nothing configured"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SKAEL_ENDPOINT", "http://localhost:8080")
			t.Setenv("SKAEL_API_KEY", "k")
			t.Setenv("ANTHROPIC_API_KEY", "sk-test")
			t.Setenv("ANTHROPIC_BASE_URL", tc.baseURL)
			t.Setenv("LLM_MODEL", tc.models)

			cfg, err := configFromEnv()
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Provider.PanelModels(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("PanelModels() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The panel dials ANTHROPIC_BASE_URL from inside the sandbox and the judge
// dials it from this process. Both must be the same endpoint: a judge left
// pointing at Anthropic while the panel talks to a gateway is the confusion
// the old LLM_BASE_URL invited.
func TestConfigFromEnv_TheJudgeAndThePanelShareOneGateway(t *testing.T) {
	t.Setenv("SKAEL_ENDPOINT", "http://x")
	t.Setenv("SKAEL_API_KEY", "k")
	t.Setenv("ANTHROPIC_API_KEY", "a")
	t.Setenv("ANTHROPIC_BASE_URL", "https://openrouter.ai/api")

	cfg, err := configFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.BaseURL != "https://openrouter.ai/api" {
		t.Errorf("BaseURL = %q, want the ANTHROPIC_BASE_URL value", cfg.Provider.BaseURL)
	}
}
