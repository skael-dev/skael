package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/score"
)

func parseFixture(t *testing.T) *agent.Result {
	t.Helper()
	f, err := os.Open("testdata/opencode-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res, err := agent.New().Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return res
}

// The fixture is a real OpenCode 1.19.5 session that loads a skill, reads a
// file, writes one and runs a command. A parser that drifts from the real
// stream measures nothing and still reports a score.
func TestParse_MapsARealSessionOntoTheNormalizedEvents(t *testing.T) {
	res := parseFixture(t)
	var kinds []string
	var shellExit *int
	for _, e := range res.Events {
		switch e.Type {
		case agent.TypeFileRead, agent.TypeFileWrite, agent.TypeShell:
			kinds = append(kinds, string(e.Type)+":"+strings.Join(e.Paths, ","))
			if e.Type == agent.TypeShell {
				shellExit = e.ExitCode
			}
		case agent.TypeToolCall:
			kinds = append(kinds, e.Name+":"+strings.Join(e.Paths, ","))
		}
	}
	want := []string{"Skill:greeting-format/SKILL.md", "file_read:/w/notes.txt", "file_write:/w/greeting.txt", "shell:"}
	if !slices.Equal(kinds, want) {
		t.Errorf("events = %v, want %v", kinds, want)
	}
	if shellExit == nil || *shellExit != 0 {
		t.Errorf("shell exit = %v, want 0", shellExit)
	}
	if res.Meta.NumTurns != 4 {
		t.Errorf("NumTurns = %d, want 4", res.Meta.NumTurns)
	}
	if res.Meta.CostUSD <= 0 || res.Meta.InputTokens == 0 || res.Meta.OutputTokens == 0 {
		t.Errorf("cost %.6f, tokens in %d out %d: want all positive", res.Meta.CostUSD, res.Meta.InputTokens, res.Meta.OutputTokens)
	}
	if res.Meta.AgentVersion != agent.OpenCodeVersion || res.Meta.DurationMS <= 0 || res.Meta.IsError {
		t.Errorf("meta = %+v", res.Meta)
	}
	for i, e := range res.Events {
		if e.Seq != i+1 || e.TS.IsZero() {
			t.Fatalf("event %d: seq %d ts %v", i, e.Seq, e.TS)
		}
	}
}

// The skill tool is an explicit invocation, not an inferred read.
func TestParse_SkillToolCountsAsAnExplicitFiring(t *testing.T) {
	res := parseFixture(t)
	fired, inferred := score.DetectFiring("greeting-format", agent.New().Caps(), res.Events)
	if !fired || inferred {
		t.Errorf("DetectFiring = fired %v inferred %v, want fired and explicit", fired, inferred)
	}
}

const badModelStream = `{"type":"error","timestamp":1794327525809,"sessionID":"s","error":{"name":"APIError","data":{"message":"nope/not-a-model is not a valid model ID","statusCode":400,"isRetryable":false}}}` + "\n"

func TestParse_CarriesTheProvidersErrorMessage(t *testing.T) {
	res, err := agent.New().Parse(strings.NewReader(badModelStream))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Meta.IsError || res.Meta.Error != "nope/not-a-model is not a valid model ID (HTTP 400)" {
		t.Errorf("meta = %+v", res.Meta)
	}
}

type fakeExec struct {
	code   int
	stdout string
	argv   []string
}

func (f *fakeExec) Exec(_ context.Context, argv []string, stdout, _ io.Writer) (int, error) {
	f.argv = argv
	_, _ = io.WriteString(stdout, f.stdout)
	return f.code, nil
}

// A failed session names the provider's reason, which is what a person reads
// in the job view to tell a bad model id from a bad key.
func TestInvoke_ReportsTheProvidersReasonForAFailedSession(t *testing.T) {
	ex := &fakeExec{code: 1, stdout: badModelStream}
	_, err := agent.New().Invoke(context.Background(), agent.InvokeSpec{Prompt: "hi", Model: "nope/not-a-model", Exec: ex})
	if err == nil || !strings.Contains(err.Error(), "not a valid model ID") {
		t.Fatalf("Invoke = %v, want the provider's message", err)
	}
	want := []string{"opencode", "run", "--format", "json", "--auto", "-m", "skael/nope/not-a-model", "hi"}
	if !slices.Equal(ex.argv, want) {
		t.Errorf("argv = %q, want %q", ex.argv, want)
	}
}

func TestInvoke_RefusesToRunOutsideASandbox(t *testing.T) {
	if _, err := agent.New().Invoke(context.Background(), agent.InvokeSpec{Prompt: "p", Model: "m"}); err != agent.ErrNoExecutor {
		t.Fatalf("Invoke = %v, want ErrNoExecutor", err)
	}
}

func sessionConfig(t *testing.T, b agent.Backend) (map[string]any, map[string]string) {
	t.Helper()
	env, err := agent.SessionEnv(b)
	if err != nil {
		t.Fatalf("SessionEnv: %v", err)
	}
	vars := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(vars["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil {
		t.Fatalf("config: %v", err)
	}
	p := cfg["provider"].(map[string]any)["skael"].(map[string]any)
	return p, vars
}

// Each provider type maps to the SDK package and endpoint verified against a
// real OpenCode session; the key never appears in the config itself.
func TestSessionEnv_MapsEachProviderType(t *testing.T) {
	cases := []struct {
		b                agent.Backend
		pkg, baseURL     string
		apiKey, headerOK bool
	}{
		{agent.Backend{Type: "anthropic", Key: "k", Model: "claude-sonnet-5"}, "@ai-sdk/anthropic", "https://api.anthropic.com/v1", true, false},
		{agent.Backend{Type: "anthropic", BaseURL: "https://r.services.ai.azure.com/anthropic/", Key: "k", Model: "m"}, "@ai-sdk/anthropic", "https://r.services.ai.azure.com/anthropic/v1", true, false},
		{agent.Backend{Type: "openrouter", Key: "k", Model: "deepseek/deepseek-v4.1-flash"}, "@ai-sdk/openai-compatible", "https://openrouter.ai/api/v1", true, false},
		{agent.Backend{Type: "openai", Key: "k", Model: "gpt-5"}, "@ai-sdk/openai-compatible", "https://api.openai.com/v1", true, false},
		{agent.Backend{Type: "openai-compatible", BaseURL: "https://r.openai.azure.com/openai/v1", AuthHeader: "api-key", Key: "k", Model: "my-deployment"}, "@ai-sdk/openai-compatible", "https://r.openai.azure.com/openai/v1", false, true},
	}
	for _, c := range cases {
		p, vars := sessionConfig(t, c.b)
		opts := p["options"].(map[string]any)
		if p["npm"] != c.pkg || opts["baseURL"] != c.baseURL {
			t.Errorf("%s: npm %v base %v, want %s %s", c.b.Type, p["npm"], opts["baseURL"], c.pkg, c.baseURL)
		}
		_, hasKey := opts["apiKey"]
		headers, _ := opts["headers"].(map[string]any)
		if hasKey != c.apiKey || (headers["api-key"] != nil) != c.headerOK {
			t.Errorf("%s: apiKey %v headers %v", c.b.Type, hasKey, headers)
		}
		if strings.Contains(vars["OPENCODE_CONFIG_CONTENT"], `"k"`) || vars["SKAEL_PROVIDER_KEY"] != "k" {
			t.Errorf("%s: the key must travel only in SKAEL_PROVIDER_KEY", c.b.Type)
		}
		if _, ok := p["models"].(map[string]any)[c.b.Model]; !ok {
			t.Errorf("%s: model %s not declared", c.b.Type, c.b.Model)
		}
	}
}

func TestSessionEnv_RefusesABackendWithNoKey(t *testing.T) {
	if _, err := agent.SessionEnv(agent.Backend{Type: "openrouter", Model: "m"}); err == nil {
		t.Fatal("want an error for a provider with no key")
	}
}
