// Package gate decides whether a skill version may be published, held for
// review, or refused outright. It is pure: no database, no HTTP, no clock.
package gate

import "github.com/skael-dev/skael/internal/scan"

// Class groups a scan finding by what kind of claim it makes. Severity says
// how confident the scanner is; class says whether an empirical measurement
// could ever overturn it.
//
// The vocabulary itself lives in internal/scan (see scan/class.go): scan
// already needs it to populate Finding.Class as rules fire, and gate needs
// scan's report types for Decide, so the type must live on the side with no
// outgoing edge back to the other. Class is a type alias (not a defined
// type) precisely so gate.Class and scan.Class stay interchangeable and every
// existing gate.ClassExecution / gate.ClassOf callsite keeps compiling and
// reading naturally.
type Class = scan.Class

const (
	// ClassExfiltration and ClassSecret are unappealable. No evaluation
	// overrides credential theft: a sandbox observing sixty well-behaved
	// runs does not prove the sixty-first will not post a token somewhere.
	ClassExfiltration = scan.ClassExfiltration
	ClassSecret       = scan.ClassSecret

	// The rest are guesses the scanner makes from shape alone, which is
	// exactly what running the skill in a network-off sandbox measures
	// directly.
	ClassExecution = scan.ClassExecution
	ClassInjection = scan.ClassInjection
	ClassHeuristic = scan.ClassHeuristic
)

// ClassOf maps a scan rule category onto a Class. The bool is false for an
// unmapped category; callers must not substitute a default, because every
// plausible default is wrong in one direction or the other. Decide treats an
// unmapped class as Block, and TestEveryRuleHasAClass keeps that unreachable
// from the native rule set.
func ClassOf(category string) (Class, bool) { return scan.ClassOf(category) }
