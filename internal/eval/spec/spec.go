// Package spec holds the SkillSpec intermediate representation — the source of
// truth every other eval component derives from. The generator writes a bundle
// from it, the contract compiler compiles matchers from it, and the suite
// generator drafts tasks from it. Nothing downstream parses the rendered
// SKILL.md to recover intent.
package spec

// RuleKind distinguishes a positive obligation from a prohibition.
type RuleKind string

const (
	RuleMust    RuleKind = "must"
	RuleMustNot RuleKind = "must_not"
)

// Severity grades a constraint. It is carried through to the drift contract's
// forbid rules, where it weights ViolationScore.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityMajor    Severity = "major"
	SeverityMinor    Severity = "minor"
)

// ModelTier names the capability floor the generator writes for.
//
// "Tier" is heavily overloaded in this codebase: runner.Tier is eval depth
// (smoke/full/deep), ModelTier is model capability (floor/mid/strong),
// distractor difficulty is graded by distance from the skill (near/mid/far), and
// agent.Caps.EventTier grades how much fidelity an adapter's event stream
// carries (A/B/C). Four different axes, one overloaded word. runner.Member
// uses the field name Class rather than Tier for exactly this reason — its
// value is still a ModelTier, but the field name doesn't collide with
// runner.Tier sitting one struct away.
type ModelTier string

const (
	TierFloor  ModelTier = "floor"
	TierMid    ModelTier = "mid"
	TierStrong ModelTier = "strong"
)

// MaxModules caps bundled resource files. SkillsBench found focused skills beat
// exhaustive bundles, so this is a hard limit rather than a warning.
const MaxModules = 3

// MaxDescription is the Agent Skills spec limit on frontmatter description.
const MaxDescription = 1024

// MaxName is the Agent Skills spec limit on skill name length.
const MaxName = 64

// TriggerPhrase is one example prompt. Negative phrases are hard negatives —
// adjacent-domain near-misses. Obviously-irrelevant negatives test nothing, so
// the interview prompt asks for near-misses explicitly.
type TriggerPhrase struct {
	Text     string `yaml:"text" json:"text"`
	Negative bool   `yaml:"negative,omitempty" json:"negative,omitempty"`
}

// Step is one action in the skill's workflow. Every step carries a verifiable
// postcondition; Validation marks the steps that are checkpoints.
type Step struct {
	ID            string `yaml:"id" json:"id"`
	Action        string `yaml:"action" json:"action"`
	Postcondition string `yaml:"postcondition" json:"postcondition"`
	Validation    bool   `yaml:"validation,omitempty" json:"validation,omitempty"`
	Rationale     string `yaml:"rationale,omitempty" json:"rationale,omitempty"`
}

// Rule is a MUST or MUST-NOT constraint.
type Rule struct {
	ID       string   `yaml:"id" json:"id"`
	Text     string   `yaml:"text" json:"text"`
	Kind     RuleKind `yaml:"kind" json:"kind"`
	Severity Severity `yaml:"severity" json:"severity"`
}

// ResourceItem is one planned bundle file.
type ResourceItem struct {
	Path    string `yaml:"path" json:"path"`
	Purpose string `yaml:"purpose,omitempty" json:"purpose,omitempty"`
}

// ResourcePlan decides what becomes scripts/ vs references/ vs assets/.
type ResourcePlan struct {
	Scripts    []ResourceItem `yaml:"scripts,omitempty" json:"scripts,omitempty"`
	References []ResourceItem `yaml:"references,omitempty" json:"references,omitempty"`
	Assets     []ResourceItem `yaml:"assets,omitempty" json:"assets,omitempty"`
}

// Count returns the total number of planned modules, for the MaxModules cap.
func (p ResourcePlan) Count() int {
	return len(p.Scripts) + len(p.References) + len(p.Assets)
}

// DepsDecl lists packages baked into the per-skill sandbox image layer.
type DepsDecl struct {
	Apt []string `yaml:"apt,omitempty" json:"apt,omitempty"`
	Pip []string `yaml:"pip,omitempty" json:"pip,omitempty"`
	Npm []string `yaml:"npm,omitempty" json:"npm,omitempty"`
}

// SkillSpec is the IR.
type SkillSpec struct {
	Name        string          `yaml:"name" json:"name"`
	Purpose     string          `yaml:"purpose" json:"purpose"`
	Description string          `yaml:"description" json:"description"`
	Triggers    []TriggerPhrase `yaml:"triggers" json:"triggers"`
	Steps       []Step          `yaml:"steps" json:"steps"`
	Constraints []Rule          `yaml:"constraints,omitempty" json:"constraints,omitempty"`
	Resources   ResourcePlan    `yaml:"resources,omitempty" json:"resources,omitempty"`
	Deps        DepsDecl        `yaml:"deps,omitempty" json:"deps,omitempty"`
	TargetTier  ModelTier       `yaml:"target_tier" json:"target_tier"`
}
