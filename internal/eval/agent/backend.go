package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Provider types, the same strings the server stores.
const (
	ProviderAnthropic        = "anthropic"
	ProviderOpenAI           = "openai"
	ProviderOpenRouter       = "openrouter"
	ProviderOpenAICompatible = "openai-compatible"
)

// Default endpoints. Anthropic's is the host the Messages API appends /v1 to;
// the others are the base /chat/completions is appended to.
const (
	DefaultAnthropicURL  = "https://api.anthropic.com"
	DefaultOpenAIURL     = "https://api.openai.com/v1"
	DefaultOpenRouterURL = "https://openrouter.ai/api/v1"
)

// Cost is a model's price in US dollars per million tokens.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// Backend is one provider and model a session runs against.
type Backend struct {
	Type    string
	BaseURL string
	Key     string
	// AuthHeader sends the key in this header instead of as a bearer token.
	// Only an openai-compatible provider uses it.
	AuthHeader string
	Model      string
	Cost       *Cost
}

// providerID is the name the session config gives the one provider it defines.
const providerID = "skael"

// keyEnv holds the key inside the sandbox; the config only references it.
const keyEnv = "SKAEL_PROVIDER_KEY"

// BaseURLEnv carries the endpoint into the sandbox. Its _BASE_URL suffix is
// what adds the host to the session's egress allowlist.
const BaseURLEnv = "SKAEL_PROVIDER_BASE_URL"

// ModelRef is the -m value for a model on the session's provider.
func ModelRef(model string) string { return providerID + "/" + model }

// Endpoint is the base URL a provider type uses when none is set.
func (b Backend) Endpoint() string {
	if u := strings.TrimRight(b.BaseURL, "/"); u != "" {
		return u
	}
	switch b.Type {
	case ProviderAnthropic:
		return DefaultAnthropicURL
	case ProviderOpenAI:
		return DefaultOpenAIURL
	case ProviderOpenRouter:
		return DefaultOpenRouterURL
	}
	return ""
}

// SessionEnv is the environment one session needs: OpenCode's config, which
// defines a single provider and model, and the key it references.
func SessionEnv(b Backend) ([]string, error) {
	if b.Key == "" {
		return nil, errors.New("the provider has no key")
	}
	if b.Model == "" {
		return nil, errors.New("no model")
	}
	base := b.Endpoint()
	if base == "" {
		return nil, fmt.Errorf("a %s provider needs a base URL", b.Type)
	}
	pkg, sdkBase := "@ai-sdk/openai-compatible", base
	switch b.Type {
	case ProviderAnthropic:
		pkg, sdkBase = "@ai-sdk/anthropic", base+"/v1"
	case ProviderOpenAI, ProviderOpenRouter, ProviderOpenAICompatible:
	default:
		return nil, fmt.Errorf("unknown provider type %q", b.Type)
	}
	options := map[string]any{"baseURL": sdkBase}
	if b.AuthHeader != "" {
		options["headers"] = map[string]string{b.AuthHeader: "{env:" + keyEnv + "}"}
	} else {
		options["apiKey"] = "{env:" + keyEnv + "}"
	}
	model := map[string]any{}
	if b.Cost != nil {
		model["cost"] = b.Cost
	}
	cfg := map[string]any{
		"autoupdate": false,
		"share":      "disabled",
		"provider": map[string]any{providerID: map[string]any{
			"npm":     pkg,
			"name":    providerID,
			"options": options,
			"models":  map[string]any{b.Model: model},
		}},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return []string{
		"OPENCODE_CONFIG_CONTENT=" + string(raw),
		keyEnv + "=" + b.Key,
		BaseURLEnv + "=" + base,
	}, nil
}
