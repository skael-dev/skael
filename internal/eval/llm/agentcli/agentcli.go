// Package agentcli implements the LLM gateway by shelling out to a coding-agent
// CLI in print mode, billing to the operator's existing subscription.
package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/llm"
)

// ErrNoCLI is returned when no supported agent CLI is on PATH.
var ErrNoCLI = errors.New("agentcli: no supported agent CLI found on PATH")

const defaultTimeout = 3 * time.Minute

// waitDelay bounds how long Wait drains pipes after process exit. Without it,
// an orphaned grandchild holding stdout open blocks Wait indefinitely.
const waitDelay = 5 * time.Second

const preamble = "You are being called as a JSON completion endpoint. " +
	"Use no tools. Do not read or write files. " +
	"Reply with a single JSON value and nothing else — no prose, no code fence.\n\n"

// disallowedTools prevents a prompt injection from reaching the host
// through the CLI's own tool set.
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
	MaxRetries  int
	Sleep       func(time.Duration)
}

// Gateway is a subscription-backed LLM gateway.
type Gateway struct {
	opts Options
}

// envelope is the JSON object `--output-format json` emits.
//
// The subtype field is deliberately not decoded: a transient failure was
// observed with subtype "success" while is_error was true. Only is_error
// and the exit code are authoritative.
type envelope struct {
	IsError    bool                       `json:"is_error"`
	Result     string                     `json:"result"`
	ModelUsage map[string]json.RawMessage `json:"modelUsage"`
}

type cliFailure struct {
	msg    string
	result string
}

func (e *cliFailure) Error() string { return e.msg }

type execFailure struct {
	msg    string
	stderr string
}

func (e *execFailure) Error() string { return e.msg }

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

		if ctx.Err() != nil || !isTransient(err) {
			return llm.Res{}, err
		}
	}
	return llm.Res{}, fmt.Errorf("agentcli: giving up after %d retries: %w", g.opts.MaxRetries, lastErr)
}

func (g *Gateway) run(ctx context.Context, r llm.Req) (llm.Res, error) {
	// parent is kept so caller cancellation (Ctrl-C) is distinguishable
	// from this gateway's own deadline (llm.ErrTimeout).
	parent := ctx
	ctx, cancel := context.WithTimeout(parent, g.opts.Timeout)
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
	cmd.Stdin = bytes.NewReader(nil)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay
	setupProcessGroup(cmd)

	runErr := cmd.Run()

	var env envelope
	if uerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &env); uerr != nil {
		if runErr != nil {
			if parent.Err() != nil {
				return llm.Res{}, fmt.Errorf("agentcli: %s: %w", g.opts.Binary, parent.Err())
			}
			if ctx.Err() != nil {
				return llm.Res{}, fmt.Errorf("agentcli: %s produced no response within %s: %w",
					g.opts.Binary, g.opts.Timeout, llm.ErrTimeout)
			}
			stderrText := strings.TrimSpace(stderr.String())
			return llm.Res{}, &execFailure{
				msg:    fmt.Sprintf("agentcli: %s failed: %v (stderr: %s)", g.opts.Binary, runErr, stderrText),
				stderr: stderrText,
			}
		}
		return llm.Res{}, fmt.Errorf("agentcli: unparseable envelope: %w (stdout: %.200s)", uerr, stdout.String())
	}

	if env.IsError || runErr != nil {
		result := strings.TrimSpace(env.Result)
		return llm.Res{}, &cliFailure{
			msg:    fmt.Sprintf("agentcli: call failed: %s", result),
			result: result,
		}
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

// ModelFor implements llm.Gateway. Returns "" when no model was configured,
// meaning the CLI picks its own default and we cannot observe which.
func (g *Gateway) ModelFor(c llm.ModelClass) string {
	return g.modelFor(c)
}

// apiErrorPattern matches "API Error: <status>" at the start of a CLI result.
// Anchored at the start because a refusal's prose can quote the same phrase.
var apiErrorPattern = regexp.MustCompile(`^API Error:\s*(\d{3})`)

var transientMarkers = []string{
	"ECONNRESET",
	"context deadline exceeded",
	"connection reset",
	"connection refused",
}

// isTransient reports whether a failure is worth retrying.
//   - cliFailure: transient only when result begins with "API Error: <5xx|429>".
//   - execFailure: transient only when stderr contains a transport marker.
func isTransient(err error) bool {
	var cf *cliFailure
	if errors.As(err, &cf) {
		if m := apiErrorPattern.FindStringSubmatch(cf.result); m != nil {
			if code, cerr := strconv.Atoi(m[1]); cerr == nil && (code == 429 || (code >= 500 && code < 600)) {
				return true
			}
		}
		return false
	}

	var ef *execFailure
	if errors.As(err, &ef) {
		for _, marker := range transientMarkers {
			if strings.Contains(ef.stderr, marker) {
				return true
			}
		}
		return false
	}

	return false
}
