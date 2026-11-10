package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/skael-dev/skael/cli/whetstone"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/worker"
)

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
	// `just check` loads .env, so the ambient environment can carry a gateway
	// this test is asserting the absence of.
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("LLM_MODEL", "")
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
		Contest:  true,
		Attempts: 3,
	}

	req := evalRequestFrom(in, 4, 8)

	if !req.Contest || req.Attempts != 3 {
		t.Fatalf("Contest = %v, Attempts = %d, want true and 3", req.Contest, req.Attempts)
	}

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
	if req.GradeConcurrency != 8 {
		t.Fatalf("GradeConcurrency = %d, want 8", req.GradeConcurrency)
	}
}

// TestEvalRequestFrom_DefaultsConcurrencyToOne guards against a zero or
// negative WORKER_CONCURRENCY silently producing an EvalRequest with no
// panel parallelism at all.
func TestEvalRequestFrom_DefaultsConcurrencyToOne(t *testing.T) {
	req := evalRequestFrom(worker.RunInput{Skill: "s"}, 0, 0)
	if req.Concurrency != 1 {
		t.Fatalf("Concurrency = %d, want 1", req.Concurrency)
	}
	// Each knob falls back to its own default, never to the other's: a zero
	// grade concurrency means whetstone's default, not one judge call.
	if req.GradeConcurrency != 0 {
		t.Fatalf("GradeConcurrency = %d, want 0 so whetstone applies its own default", req.GradeConcurrency)
	}
}

// TestJudgeGatewayOptions_RetriesAreSet guards the defect where the worker's
// judge made exactly one attempt, so one 429 threw away a whole panel run.
func TestJudgeGatewayOptions_RetriesAreSet(t *testing.T) {
	o := judgeGatewayOptions()
	if o.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", o.MaxRetries)
	}
	if o.Timeout != judgeTimeout {
		t.Errorf("Timeout = %s, want %s", o.Timeout, judgeTimeout)
	}
}

// A job claimed while the server has no panel fails naming where to fix it.
func TestForJob_WithNoPanelNamesSettings(t *testing.T) {
	if _, err := forJob(nil); err == nil || !strings.Contains(err.Error(), "Settings → Evaluation") {
		t.Fatalf("forJob(nil) = %v, want it to name Settings → Evaluation", err)
	}
}

// The claim carries each provider's key. Each member's session gets its own
// provider's config and key, and a member whose provider has no key fails the
// job naming that provider.
func TestForJob_GivesEachMemberItsOwnProvider(t *testing.T) {
	eval := &evalqueue.EvalConfig{
		Judge: evalqueue.EvalBackend{Provider: "Anthropic", Type: "anthropic", Credential: "sk-judge", Model: "claude-opus-5-5"},
		Members: []evalqueue.EvalBackend{
			{Provider: "Anthropic", Type: "anthropic", Credential: "sk-judge", Model: "claude-sonnet-5-5"},
			{Provider: "OpenRouter", Type: "openrouter", Credential: "or", Model: "deepseek/deepseek-v4.1-flash"},
		},
	}
	jb, err := forJob(eval)
	if err != nil {
		t.Fatal(err)
	}
	env, ok := jb.memberAuth("opencode", "deepseek/deepseek-v4.1-flash")
	if !ok || !slices.Contains(env, "SKAEL_PROVIDER_KEY=or") || !slices.Contains(env, "SKAEL_PROVIDER_BASE_URL=https://openrouter.ai/api/v1") {
		t.Fatalf("OpenRouter member env = %v", env)
	}
	if env, _ := jb.memberAuth("opencode", "claude-sonnet-5-5"); !slices.Contains(env, "SKAEL_PROVIDER_KEY=sk-judge") {
		t.Fatalf("Anthropic member env = %v", env)
	}
	if got := jb.memberProvider("opencode", "deepseek/deepseek-v4.1-flash"); got != "openrouter" {
		t.Fatalf("OpenRouter member label = %q", got)
	}

	eval.Members[1].Credential = ""
	if _, err := forJob(eval); err == nil || !strings.Contains(err.Error(), "OpenRouter has no key") {
		t.Fatalf("a member without a key must name its provider, got %v", err)
	}
}

// Every evaluation step reaches the server as a job event, with ok set where
// the step can fail.
func TestJobEvent_CarriesTheOutcome(t *testing.T) {
	e := jobEvent(whetstone.Progress{Kind: "session_end", Task: "3", Model: "m", OK: false, Error: "boom", CostUSD: 0.5, Duration: 2 * time.Second})
	if e.OK == nil || *e.OK || e.Error != "boom" || e.CostUSD != 0.5 || e.DurationMS != 2000 {
		t.Fatalf("event = %+v", e)
	}
	if e := jobEvent(whetstone.Progress{Kind: "phase", Phase: "grading"}); e.OK != nil || e.Phase != "grading" {
		t.Fatalf("phase event = %+v", e)
	}
}
