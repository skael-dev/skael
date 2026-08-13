package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/eval/llm/api"
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
	if cfg.Provider.BaseURL != "" || cfg.Provider.AuthStyle != api.AuthStyleAnthropic || len(cfg.Provider.Models) != 0 {
		t.Fatalf("gateway overrides = %+v, want all empty/default when unset", cfg.Provider)
	}
}

// TestConfigFromEnv_GatewayOverrides pins that ANTHROPIC_BASE_URL,
// ANTHROPIC_AUTH_TOKEN and LLM_MODEL resolve onto the worker's provider, so an
// operator can point the judge at an Anthropic-compatible gateway such as
// OpenRouter and name the models it serves.
//
// The bearer header is inferred from the token rather than configured. The
// worker used to read no token at all, so the documented OpenRouter setup
// authenticated with the wrong header — that asymmetry is what this covers.
func TestConfigFromEnv_GatewayOverrides(t *testing.T) {
	t.Setenv("SKAEL_ENDPOINT", "http://localhost:8080")
	t.Setenv("SKAEL_API_KEY", "k")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "sk-or-test")
	t.Setenv("ANTHROPIC_BASE_URL", "https://openrouter.ai/api/v1")
	t.Setenv("LLM_MODEL", "anthropic/claude-opus-4,anthropic/claude-3.5-haiku")

	cfg, err := configFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("BaseURL = %q", cfg.Provider.BaseURL)
	}
	if cfg.Provider.AuthStyle != api.AuthStyleBearer {
		t.Errorf("AuthStyle = %q, want bearer for a token", cfg.Provider.AuthStyle)
	}
	if cfg.Provider.Key != "sk-or-test" {
		t.Errorf("Key = %q, want the token", cfg.Provider.Key)
	}
	want := []string{"anthropic/claude-opus-4", "anthropic/claude-3.5-haiku"}
	if !reflect.DeepEqual(cfg.Provider.Models, want) {
		t.Errorf("Models = %v, want %v", cfg.Provider.Models, want)
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

// TestEvalDepsFrom_CarriesThePanelSplit guards the realRunner -> EvalDeps hop,
// the sibling of the RunInput -> EvalRequest hop above and the same failure
// mode: a field dropped here is not an error but a scored run whose panel
// nobody chose. PanelExcludeEnv is the one that matters most — lose it and the
// sandbox is handed the judge's gateway, so a panel meant to run on a
// subscription silently runs on the gateway instead.
func TestEvalDepsFrom_CarriesThePanelSplit(t *testing.T) {
	r := &realRunner{
		runRoot:         "/var/lib/skael/run",
		panelModels:     []string{"anthropic/claude-sonnet-5"},
		panelBase:       "https://openrouter.ai/api",
		panelExcludeEnv: []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"},
	}

	deps := evalDepsFrom(r, nil)

	if !reflect.DeepEqual(deps.PanelExcludeEnv, r.panelExcludeEnv) {
		t.Fatalf("PanelExcludeEnv = %v, want %v", deps.PanelExcludeEnv, r.panelExcludeEnv)
	}
	if !reflect.DeepEqual(deps.PanelModels, r.panelModels) {
		t.Fatalf("PanelModels = %v, want %v", deps.PanelModels, r.panelModels)
	}
	if deps.PanelBaseURL != r.panelBase {
		t.Fatalf("PanelBaseURL = %q, want %q", deps.PanelBaseURL, r.panelBase)
	}
	if deps.WorkspaceRoot != r.runRoot {
		t.Fatalf("WorkspaceRoot = %q, want %q", deps.WorkspaceRoot, r.runRoot)
	}
}
