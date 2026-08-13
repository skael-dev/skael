package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// Argv is the command line one evaluation session runs. Exported so the flags
// are pinned by a test without needing a CLI or daemon.
//
// --permission-mode bypassPermissions: isolation is the sandbox's job, not the
// CLI's. The narrower acceptEdits prompts for Bash, so a headless session has
// every shell command denied and scores zero with nothing naming the cause.
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

// Invoke runs one headless session and returns the native stream verbatim.
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
