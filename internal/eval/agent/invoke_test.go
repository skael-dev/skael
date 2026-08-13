package agent_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/agent"
)

type fakeExec struct {
	argv   []string
	stdout string
	exit   int
	err    error
}

func (f *fakeExec) Exec(_ context.Context, argv []string, stdout, _ io.Writer) (int, error) {
	f.argv = argv
	_, _ = io.WriteString(stdout, f.stdout)
	return f.exit, f.err
}

func TestArgv_PinsTheFlagsAnEvaluationRunNeeds(t *testing.T) {
	a, err := agent.Argv(agent.InvokeSpec{
		Prompt: "Convert data.csv to out/tables.md", Model: "opus",
	})
	if err != nil {
		t.Fatalf("Argv: %v", err)
	}
	joined := strings.Join(a, " ")
	// stream-json is what carries individual tool calls, and tier A event
	// fidelity is what the drift engine reads. Losing it degrades every
	// contract check to "the agent said something".
	for _, want := range []string{"-p", "--output-format stream-json", "--verbose"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q: %v", want, a)
		}
	}
	// Asserted against Caps().ModelFlag, not the literal "--model": a second
	// hardcoded "--model" in Argv would pass a literal-string check just as
	// well as the real thing, and is exactly the drift Caps().ModelFlag
	// exists to prevent.
	if wantFlag := agent.New().Caps().ModelFlag + " opus"; !strings.Contains(joined, wantFlag) {
		t.Errorf("argv missing %q (from Caps().ModelFlag): %v", wantFlag, a)
	}
	if a[0] != "claude" {
		t.Errorf("argv[0] = %q, want the CLI name; the sandbox resolves it on PATH inside the image", a[0])
	}
	// The prompt is one argument, never interpolated into a shell string: a
	// task prompt is model-authored text and will contain quotes.
	if a[len(a)-1] != "Convert data.csv to out/tables.md" {
		t.Errorf("prompt is not the final single argument: %v", a)
	}
}

// The permission mode has to clear Bash, not just file edits. It was
// acceptEdits, which auto-approves Edit and Write but still prompts for Bash —
// and a headless session has nobody to prompt, so every shell command came
// back denied. A skill whose whole job is running scripts/*.py then scored
// zero on every task while looking like a skill that simply did not work.
// bypassPermissions is the correct mode here because the isolation is the
// sandbox's job: a container with no network beyond a proxy allowlist, thrown
// away after the run.
func TestArgv_PermissionModeClearsShellNotJustEdits(t *testing.T) {
	a, err := agent.Argv(agent.InvokeSpec{Prompt: "convert it", Model: "opus"})
	if err != nil {
		t.Fatalf("Argv: %v", err)
	}
	joined := strings.Join(a, " ")
	if strings.Contains(joined, "acceptEdits") {
		t.Error("argv still asks for acceptEdits, which denies every Bash call in a headless session")
	}
	if !strings.Contains(joined, "--permission-mode bypassPermissions") {
		t.Errorf("argv does not request bypassPermissions: %v", a)
	}
}

func TestArgv_RequiresAPromptAndAModel(t *testing.T) {
	if _, err := agent.Argv(agent.InvokeSpec{Model: "opus"}); err == nil {
		t.Error("Argv accepted an empty prompt")
	}
	// A run with no model is a run whose score cannot be attributed to a panel
	// member, which makes the whole matrix meaningless.
	if _, err := agent.Argv(agent.InvokeSpec{Prompt: "x"}); err == nil {
		t.Error("Argv accepted an empty model")
	}
}

func TestInvoke_RunsThroughTheExecutorAndReturnsItsStream(t *testing.T) {
	fx := &fakeExec{stdout: `{"type":"system","subtype":"init"}` + "\n"}
	r, err := agent.New().Invoke(context.Background(), agent.InvokeSpec{
		Prompt: "p", Model: "opus", Exec: fx,
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	b, _ := io.ReadAll(r)
	if !strings.Contains(string(b), `"subtype":"init"`) {
		t.Errorf("stream = %q, want the executor's stdout verbatim", b)
	}
	if len(fx.argv) == 0 {
		t.Error("Invoke did not go through the executor")
	}
}

func TestInvoke_RefusesToRunOutsideASandbox(t *testing.T) {
	_, err := agent.New().Invoke(context.Background(), agent.InvokeSpec{Prompt: "p", Model: "m"})
	// An adapter that falls back to exec.Command runs an untrusted skill on the
	// host. Failing closed is the only correct behaviour, and it is worth a
	// sentinel so a caller cannot mistake it for a CLI problem.
	if !errors.Is(err, agent.ErrNoExecutor) {
		t.Errorf("err = %v, want ErrNoExecutor", err)
	}
}

func TestInvoke_SurfacesANonZeroExitWithItsOutput(t *testing.T) {
	fx := &fakeExec{exit: 1, stdout: "API Error: 529"}
	_, err := agent.New().Invoke(context.Background(), agent.InvokeSpec{
		Prompt: "p", Model: "m", Exec: fx,
	})
	if err == nil || !strings.Contains(err.Error(), "529") {
		t.Errorf("err = %v, want the CLI's own output quoted", err)
	}
}
