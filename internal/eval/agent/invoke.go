package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// Argv is the command line one evaluation session runs. Exported so the flags
// are pinned by a test that needs neither a CLI nor a daemon: CLI churn is the
// known risk, and a silently-dropped --output-format degrades every trajectory
// to "the agent said something" without failing anything.
//
// --verbose accompanies stream-json because the CLI requires it for streaming
// output in print mode.
//
// --permission-mode bypassPermissions is what lets a skill do file *and* shell
// work without a prompt no one can answer: the isolation is the sandbox's job,
// not the CLI's, and a session that stops to ask is a session scored as a
// failure for the wrong reason. It must not be narrowed back to acceptEdits,
// which was here first and looks safer while being strictly worse — that mode
// clears Edit and Write but still prompts for Bash, so a headless session had
// every shell command denied, and any skill that runs a script scored zero on
// every task with nothing in the report naming the cause.
func Argv(s InvokeSpec) ([]string, error) {
	if strings.TrimSpace(s.Prompt) == "" {
		return nil, errors.New("claudecode: invoke has no prompt")
	}
	if strings.TrimSpace(s.Model) == "" {
		return nil, errors.New("claudecode: invoke has no model; a run with no model cannot be attributed to a panel member")
	}
	return []string{
		"claude", "-p",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		New().Caps().ModelFlag, s.Model,
		s.Prompt,
	}, nil
}

// Invoke runs one headless session through the caller's executor and returns
// the native stream verbatim. Parsing is a separate step so a transcript is
// recorded even when parsing fails.
func (a *ClaudeCode) Invoke(ctx context.Context, s InvokeSpec) (RawStream, error) {
	if s.Exec == nil {
		return nil, ErrNoExecutor
	}
	argv, err := Argv(s)
	if err != nil {
		return nil, err
	}

	var out, errBuf bytes.Buffer
	code, err := s.Exec.Exec(ctx, argv, &out, &errBuf)
	if err != nil {
		return nil, fmt.Errorf("claudecode: session failed: %w\n%s", err, tail(&errBuf))
	}
	if code != 0 {
		return nil, fmt.Errorf("claudecode: session exited %d: %s", code, tail(&out, &errBuf))
	}
	return bytes.NewReader(out.Bytes()), nil
}

// tail quotes the last of what the CLI said. A whole stream in an error message
// is unreadable; the end of it is where the reason is.
func tail(bufs ...*bytes.Buffer) string {
	const max = 512
	var b strings.Builder
	for _, buf := range bufs {
		s := strings.TrimSpace(buf.String())
		if len(s) > max {
			s = "…" + s[len(s)-max:]
		}
		if s != "" {
			b.WriteString(s + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}
