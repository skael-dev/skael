package provider

import (
	"fmt"

	"github.com/skael-dev/skael/internal/eval/llm/api"
)

// Backend adapters the server's providers use.
const (
	AdapterAnthropic    = "anthropic"
	AdapterGateway      = "gateway"
	AdapterSubscription = "subscription"
)

// Backend is one provider and model chosen on the server. The credential is
// never sent: CredentialEnv names the worker's own variable that holds it.
type Backend struct {
	Adapter       string
	BaseURL       string
	AuthStyle     string
	CredentialEnv string
	Model         string
}

func (b Backend) credential(env Getenv) (string, error) {
	v := env(b.CredentialEnv)
	if v == "" {
		return "", fmt.Errorf("this worker has no %s, which the provider needs", b.CredentialEnv)
	}
	return v, nil
}

// Judge resolves the judge's backend. A subscription never judges: a released
// version's score must come from a metered backend pinned to a model.
func Judge(env Getenv, b Backend) (Config, error) {
	if b.Adapter == AdapterSubscription {
		return Config{}, fmt.Errorf("a Claude subscription cannot judge; choose an API or gateway provider")
	}
	key, err := b.credential(env)
	if err != nil {
		return Config{}, err
	}
	c := Config{Kind: KindAPI, Key: key, AuthStyle: api.AuthStyleAnthropic, Models: []string{b.Model}}
	if b.Adapter == AdapterGateway {
		c.BaseURL = b.BaseURL
		if b.AuthStyle == string(api.AuthStyleBearer) {
			c.AuthStyle = api.AuthStyleBearer
		}
		c.Detail = fmt.Sprintf("compatible gateway %s, authenticated with %s", b.BaseURL, b.CredentialEnv)
	} else {
		c.Detail = "Anthropic's API, authenticated with " + b.CredentialEnv
	}
	return c, nil
}

// MemberEnv is the credential variables a panel member's sandbox receives.
func MemberEnv(env Getenv, b Backend) ([]string, error) {
	v, err := b.credential(env)
	if err != nil {
		return nil, err
	}
	switch b.Adapter {
	case AdapterSubscription:
		return []string{OAuthTokenEnv + "=" + v}, nil
	case AdapterGateway:
		name := APIKeyEnv
		if b.AuthStyle == string(api.AuthStyleBearer) {
			name = AuthTokenEnv
		}
		return []string{BaseURLEnv + "=" + b.BaseURL, name + "=" + v}, nil
	}
	return []string{APIKeyEnv + "=" + v}, nil
}

// PinnedByEnv reports whether this process's own gateway or model settings
// override the server's providers.
func PinnedByEnv(env Getenv) bool {
	return env(BaseURLEnv) != "" || env(ModelEnv) != ""
}
