package provider_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm/api"
	"github.com/skael-dev/skael/internal/eval/provider"
)

// envOf turns a map into a Getenv, so a case names only what it sets.
func envOf(m map[string]string) provider.Getenv {
	return func(k string) string { return m[k] }
}

func noCLI() (string, error) { return "", errors.New("no agent CLI") }

func haveCLI() (string, error) { return "/usr/local/bin/claude", nil }

func TestResolve_Precedence(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		detect  provider.Detector
		want    provider.Kind
		wantKey string
	}{
		{
			name:   "nothing set and no CLI",
			env:    nil,
			detect: noCLI,
			want:   provider.KindNone,
		},
		{
			// The developer machine that has both. Metering someone who never
			// asked for it is the mistake this ordering exists to avoid.
			name:   "a CLI outranks a bare API key",
			env:    map[string]string{provider.APIKeyEnv: "sk-x"},
			detect: haveCLI,
			want:   provider.KindSubscription,
		},
		{
			// Naming a gateway is unambiguous. A CLI on PATH must not win it.
			name:    "an explicit gateway outranks a CLI",
			env:     map[string]string{provider.BaseURLEnv: "https://openrouter.ai/api", provider.APIKeyEnv: "sk-x"},
			detect:  haveCLI,
			want:    provider.KindAPI,
			wantKey: "sk-x",
		},
		{
			name:    "a bearer token outranks a CLI",
			env:     map[string]string{provider.AuthTokenEnv: "tok"},
			detect:  haveCLI,
			want:    provider.KindAPI,
			wantKey: "tok",
		},
		{
			name:    "an API key with no CLI",
			env:     map[string]string{provider.APIKeyEnv: "sk-x"},
			detect:  noCLI,
			want:    provider.KindAPI,
			wantKey: "sk-x",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := provider.Resolve(envOf(tc.env), tc.detect)
			if got.Kind != tc.want {
				t.Errorf("Kind = %q, want %q", got.Kind, tc.want)
			}
			if got.Key != tc.wantKey {
				t.Errorf("Key = %q, want %q", got.Key, tc.wantKey)
			}
			if got.Detail == "" {
				t.Error("every choice must explain itself, including none")
			}
		})
	}
}

// The asymmetry this replaced: the worker read no bearer token at all, so a
// gateway configured the documented way authenticated with the wrong header
// or not at all.
func TestResolve_InfersTheAuthStyleFromTheCredential(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want api.AuthStyle
	}{
		{
			name: "a key is presented as x-api-key",
			env:  map[string]string{provider.APIKeyEnv: "sk-x"},
			want: api.AuthStyleAnthropic,
		},
		{
			name: "a token is presented as a bearer token",
			env:  map[string]string{provider.AuthTokenEnv: "tok"},
			want: api.AuthStyleBearer,
		},
		{
			// Both set is not ambiguous: a bearer token is only ever issued for
			// a gateway that wants one, so it wins.
			name: "a token wins when both are set",
			env:  map[string]string{provider.APIKeyEnv: "sk-x", provider.AuthTokenEnv: "tok"},
			want: api.AuthStyleBearer,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := provider.Resolve(envOf(tc.env), noCLI)
			if got.AuthStyle != tc.want {
				t.Errorf("AuthStyle = %q, want %q", got.AuthStyle, tc.want)
			}
		})
	}
}

func TestResolve_Models(t *testing.T) {
	cfg := provider.Resolve(envOf(map[string]string{
		provider.APIKeyEnv: "sk-x",
		provider.ModelEnv:  " anthropic/claude-opus-4 , ,anthropic/claude-3.5-haiku ",
	}), noCLI)

	want := []string{"anthropic/claude-opus-4", "anthropic/claude-3.5-haiku"}
	if len(cfg.Models) != len(want) {
		t.Fatalf("Models = %v, want %v", cfg.Models, want)
	}
	for i, w := range want {
		if cfg.Models[i] != w {
			t.Errorf("Models[%d] = %q, want %q", i, cfg.Models[i], w)
		}
	}
}

func TestPanelModels(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{
			// The backward-compatibility guarantee. Naming a model to pick a
			// cheaper judge must not change the panel: a changed panel splits
			// the score trend line.
			name: "models alone leave the panel shipped",
			env:  map[string]string{provider.APIKeyEnv: "sk-x", provider.ModelEnv: "claude-opus-5"},
		},
		{
			name: "a custom gateway adopts the configured models",
			env: map[string]string{
				provider.BaseURLEnv: "https://openrouter.ai/api",
				provider.APIKeyEnv:  "sk-x",
				provider.ModelEnv:   "anthropic/claude-opus-4,anthropic/claude-3.5-haiku",
			},
			want: []string{"anthropic/claude-opus-4", "anthropic/claude-3.5-haiku"},
		},
		{
			name: "a custom gateway with no models substitutes nothing",
			env:  map[string]string{provider.BaseURLEnv: "https://openrouter.ai/api", provider.APIKeyEnv: "sk-x"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := provider.Resolve(envOf(tc.env), noCLI).PanelModels()
			if len(got) != len(tc.want) {
				t.Fatalf("PanelModels() = %v, want %v", got, tc.want)
			}
			for i, w := range tc.want {
				if got[i] != w {
					t.Errorf("PanelModels()[%d] = %q, want %q", i, got[i], w)
				}
			}
		})
	}
}

func TestWarnings(t *testing.T) {
	t.Run("silent when there is nothing to say", func(t *testing.T) {
		for _, env := range []map[string]string{
			nil,
			{provider.APIKeyEnv: "sk-x"},
			{provider.APIKeyEnv: "sk-x", provider.ModelEnv: "claude-opus-5"},
			{provider.BaseURLEnv: "https://g", provider.APIKeyEnv: "sk-x", provider.ModelEnv: "m"},
		} {
			if got := provider.Resolve(envOf(env), noCLI).Warnings(); len(got) != 0 {
				t.Errorf("%v: warned when it should not have: %v", env, got)
			}
		}
	})

	// An operator can only act on a warning that names the variable to set.
	t.Run("a custom gateway with no models names what to set", func(t *testing.T) {
		got := provider.Resolve(envOf(map[string]string{
			provider.BaseURLEnv: "https://openrouter.ai/api",
			provider.APIKeyEnv:  "sk-x",
		}), noCLI).Warnings()
		if len(got) != 1 {
			t.Fatalf("Warnings() = %v, want one warning", got)
		}
		for _, want := range []string{provider.BaseURLEnv, provider.ModelEnv, "https://openrouter.ai/api"} {
			if !strings.Contains(got[0], want) {
				t.Errorf("warning does not name %q: %s", want, got[0])
			}
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("a gateway with no credential cannot serve a call", func(t *testing.T) {
		cfg := provider.Resolve(envOf(map[string]string{provider.BaseURLEnv: "https://g"}), noCLI)
		err := cfg.Validate()
		if err == nil {
			t.Fatal("accepted a gateway with neither a key nor a token")
		}
		for _, want := range []string{provider.APIKeyEnv, provider.AuthTokenEnv} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error does not name %q: %v", want, err)
			}
		}
	})

	t.Run("no provider at all is an error", func(t *testing.T) {
		if err := provider.Resolve(envOf(nil), noCLI).Validate(); err == nil {
			t.Fatal("KindNone validated")
		}
	})

	t.Run("a usable provider validates", func(t *testing.T) {
		if err := provider.Resolve(envOf(map[string]string{provider.APIKeyEnv: "sk-x"}), noCLI).Validate(); err != nil {
			t.Errorf("a plain API key was refused: %v", err)
		}
	})
}

// A verified score must come from a metered, reproducible backend. The worker
// resolves with no detector, so a CLI on its host is not a provider it has to
// refuse — an operator with both a key and a CLI installed is served by the
// key, which is what the worker did before this package existed.
func TestResolve_WithoutADetectorNeverPicksASubscription(t *testing.T) {
	cfg := provider.Resolve(envOf(map[string]string{provider.APIKeyEnv: "sk-x"}), nil)
	if cfg.Kind != provider.KindAPI {
		t.Fatalf("Kind = %q, want %q with a key and no detector", cfg.Kind, provider.KindAPI)
	}

	none := provider.Resolve(envOf(nil), nil)
	if none.Kind != provider.KindNone {
		t.Fatalf("Kind = %q, want %q with nothing set", none.Kind, provider.KindNone)
	}
	// Naming a CLI that would not have been used either way sends an operator
	// looking in the wrong place.
	if strings.Contains(none.Detail, "PATH") {
		t.Errorf("detail names a subscription CLI the caller never offered: %s", none.Detail)
	}
	if !strings.Contains(none.Detail, provider.APIKeyEnv) {
		t.Errorf("detail does not name the variable to set: %s", none.Detail)
	}
}

// The gateway must carry the resolved credential and models, or resolution is
// decorative.
func TestGateway_CarriesTheResolvedConfiguration(t *testing.T) {
	cfg := provider.Resolve(envOf(map[string]string{
		provider.BaseURLEnv:   "https://openrouter.ai/api",
		provider.AuthTokenEnv: "tok",
		provider.ModelEnv:     "anthropic/claude-opus-4,anthropic/claude-3.5-haiku",
	}), noCLI)

	gw, err := cfg.Gateway(provider.Options{})
	if err != nil {
		t.Fatalf("Gateway: %v", err)
	}
	if got := gw.ModelFor("strong"); got != "anthropic/claude-opus-4" {
		t.Errorf("ModelFor(strong) = %q, want the first configured model", got)
	}
	if got := gw.ModelFor("fast"); got != "anthropic/claude-3.5-haiku" {
		t.Errorf("ModelFor(fast) = %q, want the last configured model", got)
	}
}

// One model configured serves both slots — which is exactly the behaviour the
// two-variable pair had when only the strong one was set.
func TestGateway_OneModelServesBothClasses(t *testing.T) {
	cfg := provider.Resolve(envOf(map[string]string{
		provider.APIKeyEnv: "sk-x",
		provider.ModelEnv:  "claude-opus-5",
	}), noCLI)

	gw, err := cfg.Gateway(provider.Options{})
	if err != nil {
		t.Fatalf("Gateway: %v", err)
	}
	if got := gw.ModelFor("fast"); got != "claude-opus-5" {
		t.Errorf("ModelFor(fast) = %q, want the single configured model", got)
	}
}

func TestGateway_RefusesAnUnusableProvider(t *testing.T) {
	if _, err := provider.Resolve(envOf(nil), noCLI).Gateway(provider.Options{}); err == nil {
		t.Fatal("built a gateway with no provider at all")
	}
}
