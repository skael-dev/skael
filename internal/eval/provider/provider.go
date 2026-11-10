// Package provider resolves an LLM backend: the server's providers for a
// worker (Session, Judge), and one Anthropic-format endpoint from the
// environment for the local `whetstone` CLI (Resolve).
package provider

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/api"
	"github.com/skael-dev/skael/internal/eval/runner"
)

const (
	APIKeyEnv    = "ANTHROPIC_API_KEY"
	AuthTokenEnv = "ANTHROPIC_AUTH_TOKEN"
	BaseURLEnv   = "ANTHROPIC_BASE_URL"
	// ModelEnv is comma-separated, most capable first: the first entry serves
	// every judge call and the panel's primary member, the rest are the panel's
	// floor members. One list rather than a strong/fast pair, which could be
	// half-set — a panel with one working member and one that 404s is not an
	// error but a complete run that scores and can never release anything.
	ModelEnv = "LLM_MODEL"
)

// Kind names the backend serving model calls.
type Kind string

const (
	KindAPI  Kind = "api"
	KindNone Kind = "none"
)

// Config is data only: nothing dials anything until Gateway is called.
type Config struct {
	Kind   Kind
	Detail string
	// BaseURL is the panel's gateway as much as the judge's. Empty is
	// Anthropic's own API.
	BaseURL string
	Key     string
	// AuthStyle is inferred from which credential was set rather than
	// configured: a bearer token is only ever presented as a bearer token.
	AuthStyle api.AuthStyle
	// Models is ModelEnv, split and trimmed. Empty means the shipped defaults.
	Models []string
}

// Getenv is os.Getenv, injectable so resolution is testable.
type Getenv func(string) string

func FromEnv() Config { return Resolve(os.Getenv) }

func Resolve(env Getenv) Config {
	baseURL := strings.TrimSpace(env(BaseURLEnv))
	token := env(AuthTokenEnv)
	key := env(APIKeyEnv)
	models := splitModels(env(ModelEnv))

	c := Config{
		Kind: KindAPI, BaseURL: baseURL, Models: models,
		Key: key, AuthStyle: api.AuthStyleAnthropic,
		Detail: fmt.Sprintf("Anthropic's API, authenticated with %s", APIKeyEnv),
	}
	if token != "" {
		c.Key, c.AuthStyle = token, api.AuthStyleBearer
	}
	switch {
	case baseURL != "":
		c.Detail = fmt.Sprintf("compatible endpoint %s, authenticated with %s", baseURL, credentialName(token))
	case token != "":
		c.Detail = fmt.Sprintf("Anthropic's API, authenticated with %s", AuthTokenEnv)
	case key == "":
		return Config{Kind: KindNone, Models: models,
			Detail: fmt.Sprintf("neither %s nor %s is set", APIKeyEnv, AuthTokenEnv)}
	}
	return c
}

func credentialName(token string) string {
	if token != "" {
		return AuthTokenEnv
	}
	return APIKeyEnv
}

func splitModels(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Validate reports a configuration that cannot serve a call. `whetstone doctor`
// and the worker's startup both report what this returns.
func (c Config) Validate() error {
	switch c.Kind {
	case KindAPI:
		if c.Key == "" {
			return fmt.Errorf("%s is set but neither %s nor %s is, so no call can be authenticated",
				BaseURLEnv, APIKeyEnv, AuthTokenEnv)
		}
	case KindNone:
		return fmt.Errorf("%s", c.Detail)
	}
	return nil
}

// Warnings reports configurations that work often enough not to refuse, and
// break confusingly when they do.
func (c Config) Warnings() []string {
	if c.BaseURL == "" || len(c.Models) > 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"%s points the judge and the eval panel at %s, but %s is not set, so both ask that endpoint "+
			"for Anthropic's own id %q. An endpoint that namespaces its model ids (OpenRouter uses "+
			"anthropic/claude-sonnet-5) rejects that, and every panel member fails its health probe. "+
			"Set %s to ids it serves, most capable first.",
		BaseURLEnv, c.BaseURL, ModelEnv, runner.DefaultPanel()[0].Model, ModelEnv)}
}

// PanelModels is what the panel asks for, empty for the shipped default. A
// model named only to cheapen the judge on Anthropic's API keeps the panel.
func (c Config) PanelModels() []string {
	if c.BaseURL == "" {
		return nil
	}
	return c.Models
}

// Member is one panel model on the judge's endpoint and key.
func (c Config) Member(model string) agent.Backend {
	return agent.Backend{Type: agent.ProviderAnthropic, BaseURL: c.BaseURL, Key: c.Key, Model: model}
}

// Options are per-caller settings that are not provider choices: whetstone
// allows a long timeout and shares a completion cache, the worker does neither.
type Options struct {
	Cache      llm.Cache
	Timeout    time.Duration
	MaxRetries int
}

// Gateway builds the resolved backend.
func (c Config) Gateway(o Options) (llm.Gateway, error) {
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("no LLM gateway available: %w", err)
	}
	switch c.Kind {
	case KindAPI:
		return api.New(api.Options{
			BaseURL:     c.BaseURL,
			APIKey:      c.Key,
			AuthStyle:   c.AuthStyle,
			StrongModel: first(c.Models),
			FastModel:   last(c.Models),
			Cache:       o.Cache,
			HTTPClient:  &http.Client{Timeout: o.Timeout},
			MaxRetries:  o.MaxRetries,
		})
	}
	return nil, fmt.Errorf("no LLM gateway available: %s", c.Detail)
}

// gen.outline is the only production caller of llm.ClassFast, so a single-entry
// list serving both slots resolves to one model.
func first(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}

func last(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[len(ss)-1]
}
