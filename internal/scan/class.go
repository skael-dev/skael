package scan

// Class groups a finding by whether an empirical measurement could overturn it.
type Class string

const (
	// Unappealable: no evaluation overrides credential theft.
	ClassExfiltration Class = "exfiltration"
	ClassSecret       Class = "secret"

	// Appealable: shape-based guesses a sandbox can verify.
	ClassExecution Class = "execution"
	ClassInjection Class = "injection"
	ClassHeuristic Class = "heuristic"
)

// "obfuscation" maps to ClassHeuristic, not ClassExfiltration: a base64 blob
// is a smell, not proof, and treating it as unappealable makes minified
// vendored dependencies permanently unpublishable.
var classByCategory = map[string]Class{
	"exfiltration": ClassExfiltration,
	"secrets":      ClassSecret,
	"execution":    ClassExecution,
	"injection":    ClassInjection,
	"obfuscation":  ClassHeuristic,
}

// ClassOf maps a rule category onto a Class. False for an unmapped category;
// Decide treats unmapped as Block.
func ClassOf(category string) (Class, bool) {
	c, ok := classByCategory[category]
	return c, ok
}
