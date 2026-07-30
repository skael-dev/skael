// Package agentcli implements the LLM gateway by shelling out to a coding-agent
// CLI in print mode, so every model call is billed to the operator's existing
// subscription rather than requiring an API key.
//
// Every flag the CLI needs lives in this file. CLI churn is a known risk, and
// the fake-binary test in this package is what pins the flag set.
package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/llm"
)

// ErrNoCLI is returned when no supported agent CLI is on PATH.
var ErrNoCLI = errors.New("agentcli: no supported agent CLI found on PATH")

// defaultTimeout bounds a single gateway call.
const defaultTimeout = 3 * time.Minute

// preamble is prepended to every prompt. The CLI is a coding agent by default,
// so it has to be told it is being used as a text completion endpoint.
const preamble = "You are being called as a JSON completion endpoint. " +
	"Use no tools. Do not read or write files. " +
	"Reply with a single JSON value and nothing else — no prose, no code fence.\n\n"

// disallowedTools is belt-and-braces alongside the preamble. A gateway call
// that can execute Bash is a gateway call that a prompt injection can turn into
// arbitrary execution on the host, outside any sandbox.
var disallowedTools = []string{
	"Bash", "Read", "Write", "Edit", "NotebookEdit", "Task",
	"WebFetch", "WebSearch", "Skill", "Glob", "Grep",
}

// Options configures the gateway.
type Options struct {
	Binary      string
	StrongModel string
	FastModel   string
	Cache       llm.Cache
	Timeout     time.Duration
	// MaxRetries bounds retries of transient API errors. Transient upstream
	// overload is common enough that zero retries will lose a long eval.
	MaxRetries int
	// Sleep is the backoff hook, overridden in tests so they do not wait.
	Sleep func(time.Duration)
}

// Gateway is a subscription-backed LLM gateway.
type Gateway struct {
	opts Options
}

// envelope is the single JSON object `--output-format json` emits.
type envelope struct {
	Subtype    string                     `json:"subtype"`
	IsError    bool                       `json:"is_error"`
	Result     string                     `json:"result"`
	ModelUsage map[string]json.RawMessage `json:"modelUsage"`
}

// Detect finds a supported agent CLI on PATH.
func Detect() (string, error) {
	for _, name := range []string{"claude", "cursor-agent"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", ErrNoCLI
}

// New returns a gateway backed by the given CLI.
func New(o Options) (*Gateway, error) {
	if o.Binary == "" {
		p, err := Detect()
		if err != nil {
			return nil, err
		}
		o.Binary = p
	}
	if _, err := os.Stat(o.Binary); err != nil {
		return nil, fmt.Errorf("agentcli.New: %w", err)
	}
	if o.Timeout == 0 {
		o.Timeout = defaultTimeout
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	return &Gateway{opts: o}, nil
}

// Complete implements llm.Gateway.
func (g *Gateway) Complete(ctx context.Context, r llm.Req) (llm.Res, error) {
	key := llm.CacheKey(r)

	if g.opts.Cache != nil {
		if v, ok, err := g.opts.Cache.Get(key); err == nil && ok {
			return llm.Res{Text: v, Model: g.modelFor(r.ModelClass), Cached: true}, nil
		}
	}

	var lastErr error
	for attempt := 0; attempt <= g.opts.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s.
			g.opts.Sleep(time.Duration(1<<(attempt-1)) * time.Second)
		}

		res, err := g.run(ctx, r)
		if err == nil {
			if g.opts.Cache != nil {
				_ = g.opts.Cache.Put(key, res.Text)
			}
			return res, nil
		}
		lastErr = err

		// Only transient upstream failures are worth another session.
		if ctx.Err() != nil || !isTransient(err) {
			return llm.Res{}, err
		}
	}
	return llm.Res{}, fmt.Errorf("agentcli: giving up after %d retries: %w", g.opts.MaxRetries, lastErr)
}

func (g *Gateway) run(ctx context.Context, r llm.Req) (llm.Res, error) {
	ctx, cancel := context.WithTimeout(ctx, g.opts.Timeout)
	defer cancel()

	model := g.modelFor(r.ModelClass)
	prompt := preamble + r.Prompt
	if len(r.Schema) > 0 {
		prompt += "\n\nThe JSON must validate against this schema:\n" + string(r.Schema)
	}

	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--disallowed-tools", strings.Join(disallowedTools, ","),
	}
	if model != "" {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, g.opts.Binary, args...)
	cmd.Stdin = bytes.NewReader(nil) // the CLI waits on stdin otherwise
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	// The envelope is parsed even on a non-zero exit, because the error detail
	// is inside it rather than on stderr.
	var env envelope
	if uerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &env); uerr != nil {
		if runErr != nil {
			return llm.Res{}, fmt.Errorf("agentcli: %s failed: %w (stderr: %s)",
				g.opts.Binary, runErr, strings.TrimSpace(stderr.String()))
		}
		return llm.Res{}, fmt.Errorf("agentcli: unparseable envelope: %w (stdout: %.200s)", uerr, stdout.String())
	}

	// is_error is the authority, not subtype. A transient upstream failure was
	// observed reporting subtype "success" with is_error true and the error
	// message in result; trusting subtype would parse that text as a response.
	if env.IsError || runErr != nil {
		return llm.Res{}, fmt.Errorf("agentcli: call failed: %s", strings.TrimSpace(env.Result))
	}

	res := llm.Res{Text: env.Result, Model: model}
	if res.Model == "" {
		for m := range env.ModelUsage {
			res.Model = m
			break
		}
	}
	return res, nil
}

func (g *Gateway) modelFor(c llm.ModelClass) string {
	if c == llm.ClassFast {
		return g.opts.FastModel
	}
	return g.opts.StrongModel
}

// isTransient reports whether an error is worth retrying. Upstream overload and
// rate limiting resolve on their own; a refusal or a bad flag will not.
func isTransient(err error) bool {
	s := err.Error()
	for _, marker := range []string{"529", "overloaded", "Overloaded", "rate limit", "timeout", "502", "503", "504"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}
