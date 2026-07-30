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
	Text     string `yaml:"text"`
	Negative bool   `yaml:"negative,omitempty"`
}

// Step is one action in the skill's workflow. Every step carries a verifiable
// postcondition; Validation marks the steps that are checkpoints.
type Step struct {
	ID            string `yaml:"id"`
	Action        string `yaml:"action"`
	Postcondition string `yaml:"postcondition"`
	Validation    bool   `yaml:"validation,omitempty"`
	Rationale     string `yaml:"rationale,omitempty"`
}

// Rule is a MUST or MUST-NOT constraint.
type Rule struct {
	ID       string   `yaml:"id"`
	Text     string   `yaml:"text"`
	Kind     RuleKind `yaml:"kind"`
	Severity Severity `yaml:"severity"`
}

// ResourceItem is one planned bundle file.
type ResourceItem struct {
	Path    string `yaml:"path"`
	Purpose string `yaml:"purpose,omitempty"`
}

// ResourcePlan decides what becomes scripts/ vs references/ vs assets/.
type ResourcePlan struct {
	Scripts    []ResourceItem `yaml:"scripts,omitempty"`
	References []ResourceItem `yaml:"references,omitempty"`
	Assets     []ResourceItem `yaml:"assets,omitempty"`
}

// Count returns the total number of planned modules, for the MaxModules cap.
func (p ResourcePlan) Count() int {
	return len(p.Scripts) + len(p.References) + len(p.Assets)
}

// DepsDecl lists packages baked into the per-skill sandbox image layer.
type DepsDecl struct {
	Apt []string `yaml:"apt,omitempty"`
	Pip []string `yaml:"pip,omitempty"`
	Npm []string `yaml:"npm,omitempty"`
}

// SkillSpec is the IR.
type SkillSpec struct {
	Name        string          `yaml:"name"`
	Purpose     string          `yaml:"purpose"`
	Description string          `yaml:"description"`
	Triggers    []TriggerPhrase `yaml:"triggers"`
	Steps       []Step          `yaml:"steps"`
	Constraints []Rule          `yaml:"constraints,omitempty"`
	Resources   ResourcePlan    `yaml:"resources,omitempty"`
	Deps        DepsDecl        `yaml:"deps,omitempty"`
	TargetTier  ModelTier       `yaml:"target_tier"`
}
