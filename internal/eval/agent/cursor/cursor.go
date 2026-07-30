// Package cursor reserves the Cursor CLI adapter. Caps are declared so the
// orchestrator can reason about the agent, but Parse and Invoke fail closed
// until a real stream has been recorded from `cursor exec --json`.
//
// Writing a parser from documentation produces one that passes its own tests
// and fails on real output, so this package deliberately has no parser.
package cursor

import (
	"context"

	"github.com/skael-dev/skael/internal/eval/agent"
)

// Adapter implements agent.Adapter for Cursor.
type Adapter struct{}

// New returns the Cursor adapter.
func New() *Adapter { return &Adapter{} }

func init() { agent.Register(New()) }

// Name identifies the adapter.
func (a *Adapter) Name() string { return "cursor" }

// Caps declares what is known about Cursor without a recorded stream. Whether
// its stream exposes a skill-invocation event is unverified, so
// SupportsSkillInvocation stays false — claiming otherwise would silently
// zero trigger measurement.
func (a *Adapter) Caps() agent.Caps {
	return agent.Caps{
		EventTier:               "A",
		ModelFlag:               "--model",
		SkillDir:                ".cursor/skills",
		AuthDirs:                []string{"~/.cursor"},
		SupportsSkillInvocation: false,
	}
}

// InstallSkill is not implemented without a verified layout.
func (a *Adapter) InstallSkill(string, string) error { return agent.ErrInvokeNotImplemented }

// Invoke is not implemented.
func (a *Adapter) Invoke(context.Context, agent.InvokeSpec) (agent.RawStream, error) {
	return nil, agent.ErrInvokeNotImplemented
}

// Parse is not implemented — no recorded fixture exists for this CLI.
func (a *Adapter) Parse(agent.RawStream) (*agent.Result, error) {
	return nil, agent.ErrParseNotImplemented
}
