package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/worker"
)

func writeFakeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho fake\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestConfigFromEnv_RefusesWithoutAnAPIGatewayKey(t *testing.T) {
	t.Setenv("SKAEL_ENDPOINT", "http://localhost:8080")
	t.Setenv("SKAEL_API_KEY", "k")
	t.Setenv("ANTHROPIC_API_KEY", "")

	_, err := configFromEnv()
	if err == nil {
		t.Fatal("the worker started without an API gateway key")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("the error does not name the missing variable: %v", err)
	}
}

// A subscription gateway is whetstone's affordance, not the worker's: a
// verified score must come from a metered, reproducible backend.
func TestConfigFromEnv_DoesNotFallBackToAnAgentCLI(t *testing.T) {
	t.Setenv("SKAEL_ENDPOINT", "http://localhost:8080")
	t.Setenv("SKAEL_API_KEY", "k")
	t.Setenv("ANTHROPIC_API_KEY", "")
	dir := t.TempDir()
	writeFakeExecutable(t, filepath.Join(dir, "claude"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, err := configFromEnv(); err == nil {
		t.Fatal("the worker accepted a subscription CLI in place of an API key")
	}
}

func TestConfigFromEnv_RequiresAnEndpointAndKey(t *testing.T) {
	t.Setenv("SKAEL_ENDPOINT", "")
	t.Setenv("SKAEL_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	_, err := configFromEnv()
	if err == nil {
		t.Fatal("the worker started without an endpoint or API key")
	}
	if !strings.Contains(err.Error(), "SKAEL_ENDPOINT") {
		t.Fatalf("the error does not name SKAEL_ENDPOINT: %v", err)
	}
	if !strings.Contains(err.Error(), "SKAEL_API_KEY") {
		t.Fatalf("the error does not name SKAEL_API_KEY: %v", err)
	}
}

func TestConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("SKAEL_ENDPOINT", "http://localhost:8080")
	t.Setenv("SKAEL_API_KEY", "k")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	cfg, err := configFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Lease != 5*time.Minute || cfg.PollInterval != 15*time.Second {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.WorkerID == "" {
		t.Fatal("worker id defaulted to empty; two anonymous workers cannot be told apart in the lease column")
	}
}

// TestEvalRequestFrom_CarriesTierAndPanel guards the RunInput -> EvalRequest
// hop. A prior task's fix round found Panel silently dropped on exactly this
// kind of hand-off; nothing but inspection caught it. This asserts a
// non-default tier and an explicit agent/model panel both survive.
func TestEvalRequestFrom_CarriesTierAndPanel(t *testing.T) {
	in := worker.RunInput{
		JobID:    evalqueue.JobID("job-123"),
		Skill:    "my-skill",
		Version:  3,
		SuiteRef: "sha256:abc",
		Tier:     "thorough",
		Panel: evalqueue.Panel{
			Agents: []string{"claude-code", "codex"},
			Models: []string{"claude-opus-5", "gpt-5"},
		},
	}

	req := evalRequestFrom(in, 4)

	if got, want := string(req.Tier), in.Tier; got != want {
		t.Fatalf("Tier = %q, want %q", got, want)
	}
	if req.Skill != in.Skill {
		t.Fatalf("Skill = %q, want %q", req.Skill, in.Skill)
	}
	if !reflect.DeepEqual(req.Agents, in.Panel.Agents) {
		t.Fatalf("Agents = %v, want %v", req.Agents, in.Panel.Agents)
	}
	if !reflect.DeepEqual(req.Models, in.Panel.Models) {
		t.Fatalf("Models = %v, want %v", req.Models, in.Panel.Models)
	}
	if req.Concurrency != 4 {
		t.Fatalf("Concurrency = %d, want 4", req.Concurrency)
	}
}

// TestEvalRequestFrom_DefaultsConcurrencyToOne guards against a zero or
// negative WORKER_CONCURRENCY silently producing an EvalRequest with no
// panel parallelism at all.
func TestEvalRequestFrom_DefaultsConcurrencyToOne(t *testing.T) {
	req := evalRequestFrom(worker.RunInput{Skill: "s"}, 0)
	if req.Concurrency != 1 {
		t.Fatalf("Concurrency = %d, want 1", req.Concurrency)
	}
}
