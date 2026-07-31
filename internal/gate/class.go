// Package gate decides whether a skill version may be published, held for
// review, or refused outright. It is pure: no database, no HTTP, no clock.
package gate

// Class groups a scan finding by what kind of claim it makes. Severity says
// how confident the scanner is; class says whether an empirical measurement
// could ever overturn it.
type Class string

const (
	// ClassExfiltration and ClassSecret are unappealable. No evaluation
	// overrides credential theft: a sandbox observing sixty well-behaved
	// runs does not prove the sixty-first will not post a token somewhere.
	ClassExfiltration Class = "exfiltration"
	ClassSecret       Class = "secret"

	// The rest are guesses the scanner makes from shape alone, which is
	// exactly what running the skill in a network-off sandbox measures
	// directly.
	ClassExecution Class = "execution"
	ClassInjection Class = "injection"
	ClassHeuristic Class = "heuristic"
)

// classByCategory maps a scan rule's Category onto a Class. Two entries are
// not identity mappings and both are deliberate:
//
//   - "secrets" -> ClassSecret is a rename only.
//   - "obfuscation" -> ClassHeuristic is a judgement. A base64 blob or a
//     zero-width sequence is a strong smell, not proof; treating it as
//     unappealable makes a minified vendored dependency permanently
//     unpublishable. The accepted risk is a payload that stays dormant
//     under evaluation.
var classByCategory = map[string]Class{
	"exfiltration": ClassExfiltration,
	"secrets":      ClassSecret,
	"execution":    ClassExecution,
	"injection":    ClassInjection,
	"obfuscation":  ClassHeuristic,
}

// ClassOf maps a scan rule category onto a Class. The bool is false for an
// unmapped category; callers must not substitute a default, because every
// plausible default is wrong in one direction or the other. Decide treats an
// unmapped class as Block, and TestEveryRuleHasAClass keeps that unreachable
// from the native rule set.
func ClassOf(category string) (Class, bool) {
	c, ok := classByCategory[category]
	return c, ok
}
