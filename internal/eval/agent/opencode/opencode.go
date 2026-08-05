// Package opencode reserves the OpenCode CLI adapter. Caps are declared so the
// orchestrator can reason about the agent, but Parse and Invoke fail closed
// until a real stream has been recorded from `opencode exec --json`.
//
// Writing a parser from documentation produces one that passes its own tests
// and fails on real output, so this package deliberately has no parser.
package opencode

import (
	"context"

	"github.com/skael-dev/skael/internal/eval/agent"
)

// Adapter implements agent.Adapter for OpenCode.
type Adapter struct{}

// New returns the OpenCode adapter.
func New() *Adapter { return &Adapter{} }

func init() { agent.Register(New()) }

// Name identifies the adapter.
func (a *Adapter) Name() string { return "opencode" }

// Caps declares what is known about OpenCode without a recorded stream. Whether
// its stream exposes a skill-invocation event is unverified, so
// SupportsSkillInvocation stays false — claiming otherwise would silently
// zero trigger measurement.
func (a *Adapter) Caps() agent.Caps {
	return agent.Caps{
		EventTier: "A",
		ModelFlag: "--model",
		SkillDir:  ".opencode/skills",
		AuthDirs:  []string{"~/.local/share/opencode", "~/.config/opencode"},
		// AuthEnv is left empty: Parse returns ErrParseNotImplemented, so this
		// adapter cannot contribute to a panel yet. It gets populated when the
		// parser lands and the real CLI's env-based auth can be verified,
		// rather than guessed from documentation.
		SupportsSkillInvocation: false,
	}
}

// InstallSkill is not implemented without a verified layout.
func (a *Adapter) InstallSkill(string, string) error { return agent.ErrInstallNotImplemented }

// Invoke is not implemented.
func (a *Adapter) Invoke(context.Context, agent.InvokeSpec) (agent.RawStream, error) {
	return nil, agent.ErrInvokeNotImplemented
}

// Parse is not implemented — no recorded fixture exists for this CLI.
func (a *Adapter) Parse(agent.RawStream) (*agent.Result, error) {
	return nil, agent.ErrParseNotImplemented
}
