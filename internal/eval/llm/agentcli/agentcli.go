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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/llm"
)

// ErrNoCLI is returned when no supported agent CLI is on PATH.
var ErrNoCLI = errors.New("agentcli: no supported agent CLI found on PATH")

// defaultTimeout bounds a single gateway call.
const defaultTimeout = 3 * time.Minute

// waitDelay bounds how long Wait keeps draining the CLI's stdout/stderr
// pipes after the context is cancelled or the process exits on its own. A
// shell script's external commands (any real one shells out to something)
// run as further children; killing only the direct process — as
// exec.CommandContext's default cancellation does — leaves an orphaned
// grandchild holding those pipes open, and Wait blocks past the configured
// Timeout waiting for EOF that never comes. WaitDelay forces Wait to give up
// and return regardless. setupProcessGroup (platform-specific) additionally
// tries to kill that grandchild outright rather than merely stop waiting for
// it.
const waitDelay = 5 * time.Second

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
//
// The CLI also reports a subtype field, deliberately not decoded here: a
// real transient failure was observed with subtype "success" while is_error
// was true and the failure text sat in result. Trusting subtype would have
// handed that error text to the JSON extractor as if it were a legitimate
// response. is_error and the process exit code are the only signals this
// package treats as authoritative.
type envelope struct {
	IsError    bool                       `json:"is_error"`
	Result     string                     `json:"result"`
	ModelUsage map[string]json.RawMessage `json:"modelUsage"`
}

// cliFailure is returned when the envelope or the process itself reports
// failure. exitedNonZero records whether the process exited non-zero, which
// distinguishes an actual infrastructure fault from a policy refusal the CLI
// still treats as a handled, successful invocation (and so exits zero for) —
// the two need different retry treatment, and only cliFailure carries enough
// information for isTransient to tell them apart.
type cliFailure struct {
	msg           string
	exitedNonZero bool
}

func (e *cliFailure) Error() string { return e.msg }

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
	cmd.WaitDelay = waitDelay
	setupProcessGroup(cmd)

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

	// is_error is the authority, not subtype (see the envelope doc comment).
	if env.IsError || runErr != nil {
		return llm.Res{}, &cliFailure{
			msg:           fmt.Sprintf("agentcli: call failed: %s", strings.TrimSpace(env.Result)),
			exitedNonZero: runErr != nil,
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

// apiErrorPattern anchors on the shape observed for the CLI's own
// infrastructure failures: "API Error: <status> ...". Anchoring on this
// shape, rather than scanning arbitrary prose for words like "timeout" or
// "503", avoids retrying a refusal that merely discusses those numbers in
// unrelated text.
var apiErrorPattern = regexp.MustCompile(`API Error:\s*(\d{3})`)

// transientMarkers catches transport-level failures that do not go through
// the "API Error: <status>" shape.
var transientMarkers = []string{
	"ECONNRESET",
	"context deadline exceeded",
	"connection reset",
	"connection refused",
}

// isTransient reports whether a failure is worth retrying rather than
// returned immediately. The CLI gives no structured error code, so this
// remains a heuristic calibrated to phrasing observed from the real CLI, not
// an exhaustive classifier: a refusal that happens to mention a status code
// or the word "timeout" in its own prose must not match, or a non-retryable
// answer gets retried MaxRetries times for no benefit.
func isTransient(err error) bool {
	var cf *cliFailure
	if !errors.As(err, &cf) {
		return false
	}

	// A process that exited non-zero alongside a reported failure is
	// stronger evidence of an infrastructure fault than any word match — a
	// refusal the CLI still treats as a handled, successful invocation exits
	// zero.
	if cf.exitedNonZero {
		return true
	}

	if m := apiErrorPattern.FindStringSubmatch(cf.msg); m != nil {
		if code, cerr := strconv.Atoi(m[1]); cerr == nil && (code == 429 || (code >= 500 && code < 600)) {
			return true
		}
	}

	for _, marker := range transientMarkers {
		if strings.Contains(cf.msg, marker) {
			return true
		}
	}
	return false
}
