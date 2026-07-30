// Package agent defines the interface every coding-agent CLI is driven through.
// Adding an agent means implementing this interface and recording a stream
// fixture; no scoring code changes.
package agent

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/skael-dev/skael/internal/eval/trajectory"
)

// ErrInvokeNotImplemented is returned by adapters whose Invoke path is not
// built yet. Invoking an agent requires a sandbox, so it lands with the
// sandbox rather than with the parsers.
var ErrInvokeNotImplemented = errors.New("agent: Invoke not implemented")

// ErrInstallNotImplemented is returned by adapters whose skill installation
// path is not built yet. Installing a skill requires knowledge of the agent's
// filesystem layout, which is why it is distinct from ErrInvokeNotImplemented.
var ErrInstallNotImplemented = errors.New("agent: InstallSkill not implemented")

// RawStream is an agent's native output, verbatim.
type RawStream = io.Reader

// Caps describes what an adapter can do, so the orchestrator can plan a panel
// without special-casing agent names.
type Caps struct {
	// EventTier is "A" when the stream distinguishes individual tool calls and
	// their results, "B" when it only reports messages.
	EventTier string
	// ModelFlag is the CLI flag that selects a model, e.g. "--model".
	ModelFlag string
	// SkillDir is where a skill bundle installs, relative to the workspace.
	SkillDir string
	// AuthDirs are host paths the sandbox mounts read-only so subscription auth
	// works inside the container.
	AuthDirs []string
	// SupportsSkillInvocation reports whether the stream exposes an explicit
	// skill-invocation event. When false, activation must be inferred from a
	// read of the skill's SKILL.md, which cannot distinguish read from invoked.
	SupportsSkillInvocation bool
}

// InvokeSpec is one agent session request.
type InvokeSpec struct {
	Workspace string
	Prompt    string
	Model     string
	Timeout   time.Duration
}

// Meta is everything a parsed stream reports about the session itself, as
// opposed to the trajectory. Token counts feed Efficiency, VisibleSkills feeds
// trigger precision, and AgentVersion is recorded per run so a score can be
// attributed to a specific CLI build.
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
	IsError           bool
}

// Result is a parsed session.
type Result struct {
	Events []trajectory.Event
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
