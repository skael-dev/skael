package provider

import (
	"fmt"
	"net/http"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/api"
	"github.com/skael-dev/skael/internal/evalqueue"
)

// Session maps one of the server's providers and a model onto what a panel
// session needs.
func Session(b evalqueue.EvalBackend) agent.Backend {
	out := agent.Backend{Type: b.Type, BaseURL: b.BaseURL, Key: b.Credential, AuthHeader: b.AuthHeader, Model: b.Model}
	if b.Cost != nil {
		out.Cost = &agent.Cost{Input: b.Cost.Input, Output: b.Cost.Output, CacheRead: b.Cost.CacheRead, CacheWrite: b.Cost.CacheWrite}
	}
	return out
}

// Judge builds the judge's gateway from one of the server's providers. An
// Anthropic provider speaks Messages; every other type speaks Chat
// Completions.
func Judge(b evalqueue.EvalBackend, o Options) (llm.Gateway, error) {
	if b.Credential == "" {
		return nil, fmt.Errorf("the provider %s has no key; add one in Settings → Evaluation", b.Provider)
	}
	ab := Session(b)
	opts := api.Options{
		BaseURL: b.BaseURL, APIKey: b.Credential, AuthHeader: b.AuthHeader,
		StrongModel: b.Model, FastModel: b.Model,
		Cache: o.Cache, HTTPClient: &http.Client{Timeout: o.Timeout}, MaxRetries: o.MaxRetries,
	}
	if b.Type != evalqueue.ProviderAnthropic {
		opts.Format, opts.BaseURL = api.FormatOpenAI, ab.Endpoint()
	}
	return api.New(opts)
}
