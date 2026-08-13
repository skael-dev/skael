// Package spec holds the SkillSpec IR that generation, suite drafting, and
// scoring all derive from.
package spec

// RuleKind distinguishes a positive obligation from a prohibition.
type RuleKind string

const (
	RuleMust    RuleKind = "must"
	RuleMustNot RuleKind = "must_not"
)

// Severity grades a constraint.
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

// MaxModules caps scripts/ + assets/. references/ is capped separately by
// MaxReferences because a reference split out of an over-long body adds no
// capability.
const MaxModules = 3

// MaxReferences caps references/, independently of MaxModules. Higher because
// body offloading is per-section and a realistic skill can need five moves.
const MaxReferences = 6

// MaxDescription is the Agent Skills spec limit on frontmatter description.
const MaxDescription = 1024

// MaxName is the Agent Skills spec limit on skill name length.
const MaxName = 64

// TriggerPhrase is one example prompt. Negative phrases are near-misses.
type TriggerPhrase struct {
	Text     string `yaml:"text" json:"text"`
	Negative bool   `yaml:"negative,omitempty" json:"negative,omitempty"`
}

// Step is one action in the skill's workflow.
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

// Count returns the total number of planned files.
func (p ResourcePlan) Count() int {
	return len(p.Scripts) + len(p.References) + len(p.Assets)
}

// CapacityCount returns the scripts/ + assets/ count, checked against MaxModules.
func (p ResourcePlan) CapacityCount() int {
	return len(p.Scripts) + len(p.Assets)
}

// ReferenceCount returns the references/ count, checked against MaxReferences.
func (p ResourcePlan) ReferenceCount() int {
	return len(p.References)
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
