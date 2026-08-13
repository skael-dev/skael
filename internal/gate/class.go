// Package gate decides whether a skill version may be published, held for
// review, or refused outright. It is pure: no database, no HTTP, no clock.
package gate

import "github.com/skael-dev/skael/internal/scan"

// Class groups a scan finding by what kind of claim it makes. Severity says
// how confident the scanner is; class says whether an empirical measurement
// could ever overturn it. Type alias so gate.Class and scan.Class stay
// interchangeable.
type Class = scan.Class

const (
	// Unappealable: no evaluation overrides credential theft.
	ClassExfiltration = scan.ClassExfiltration
	ClassSecret       = scan.ClassSecret

	// Appealable: shape-based guesses a sandbox can verify.
	ClassExecution = scan.ClassExecution
	ClassInjection = scan.ClassInjection
	ClassHeuristic = scan.ClassHeuristic
)

// ClassOf maps a scan rule category onto a Class. The bool is false for an
// unmapped category; Decide treats unmapped as Block.
func ClassOf(category string) (Class, bool) { return scan.ClassOf(category) }
