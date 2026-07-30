package gen_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/gen"
	"github.com/skael-dev/skael/internal/eval/llm/fake"
)

// TestGenerate_PassOrderAndRoles asserts the four gateway calls happen in the
// documented order — outline, body, resources, description — tagged with the
// matching Role, and that only the body pass carries the robustness rules.
// A regression that swapped two passes (e.g. resources before body) would
// still make exactly four calls and could still pass TestGenerate_WritesABundle
// against this scripted fixture, so call count alone does not catch it; the
// Role sequence does.
func TestGenerate_PassOrderAndRoles(t *testing.T) {
	g := fake.New(scripted()...)
	if _, err := gen.Generate(context.Background(), g, genSpec(), t.TempDir()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	calls := g.Calls()
	if len(calls) != 4 {
		t.Fatalf("made %d gateway calls, want 4", len(calls))
	}

	wantRoles := []string{"gen.outline", "gen.body", "gen.resources", "gen.description"}
	for i, want := range wantRoles {
		if calls[i].Role != want {
			t.Errorf("call %d has role %q, want %q (pass order must be outline, body, resources, description)",
				i, calls[i].Role, want)
		}
	}

	bodyPrompt := strings.ToLower(calls[1].Prompt)
	if !strings.Contains(bodyPrompt, "postcondition") || !strings.Contains(bodyPrompt, "500 lines") {
		t.Error("body pass prompt does not carry the robustness rules")
	}

	descPrompt := strings.ToLower(calls[3].Prompt)
	if strings.Contains(descPrompt, "hedge") || strings.Contains(descPrompt, "500 lines") {
		t.Error("description pass prompt unexpectedly carries body robustness-rule language")
	}
}

// TestGenerate_RefusesNestedTraversalResourcePath proves a traversal buried
// mid-path — not just a leading ".." — is also refused. A check that only
// inspects a path's prefix (e.g. strings.HasPrefix(rel, "..")) would miss
// both of these; see TestNaivePrefixCheck_WouldMissNestedTraversal in
// safejoin_internal_test.go for the direct demonstration.
func TestGenerate_RefusesNestedTraversalResourcePath(t *testing.T) {
	for _, evil := range []string{"scripts/../../escape.sh", "./../escape.sh"} {
		t.Run(evil, func(t *testing.T) {
			responses := scripted()
			responses[2] = `{"files":[{"path":"` + evil + `","content":"#!/bin/sh\n"}]}`

			out := t.TempDir()
			if _, err := gen.Generate(context.Background(), fake.New(responses...), genSpec(), out); err == nil {
				t.Fatalf("Generate accepted nested-traversal resource path %q", evil)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(out), "escape.sh")); err == nil {
				t.Error("a file was written outside the bundle directory")
			}
		})
	}
}
