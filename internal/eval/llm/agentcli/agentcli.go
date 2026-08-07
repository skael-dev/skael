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

// cliFailure is returned when the envelope parsed successfully but reports
// failure (is_error, or a non-zero exit alongside a valid envelope). result
// is the trimmed CLI result text, kept separate from msg so isTransient can
// anchor on where the CLI's own text actually begins rather than on a string
// that already has an "agentcli: call failed: " prefix glued to the front
// of it.
type cliFailure struct {
	msg    string
	result string
}

func (e *cliFailure) Error() string { return e.msg }

// execFailure is returned when the process itself failed and stdout held no
// parseable envelope at all — a crash, an unrecognised flag rejected before
// any JSON was written, or a dropped connection. There is no envelope.Result
// to inspect on this path; a transport diagnostic, if the failure produced
// one, appears on stderr instead. stderr is kept separately from msg for the
// same reason cliFailure keeps result separate: isTransient needs the raw
// text, not a string with our own formatting glued on.
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
	// parent is kept so a caller cancelling (Ctrl-C, SIGTERM) can still be
	// told apart from this gateway's own deadline firing: both cancel ctx,
	// but only one of them is llm.ErrTimeout.
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
	cmd.Stdin = bytes.NewReader(nil) // the CLI waits on stdin otherwise
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay
	setupProcessGroup(cmd)

	runErr := cmd.Run()

	// The envelope is parsed even on a non-zero exit, because when it does
	// parse, the error detail is inside it rather than on stderr.
	var env envelope
	if uerr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &env); uerr != nil {
		if runErr != nil {
			// A SIGKILL from either cancellation source produces the same
			// process-level error (see setupProcessGroup), so the two contexts
			// are what distinguish them — check parent first, since ctx is
			// derived from it and reports cancelled too once parent is.
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

	// is_error is the authority, not subtype (see the envelope doc comment).
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

// ModelFor implements llm.Gateway. When StrongModel/FastModel were left
// unset, run passes no --model flag (see run) and the CLI picks its own
// default — this gateway never observes what that default resolved to, so it
// returns "" rather than guess. It only reports a model when the operator
// configured one explicitly.
func (g *Gateway) ModelFor(c llm.ModelClass) string {
	return g.modelFor(c)
}

// apiErrorPattern anchors on the shape observed for the CLI's own
// infrastructure failures: the result *beginning with* "API Error: <status>".
// Anchoring at the start, rather than searching for the phrase anywhere in
// the text, matters because the live observation was the result equal to
// "API Error: 529 Overloaded. This is a server-side issue...", not an error
// mentioned partway through a longer answer — a refusal's prose can quote or
// discuss that exact phrase (e.g. "our docs say 'API Error: 503' means...")
// without the CLI itself having failed, and only the anchored form tells the
// two apart.
var apiErrorPattern = regexp.MustCompile(`^API Error:\s*(\d{3})`)

// transientMarkers catches transport-level failures. These only ever surface
// on execFailure's stderr (see isTransient): a cliFailure means the envelope
// parsed, and no case has been found where the real CLI reports a transport
// error inside an otherwise-valid envelope rather than either the anchored
// "API Error: <status>" shape or crashing outright with nothing parseable on
// stdout. An earlier version of this classifier also scanned cliFailure's
// result for these markers; it was removed after review showed every test
// in this package passed whether that branch returned true or false — dead
// code shaped like a safety net, covering a case that could not be
// triggered. If the real CLI is ever observed emitting one of these inside a
// parsed envelope's result, add it back here with a test that pins the
// observation, the same way apiErrorPattern was pinned.
// "context deadline exceeded" here is the child CLI's own HTTP timeout on its
// stderr, which is retryable — not this gateway's deadline, which is
// llm.ErrTimeout and never is.
var transientMarkers = []string{
	"ECONNRESET",
	"context deadline exceeded",
	"connection reset",
	"connection refused",
}

// isTransient reports whether a failure is worth retrying rather than
// returned immediately. The CLI gives no structured error code, so this
// remains a heuristic calibrated to phrasing observed from the real CLI, not
// an exhaustive classifier, and it looks for a transient signal in a
// different place depending on which of the two failure shapes it was
// handed:
//
//   - cliFailure (the envelope parsed): transient only when result *begins*
//     with "API Error: <5xx-or-429>" — the exact shape observed for real
//     infrastructure failures. A refusal that merely mentions a status code,
//     "timeout", or a quoted copy of that phrase partway through its own
//     prose must not match, or a non-retryable answer gets retried
//     MaxRetries times for no benefit. A non-zero exit is not consulted here
//     at all: the CLI also exits non-zero for a permanent misconfiguration
//     (bad --model, bad flag, expired auth), so exit code alone proves
//     nothing about transience.
//   - execFailure (no parseable envelope — a crash, a dropped connection, or
//     a rejected flag): transient only when stderr contains one of
//     transientMarkers. A bad flag lands on this same path (the CLI prints
//     usage to stderr and exits non-zero) and must not match, so this is a
//     positive check for an actual transport signal, not "unparseable plus
//     non-zero exit implies transient".
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
