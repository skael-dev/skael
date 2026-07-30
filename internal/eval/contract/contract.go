// Package contract compiles a spec.SkillSpec into a behavioural contract of
// deterministic matchers. A later scoring stage checks an agent's normalized
// trajectory against the contract; this package never reads a trajectory or
// scores one, it only compiles the expectations.
//
// The invariant every matcher in a compiled Contract must satisfy: it can be
// checked against a normalized trajectory.Event. Anything that cannot —
// a tone requirement, an unobservable step — is demoted to a judge-scored
// SemanticRule instead. A step matcher nothing can ever satisfy would score
// every run as a step failure, which reads as a badly-behaved skill rather
// than what it actually is: a compiler defect.
package contract

import (
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

// Version is the current Contract schema version.
const Version = 1

// Contract is the compiled behavioural contract for one skill.
type Contract struct {
	Version     int            `yaml:"version" json:"version"`
	Steps       []StepMatch    `yaml:"steps,omitempty" json:"steps,omitempty"`
	Forbid      []ForbidMatch  `yaml:"forbid,omitempty" json:"forbid,omitempty"`
	Checkpoints []string       `yaml:"checkpoints,omitempty" json:"checkpoints,omitempty"`
	Semantic    []SemanticRule `yaml:"semantic,omitempty" json:"semantic,omitempty"`
}

// Matcher describes one observable event a trajectory must (or must not)
// contain. Type is always one trajectory.EventType that the scorer can check
// against a normalized event; Pattern is a regular expression matched against
// the event's command or name, and PathGlob/PathNotGlob constrain the event's
// paths.
type Matcher struct {
	Type        trajectory.EventType `yaml:"type" json:"type"`
	Pattern     string               `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	PathGlob    string               `yaml:"path_glob,omitempty" json:"path_glob,omitempty"`
	PathNotGlob string               `yaml:"path_not_glob,omitempty" json:"path_not_glob,omitempty"`
}

// Order constrains where in the trajectory a step's matching event may fall.
// Mode is one of "any" (no constraint), "after" (must occur after the listed
// step IDs, in any relative order among themselves), or "strict" (must occur
// immediately after them, in that order).
type Order struct {
	Mode  string   `yaml:"mode" json:"mode"`
	After []string `yaml:"after,omitempty" json:"after,omitempty"`
}

// StepMatch is a deterministic check for one spec.Step.
type StepMatch struct {
	ID       string  `yaml:"id" json:"id"`
	Desc     string  `yaml:"desc,omitempty" json:"desc,omitempty"`
	Match    Matcher `yaml:"match" json:"match"`
	Order    Order   `yaml:"order" json:"order"`
	Required bool    `yaml:"required,omitempty" json:"required,omitempty"`
}

// ForbidMatch is a deterministic check for one spec.Rule of kind
// spec.RuleMustNot. Its presence in the trajectory is a violation.
type ForbidMatch struct {
	ID       string        `yaml:"id" json:"id"`
	Desc     string        `yaml:"desc,omitempty" json:"desc,omitempty"`
	Match    Matcher       `yaml:"match" json:"match"`
	Severity spec.Severity `yaml:"severity" json:"severity"`
}

// SemanticRule is a requirement with no deterministic check — a step or
// constraint that cannot be verified against a normalized event. It is scored
// by an LLM judge rather than the deterministic matcher path.
type SemanticRule struct {
	ID   string `yaml:"id" json:"id"`
	Text string `yaml:"text" json:"text"`
}
