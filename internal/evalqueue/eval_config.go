package evalqueue

import (
	"net/url"
	"strings"
)

// Provider types.
const (
	ProviderAnthropic        = "anthropic"
	ProviderOpenAI           = "openai"
	ProviderOpenRouter       = "openrouter"
	ProviderOpenAICompatible = "openai-compatible"
)

// ProviderTypes lists every provider type, in the order the UI offers them.
var ProviderTypes = []string{ProviderAnthropic, ProviderOpenAI, ProviderOpenRouter, ProviderOpenAICompatible}

// ModelCost is a model's price in US dollars per million tokens.
type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

// EvalBackend is one provider and model, as a worker needs them. Credential
// is the provider's key: the server sends it only inside a job claim.
type EvalBackend struct {
	Provider   string     `json:"provider"`
	Type       string     `json:"type"`
	BaseURL    string     `json:"base_url,omitempty"`
	AuthHeader string     `json:"auth_header,omitempty"`
	Credential string     `json:"credential,omitempty"`
	Model      string     `json:"model"`
	Cost       *ModelCost `json:"cost,omitempty"`
}

// EvalConfig is the judge and the panel the server chose. The first member is
// the primary member.
type EvalConfig struct {
	Judge   EvalBackend   `json:"judge"`
	Members []EvalBackend `json:"members"`
}

// Label names the backend in a report's model_panel: empty for Anthropic's own
// API, otherwise the type and the endpoint's host. It ignores the provider's
// name, so a rename does not split a score trend.
func (b EvalBackend) Label() string {
	if b.BaseURL == "" {
		if b.Type == ProviderAnthropic {
			return ""
		}
		return b.Type
	}
	host := b.BaseURL
	if u, err := url.Parse(b.BaseURL); err == nil && u.Host != "" {
		host = u.Host
	}
	return b.Type + ":" + strings.ToLower(host)
}
