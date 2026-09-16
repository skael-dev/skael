// Package provider is the one place the environment is mapped onto an LLM
// backend. Both `whetstone` (authoring, on a developer's machine) and
// `skael-worker` (scoring, on a server) resolve their gateway here, so a
// misconfiguration is diagnosed with the same words wherever it is met.
//
// Four provider modes, and no variable outside this file selects one:
//
//  1. Anthropic direct       ANTHROPIC_API_KEY
//  2. Compatible gateway     ANTHROPIC_BASE_URL + ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN
//  3. Subscription CLI       nothing set, and an agent CLI on PATH
//  4. Split                  mode 2, plus CLAUDE_CODE_OAUTH_TOKEN
//
// The judge and the eval panel share ANTHROPIC_BASE_URL. Mode 4 is the one case
// where they separate, and it takes both PanelModels and PanelExcludeEnv to do
// it. Turning it on changes model_panel, which splits a skill's score trend.
package provider

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/agentcli"
	"github.com/skael-dev/skael/internal/eval/llm/api"
	"github.com/skael-dev/skael/internal/eval/runner"
)

// The SDK's and the CLI's own names, not Skael-specific ones: a machine set up
// for either needs no further configuration, and the panel is a Claude Code CLI
// reading the same values the worker resolved.
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
	// OAuthTokenEnv selects mode 4 and nothing else. A judge call is never
	// authenticated with it: a subscription is neither metered nor pinned to a
	// model, and a released version's score must be both.
	OAuthTokenEnv = "CLAUDE_CODE_OAUTH_TOKEN"
)

// Kind names the backend serving model calls.
type Kind string

const (
	KindSubscription Kind = "subscription"
	KindAPI          Kind = "api"
	KindNone         Kind = "none"
)

// Config is data only: nothing dials anything until Gateway is called.
type Config struct {
	Kind   Kind
	Detail string
	Binary string
	// BaseURL is the panel's gateway as much as the judge's. Empty is
	// Anthropic's own API.
	BaseURL string
	Key     string
	// AuthStyle is inferred from which credential was set rather than
	// configured: a bearer token is only ever presented as a bearer token.
	AuthStyle api.AuthStyle
	// Models is ModelEnv, split and trimmed. Empty means the shipped defaults.
	Models []string
	// PanelSubscription is mode 4: the judge keeps BaseURL, the panel
	// authenticates with OAuthTokenEnv.
	PanelSubscription bool
}

// Getenv is os.Getenv, injectable so resolution is testable.
type Getenv func(string) string

// Detector reports the agent CLI serving a subscription provider.
type Detector func() (string, error)

// FromEnv resolves from this process's environment, subscription CLI included.
func FromEnv() Config { return Resolve(os.Getenv, agentcli.Detect) }

// APIFromEnv is FromEnv with subscription detection off, for a caller whose
// backend must be metered and reproducible — the worker, whose judge output
// releases versions. An operator with both a key and a CLI is served by the key.
func APIFromEnv() Config { return Resolve(os.Getenv, nil) }

// Resolve picks a provider. Explicit gateway configuration beats autodetection:
// preferring a CLI that happens to be on PATH would bill the wrong account and
// score against a different model. APIKeyEnv alone stays *below* the CLI — it
// sits on plenty of machines that also have the CLI, and treating it as an
// override moves them onto metered billing without anyone asking.
func Resolve(env Getenv, detect Detector) Config {
	baseURL := strings.TrimSpace(env(BaseURLEnv))
	token := env(AuthTokenEnv)
	key := env(APIKeyEnv)
	models := splitModels(env(ModelEnv))

	direct := Config{
		Kind: KindAPI, BaseURL: baseURL, Models: models,
		Key: key, AuthStyle: api.AuthStyleAnthropic,
		Detail: fmt.Sprintf("Anthropic's API, authenticated with %s", APIKeyEnv),
	}
	if token != "" {
		direct.Key, direct.AuthStyle = token, api.AuthStyleBearer
	}
	if baseURL != "" {
		direct.Detail = fmt.Sprintf("compatible gateway %s, authenticated with %s",
			baseURL, credentialName(token))
		if env(OAuthTokenEnv) != "" {
			direct.PanelSubscription = true
			direct.Detail += fmt.Sprintf("; the eval panel runs on %s instead", OAuthTokenEnv)
		}
	} else if token != "" {
		direct.Detail = fmt.Sprintf("Anthropic's API, authenticated with %s", AuthTokenEnv)
	}

	if baseURL != "" || token != "" {
		return direct
	}
	if detect != nil {
		if bin, err := detect(); err == nil {
			return Config{
				Kind:   KindSubscription,
				Binary: bin,
				Detail: fmt.Sprintf("agent CLI %s, billed to your subscription", bin),
				Models: models,
			}
		}
	}
	if key != "" {
		return direct
	}
	// A caller with no detector never had a subscription option, so naming one
	// would send it looking for a CLI that would not have been used anyway.
	detail := fmt.Sprintf("neither %s nor %s is set", APIKeyEnv, AuthTokenEnv)
	if detect != nil {
		detail = "no supported agent CLI on PATH and " + detail
	}
	return Config{Kind: KindNone, Detail: detail, Models: models}
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
	if c.PanelSubscription {
		// Only the judge is at risk here, and no health probe covers it.
		return []string{fmt.Sprintf(
			"%s points the judge at %s, but %s is not set, so the judge asks that gateway for "+
				"Anthropic's own model name. A gateway that namespaces its model identifiers rejects "+
				"that and every run fails. Set %s to an identifier it serves.",
			BaseURLEnv, c.BaseURL, ModelEnv, ModelEnv)}
	}
	// A passthrough proxy resolves "sonnet" happily, which is why this warns
	// rather than refuses. The panel health probe is the authority.
	return []string{fmt.Sprintf(
		"%s points the judge and the eval panel at %s, but %s is not set, so both ask that gateway "+
			"for Anthropic's own alias %q. A gateway that namespaces its model identifiers (OpenRouter "+
			"uses anthropic/claude-opus-4 where Anthropic uses claude-opus-5) rejects that, and every "+
			"panel member fails its health probe. Set %s to identifiers it serves, most capable first.",
		BaseURLEnv, c.BaseURL, ModelEnv, runner.DefaultPanel()[0].Model, ModelEnv)}
}

// PanelModels is what the panel asks for, empty when the shipped default is
// right. Gated on BaseURL, not on ModelEnv alone: naming a model to pick a
// cheaper judge against Anthropic's own API must not change the panel, because
// a changed panel splits the score trend. A subscription panel is empty for the
// opposite reason — it serves Anthropic's aliases, not a gateway's ids.
func (c Config) PanelModels() []string {
	if c.BaseURL == "" || c.PanelSubscription {
		return nil
	}
	return c.Models
}

// PanelExcludeEnv names the credentials a sandbox must NOT be given, though the
// adapter declares them. This is the half that actually moves the panel: the
// adapter forwards every name it declares that is set, so without the exclusion
// a member asks a subscription for its alias while still pointed at the
// gateway. The API key goes too — forwarding it leaves the sandbox holding two
// competing credentials, which is the ambiguity this mode exists to remove.
func (c Config) PanelExcludeEnv() []string {
	if !c.PanelSubscription {
		return nil
	}
	return []string{BaseURLEnv, AuthTokenEnv, APIKeyEnv}
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
	case KindSubscription:
		return agentcli.New(agentcli.Options{
			Binary:     c.Binary,
			Cache:      o.Cache,
			Timeout:    o.Timeout,
			MaxRetries: o.MaxRetries,
		})
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
