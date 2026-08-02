// Package fake provides an in-memory Gateway for tests: these tests run with
// no LLM subscription, no API key, and no network, so the fake is how
// generation and suite behaviour get exercised.
package fake

import (
	"context"
	"fmt"
	"sync"

	"github.com/skael-dev/skael/internal/eval/llm"
)

// Gateway returns scripted responses in order and records every request.
type Gateway struct {
	mu        sync.Mutex
	responses []string
	calls     []llm.Req
	err       error
}

// New returns a fake that replies with responses in order.
func New(responses ...string) *Gateway {
	return &Gateway{responses: responses}
}

// SetError makes every subsequent Complete return err.
func (g *Gateway) SetError(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.err = err
}

// Calls returns every request received, in order.
func (g *Gateway) Calls() []llm.Req {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]llm.Req(nil), g.calls...)
}

// Complete implements llm.Gateway.
func (g *Gateway) Complete(_ context.Context, r llm.Req) (llm.Res, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.calls = append(g.calls, r)
	if g.err != nil {
		return llm.Res{}, g.err
	}
	if len(g.calls) > len(g.responses) {
		return llm.Res{}, fmt.Errorf("fake: unexpected call %d (%s); only %d responses scripted",
			len(g.calls), r.Role, len(g.responses))
	}
	return llm.Res{Text: g.responses[len(g.calls)-1], Model: "fake"}, nil
}

// ModelFor implements llm.Gateway with a deterministic, class-distinguishing
// name, so tests that assert on which model a role used can rely on it.
func (g *Gateway) ModelFor(c llm.ModelClass) string {
	if c == llm.ClassFast {
		return "fake-fast"
	}
	return "fake-strong"
}
