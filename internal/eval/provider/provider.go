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
// The judge and the eval panel share ANTHROPIC_BASE_URL. Mode 4 is the one
// case where they separate: the panel bills to a subscription, the judge keeps
// the gateway. Turning it on changes model_panel, which splits a skill's score
// trend at the changeover. See Resolve, PanelModels and PanelExcludeEnv.
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

// The environment this package reads. These are the Anthropic SDK's and the
// Claude Code CLI's own names rather than Skael-specific ones, so a machine
// already set up for either needs no further configuration — and so the panel,
// which is a Claude Code CLI running inside a sandbox, reads the same values
// the worker resolved.
const (
	APIKeyEnv    = "ANTHROPIC_API_KEY"
	AuthTokenEnv = "ANTHROPIC_AUTH_TOKEN"
	BaseURLEnv   = "ANTHROPIC_BASE_URL"
	// ModelEnv is a comma-separated list, most capable first. The first entry
	// serves every judge call and the panel's primary member; the rest are the
	// panel's floor members, which only the deep tier runs. It replaces the
	// former LLM_STRONG_MODEL/LLM_FAST_MODEL pair, whose two names could be
	// half-set — a panel with one working member and one that 404s is not an
	// error but a complete run that scores and can never release anything.
	ModelEnv = "LLM_MODEL"
	// OAuthTokenEnv is Claude Code's own subscription credential. It is read
	// here only to select mode 4 — this package never authenticates a judge
	// call with it, because a subscription is neither metered nor pinned to a
	// model, and a released version's score must be both.
	OAuthTokenEnv = "CLAUDE_CODE_OAUTH_TOKEN"
)

// Kind names the backend serving model calls.
type Kind string

const (
	// KindSubscription is an agent CLI on PATH, billed to a subscription.
	KindSubscription Kind = "subscription"
	// KindAPI is a direct HTTP gateway: Anthropic's own, or a compatible one.
	KindAPI Kind = "api"
	// KindNone is no usable backend.
	KindNone Kind = "none"
)

// Config is the resolved provider. It is data only: nothing here dials
// anything until Gateway is called.
type Config struct {
	Kind Kind
	// Detail explains the choice in one clause, including why it is none.
	Detail string
	// Binary is the agent CLI backing a subscription provider.
	Binary string
	// BaseURL is ANTHROPIC_BASE_URL, empty for Anthropic's own API. It is the
	// panel's gateway as much as the judge's.
	BaseURL string
	// Key is whichever credential authenticates the API provider.
	Key string
	// AuthStyle is inferred from which credential was set rather than
	// configured: a bearer token is only ever presented as a bearer token.
	AuthStyle api.AuthStyle
	// Models is ModelEnv, split and trimmed. Empty means the shipped defaults.
	Models []string
	// PanelSubscription reports mode 4: the judge dials BaseURL, and the eval
	// panel authenticates with OAuthTokenEnv instead. It changes what the
	// panel asks for (PanelModels) and what the sandbox is allowed to see
	// (PanelExcludeEnv); both are required, because either one alone leaves
	// the panel on the gateway.
	PanelSubscription bool
}

// Getenv is os.Getenv, injectable so resolution is testable without touching
// the process environment.
type Getenv func(string) string

// Detector reports the agent CLI serving a subscription provider.
type Detector func() (string, error)

// FromEnv resolves the provider from this process's environment, including a
// subscription CLI on PATH.
func FromEnv() Config { return Resolve(os.Getenv, agentcli.Detect) }

// APIFromEnv is FromEnv with subscription detection off, for a caller whose
// backend must be metered and reproducible — `skael-worker`, whose judge
// output releases versions. A CLI on the worker's host is then not a provider
// at all rather than a provider it must refuse, so an operator who has both a
// key and a CLI installed is served by the key.
func APIFromEnv() Config { return Resolve(os.Getenv, nil) }

// Resolve picks a provider.
//
// Explicit gateway configuration beats autodetection: setting a base URL or a
// bearer token is an unambiguous statement that a particular gateway is
// intended, and silently preferring a subscription CLI that happens to be on
// PATH would bill the wrong account and evaluate against a different model
// than the one configured.
//
// APIKeyEnv alone stays *below* the CLI. It is present on plenty of developer
// machines that also have the CLI installed, and treating it as an override
// would move those machines onto metered billing without anyone asking for it.
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

// Validate reports a configuration that cannot serve a call. It is the shared
// half of `whetstone doctor` and the worker's startup probe: both report what
// this returns, so the same mistake reads the same way in both.
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
// break confusingly when they do break. One place, so `whetstone doctor` and
// the worker's startup say the same thing.
func (c Config) Warnings() []string {
	if c.BaseURL == "" || len(c.Models) > 0 {
		return nil
	}
	if c.PanelSubscription {
		// The panel is not on the gateway here, so only the judge is at risk
		// and there is no health probe to catch it. A judge that 404s fails
		// the run outright, which at least says so.
		return []string{fmt.Sprintf(
			"%s points the judge at %s, but %s is not set, so the judge asks that gateway for "+
				"Anthropic's own model name. A gateway that namespaces its model identifiers rejects "+
				"that and every run fails. Set %s to an identifier it serves.",
			BaseURLEnv, c.BaseURL, ModelEnv, ModelEnv)}
	}
	// A passthrough proxy in front of Anthropic resolves "sonnet" happily,
	// which is why this is not a refusal. The panel health probe is the
	// authority and names the models it was refused.
	return []string{fmt.Sprintf(
		"%s points the judge and the eval panel at %s, but %s is not set, so both ask that gateway "+
			"for Anthropic's own alias %q. A gateway that namespaces its model identifiers (OpenRouter "+
			"uses anthropic/claude-opus-4 where Anthropic uses claude-opus-5) rejects that, and every "+
			"panel member fails its health probe. Set %s to identifiers it serves, most capable first.",
		BaseURLEnv, c.BaseURL, ModelEnv, runner.DefaultPanel()[0].Model, ModelEnv)}
}

// PanelModels is the model ids the eval panel should ask for, empty when the
// shipped default is right.
//
// Gated on BaseURL, not on ModelEnv alone: an operator who named a model to
// pick a cheaper judge against Anthropic's own API must keep the panel they
// had, since a changed panel is recorded in model_panel and splits the score
// trend. A custom gateway is the case where the shipped aliases cannot work.
// A panel on the subscription is the other empty case: a subscription serves
// Anthropic's own aliases, so handing it the gateway's namespaced ids fails
// every member's health probe — a complete run that scores nothing and can
// never release the version it was meant to clear.
func (c Config) PanelModels() []string {
	if c.BaseURL == "" || c.PanelSubscription {
		return nil
	}
	return c.Models
}

// PanelExcludeEnv names the credential variables a sandbox must NOT be given,
// even though the agent adapter declares them. It is empty in every mode but
// the split.
//
// This is the half that actually moves the panel. The claude-code adapter
// forwards each name it declares that is set, so a worker whose own
// environment names a gateway hands that gateway to every panel member.
// PanelModels alone does not help: the member would ask a subscription for
// the alias it serves and still be pointed at the gateway to ask.
//
// The API key is withheld with the gateway's own two names. It is not the
// judge's credential in this mode, and forwarding it leaves the sandbox
// holding two competing credentials, which is the ambiguity this mode exists
// to remove.
func (c Config) PanelExcludeEnv() []string {
	if !c.PanelSubscription {
		return nil
	}
	return []string{BaseURLEnv, AuthTokenEnv, APIKeyEnv}
}

// Options are the per-caller gateway settings that are not provider choices:
// whetstone allows a long interactive timeout and shares its workspace
// completion cache, the worker does neither.
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

// first and last map one ordered list onto the api gateway's two model slots.
// gen.outline is the only production caller of llm.ClassFast, so a
// single-entry list serving both slots resolves to one model.
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
