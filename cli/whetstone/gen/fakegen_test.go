package gen_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
)

// genRoles scripts a generation by role rather than by call order. Generate
// starts the outline/body chain, the resources pass and the description pass
// together, so no fixed call order exists to script against.
//
// Body and Description are queues: the first entry answers the pass itself,
// each later entry answers one revision call.
type genRoles struct {
	Outline     string
	Body        []string
	Description []string
	// Resource answers one resources-pass call, by planned path.
	Resource func(path string) string
}

const (
	defaultOutline = `{"sections":["Overview","Steps","Failure handling"]}`
	defaultBody    = `{"body":"# PDF Extract\n\n1. Run ` + "`scripts/extract.py <input.pdf>`" +
		`. Postcondition: out/tables.csv exists.\n2. Run ` + "`scripts/validate.py out/tables.csv`" +
		`. Postcondition: exits 0.\n\nIf a checkpoint cannot be satisfied after one retry, stop and report state.\n"}`
	defaultDescription = `{"description":"Extracts tables from PDF files into CSV. Use when the user mentions a PDF, a report, or table extraction."}`
	defaultResource    = `{"content":"#!/usr/bin/env python3\n\"\"\"Extract tables.\"\"\"\nimport sys\nif \"--help\" in sys.argv:\n    print(__doc__); sys.exit(0)\n"}`
)

// genFake returns a gateway answering r, with a usable default for anything r
// leaves unset.
func genFake(t *testing.T, r genRoles) *fake.Gateway {
	t.Helper()
	if r.Outline == "" {
		r.Outline = defaultOutline
	}
	if len(r.Body) == 0 {
		r.Body = []string{defaultBody}
	}
	if len(r.Description) == 0 {
		r.Description = []string{defaultDescription}
	}
	if r.Resource == nil {
		r.Resource = func(string) string { return defaultResource }
	}

	var mu sync.Mutex
	pop := func(q *[]string, role string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if len(*q) == 0 {
			return "", fmt.Errorf("fake: no scripted response left for %s", role)
		}
		next := (*q)[0]
		*q = (*q)[1:]
		return next, nil
	}

	return fake.NewFunc(func(req llm.Req) (string, error) {
		switch {
		case req.Role == "gen.outline":
			return r.Outline, nil
		case req.Role == "gen.body", req.Role == "gen.revise.body":
			return pop(&r.Body, req.Role)
		case req.Role == "gen.description", req.Role == "gen.revise.description":
			return pop(&r.Description, req.Role)
		case strings.HasPrefix(req.Role, "gen.resources:"):
			return r.Resource(strings.TrimPrefix(req.Role, "gen.resources:")), nil
		}
		return "", fmt.Errorf("fake: unscripted role %q", req.Role)
	})
}

// callsByRole indexes a fake's requests by role. Roles are unique per
// generation except for the revision passes, which are asserted by presence.
func callsByRole(g *fake.Gateway) map[string]llm.Req {
	out := map[string]llm.Req{}
	for _, c := range g.Calls() {
		out[c.Role] = c
	}
	return out
}
