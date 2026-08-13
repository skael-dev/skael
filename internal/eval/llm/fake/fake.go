// Package fake provides an in-memory Gateway for tests.
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
	fn        func(llm.Req) (string, error)
	calls     []llm.Req
	err       error
}

// New returns a fake that replies with responses in order.
func New(responses ...string) *Gateway {
	return &Gateway{responses: responses}
}

// NewFunc returns a fake that answers from fn, for concurrent fan-out tests
// where order-based scripting is nondeterministic.
func NewFunc(fn func(llm.Req) (string, error)) *Gateway {
	return &Gateway{fn: fn}
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
	if g.fn != nil {
		text, err := g.fn(r)
		if err != nil {
			return llm.Res{}, err
		}
		return llm.Res{Text: text, Model: "fake"}, nil
	}
	if len(g.calls) > len(g.responses) {
		return llm.Res{}, fmt.Errorf("fake: unexpected call %d (%s); only %d responses scripted",
			len(g.calls), r.Role, len(g.responses))
	}
	return llm.Res{Text: g.responses[len(g.calls)-1], Model: "fake"}, nil
}

// ModelFor implements llm.Gateway.
func (g *Gateway) ModelFor(c llm.ModelClass) string {
	if c == llm.ClassFast {
		return "fake-fast"
	}
	return "fake-strong"
}
