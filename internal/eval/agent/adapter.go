// Package agent defines the interface every coding-agent CLI is driven through.
package agent

import (
	"context"
	"errors"
	"io"
)

// ErrNoExecutor is returned when Invoke is called with no Exec. Failing closed
// is deliberate: an adapter that fell back to exec.Command would run an
// untrusted skill on the host.
var ErrNoExecutor = errors.New("agent: Invoke needs an executor; a session must run in a sandbox")

// Exec runs one command in a sandbox. Adapters build argv and hand it here; they
// never exec on the host. The executor rather than the sandbox, so flags stay in
// the adapter and containers stay out of it.
type Exec interface {
	Exec(ctx context.Context, argv []string, stdout, stderr io.Writer) (exitCode int, err error)
}

// RawStream is an agent's native output, verbatim.
type RawStream = io.Reader

// Caps describes what an adapter can do.
type Caps struct {
	EventTier string
	ModelFlag string
	SkillDir  string
	// AuthDirs are host paths the sandbox mounts read-only for subscription
	// auth. Local-development convenience only — does not work on headless
	// workers, and carries nothing on macOS where the CLI uses the Keychain.
	AuthDirs []string
	// AuthEnv is preferred over AuthDirs: it works on a headless host with no
	// interactive login. The runner forwards any of these that are set.
	AuthEnv                 []string
	SupportsSkillInvocation bool
}

// InvokeSpec is one session request. Workspace and timeout live on the sandbox.
type InvokeSpec struct {
	Prompt string
	Model  string
	Exec   Exec
}

// Meta is session-level telemetry reported alongside the trajectory.
type Meta struct {
	AgentVersion      string
	Model             string
	InputTokens       int64
	OutputTokens      int64
	DurationMS        int64
	NumTurns          int
	VisibleSkills     []string
	PermissionDenials []string
	RateLimited       bool
	// RateLimitUtilization is the highest window utilization seen, 0 when never
	// reported, so an approaching limit is visible before it fails sessions.
	RateLimitUtilization float64
	RateLimitWindow      string
	IsError              bool
}

// Result is a parsed session.
type Result struct {
	Events []Event
	Meta   Meta
}

// Adapter drives one agent CLI.
type Adapter interface {
	Name() string
	Caps() Caps
	InstallSkill(workspace, bundlePath string) error
	Invoke(ctx context.Context, s InvokeSpec) (RawStream, error)
	Parse(r RawStream) (*Result, error)
}
