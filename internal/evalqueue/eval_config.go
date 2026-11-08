package evalqueue

import (
	"net/url"
	"strings"
)

// Provider adapters.
const (
	AdapterAnthropic    = "anthropic"
	AdapterGateway      = "gateway"
	AdapterSubscription = "subscription"
)

// EvalBackend is one provider and model, as a worker needs them: where to
// send calls and which of its own env vars holds the credential.
type EvalBackend struct {
	Provider      string `json:"provider"`
	Adapter       string `json:"adapter"`
	BaseURL       string `json:"base_url,omitempty"`
	AuthStyle     string `json:"auth_style,omitempty"`
	CredentialEnv string `json:"credential_env"`
	Model         string `json:"model"`
}

// EvalConfig is the judge and the panel the server chose. The first member is
// the primary member.
type EvalConfig struct {
	Judge   EvalBackend   `json:"judge"`
	Members []EvalBackend `json:"members"`
}

// Label names the backend in a report's model_panel: empty for Anthropic's own
// API, "subscription", or "gateway:<host>". It ignores the provider's name, so
// a rename does not split a score trend.
func (b EvalBackend) Label() string {
	switch b.Adapter {
	case AdapterSubscription:
		return "subscription"
	case AdapterGateway:
		host := b.BaseURL
		if u, err := url.Parse(b.BaseURL); err == nil && u.Host != "" {
			host = u.Host
		}
		return "gateway:" + strings.ToLower(host)
	}
	return ""
}
