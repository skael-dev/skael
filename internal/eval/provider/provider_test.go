package provider_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/api"
	"github.com/skael-dev/skael/internal/eval/provider"
	"github.com/skael-dev/skael/internal/evalqueue"
)

// envOf turns a map into a Getenv, so a case names only what it sets.
func envOf(m map[string]string) provider.Getenv {
	return func(k string) string { return m[k] }
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
			got := provider.Resolve(envOf(tc.env))
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
	}))

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
			got := provider.Resolve(envOf(tc.env)).PanelModels()
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
			if got := provider.Resolve(envOf(env)).Warnings(); len(got) != 0 {
				t.Errorf("%v: warned when it should not have: %v", env, got)
			}
		}
	})

	// An operator can only act on a warning that names the variable to set.
	t.Run("a custom gateway with no models names what to set", func(t *testing.T) {
		got := provider.Resolve(envOf(map[string]string{
			provider.BaseURLEnv: "https://openrouter.ai/api",
			provider.APIKeyEnv:  "sk-x",
		})).Warnings()
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
		cfg := provider.Resolve(envOf(map[string]string{provider.BaseURLEnv: "https://g"}))
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
		if err := provider.Resolve(envOf(nil)).Validate(); err == nil {
			t.Fatal("KindNone validated")
		}
	})

	t.Run("a usable provider validates", func(t *testing.T) {
		if err := provider.Resolve(envOf(map[string]string{provider.APIKeyEnv: "sk-x"})).Validate(); err != nil {
			t.Errorf("a plain API key was refused: %v", err)
		}
	})
}

// The gateway must carry the resolved credential and models, or resolution is
// decorative.
func TestGateway_CarriesTheResolvedConfiguration(t *testing.T) {
	cfg := provider.Resolve(envOf(map[string]string{
		provider.BaseURLEnv:   "https://openrouter.ai/api",
		provider.AuthTokenEnv: "tok",
		provider.ModelEnv:     "anthropic/claude-opus-4,anthropic/claude-3.5-haiku",
	}))

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
	}))

	gw, err := cfg.Gateway(provider.Options{})
	if err != nil {
		t.Fatalf("Gateway: %v", err)
	}
	if got := gw.ModelFor("fast"); got != "claude-opus-5" {
		t.Errorf("ModelFor(fast) = %q, want the single configured model", got)
	}
}

func TestGateway_RefusesAnUnusableProvider(t *testing.T) {
	if _, err := provider.Resolve(envOf(nil)).Gateway(provider.Options{}); err == nil {
		t.Fatal("built a gateway with no provider at all")
	}
}

// A judge on any non-Anthropic provider speaks Chat Completions at that
// provider's endpoint, with the key the claim carried.
func TestJudge_UsesTheProvidersFormatAndKey(t *testing.T) {
	var path, auth string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer s.Close()
	gw, err := provider.Judge(evalqueue.EvalBackend{Provider: "Proxy", Type: evalqueue.ProviderOpenAICompatible,
		BaseURL: s.URL + "/v1", Credential: "sk-1", Model: "m"}, provider.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gw.Complete(context.Background(), llm.Req{Role: "r", Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if path != "/v1/chat/completions" || auth != "Bearer sk-1" {
		t.Errorf("path %q auth %q", path, auth)
	}
	if _, err := provider.Judge(evalqueue.EvalBackend{Provider: "OpenRouter", Type: evalqueue.ProviderOpenRouter, Model: "m"}, provider.Options{}); err == nil {
		t.Error("a provider with no key must not build a judge")
	}
}
