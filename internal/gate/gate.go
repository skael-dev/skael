package gate

import (
	"fmt"

	"github.com/skael-dev/skael/internal/scan"
)

// Outcome is what the gate decided.
type Outcome string

const (
	Allow            Outcome = "allow"
	AllowWithWarning Outcome = "allow_with_warning"
	NeedsReview      Outcome = "needs_review"
	Block            Outcome = "block"
)

// Reason is one finding that drove the decision, together with what would
// clear it. It is part of the contract, not a debug aid: the CLI renders it
// and the review UI consumes it.
type Reason struct {
	Rule     string `json:"rule"`
	Class    string `json:"class"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
	// Clears states in one sentence what would resolve this finding.
	Clears string `json:"clears"`
}

// Decision is the whole verdict. It is persisted as an object rather than a
// bare array of reasons: json.Marshal on a nil slice emits null, and a null
// in a column declared NOT NULL DEFAULT '[]' makes "no reasons" and "never
// written" indistinguishable while breaking jsonb_array_length.
type Decision struct {
	Outcome Outcome  `json:"outcome"`
	Reasons []Reason `json:"reasons"`
}

// Held reports whether this decision keeps the version out of circulation.
func (d Decision) Held() bool { return d.Outcome == NeedsReview }

// Policy is the deployment's configuration.
type Policy struct {
	// Floor is the minimum headline score a verified report must reach to
	// clear a NeedsReview. 0 means any verified report clears, provided the
	// other conditions hold.
	Floor float64
	// AdminOverride is the already-resolved answer to "may this caller
	// override, and did they ask to". Resolving it at the HTTP boundary and
	// passing the answer keeps Decide pure and keeps the privilege check to
	// exactly one call site, so re-pointing it at an audited authorizer
	// later is a one-function change.
	AdminOverride bool
}

// QualityState is what is known about the version's measured behaviour. A nil
// *QualityState means no measurement exists — never a zero score. A zero
// headline means the skill scored nothing, which is the opposite claim.
type QualityState struct {
	Verified                 bool
	PanelComplete            bool
	Headline                 float64
	CriticalForbidViolations int
}

// clears reports whether q is evidence strong enough to release a version
// held on an appealable finding. All four conditions are required.
func (q *QualityState) clears(floor float64) bool {
	switch {
	case q == nil:
		return false
	case !q.Verified:
		// An attested score is a claim, not a measurement.
		return false
	case !q.PanelComplete:
		// A panel member whose adapter failed its health probe contributes
		// no result. Letting an incomplete panel clear a gate turns CLI
		// churn or an expired token into a publish approval.
		return false
	case q.Headline < floor:
		return false
	case q.CriticalForbidViolations > 0:
		// The skill violated its own contract's forbid rules under
		// observation. This is the one case where the eval is evidence
		// against the skill, not for it.
		return false
	}
	return true
}

// blocking reports whether a finding's severity is high enough to enter the
// decision at all. This is deliberately the same threshold the publish route
// used before the gate existed: this phase changes what happens to a block,
// not what counts as one.
func blocking(severity string) bool {
	return severity == "critical" || severity == "high"
}

// appealableClears is the single definition of the sentence a held version
// shows its publisher. A zero floor is special-cased because it is the default
// configuration: QUALITY_FLOOR defaults to 0, so the unconditional phrasing
// makes the headline message of the whole feature read "scoring at least 0",
// which states a threshold that is not one.
func appealableClears(floor float64) string {
	const tail = "with a complete panel and no critical contract violations, or an admin approval"
	if floor <= 0 {
		return "a verified evaluation " + tail
	}
	return fmt.Sprintf("a verified evaluation scoring at least %.0f %s", floor, tail)
}

// Decide maps a scan report and an optional quality state onto an outcome.
// It is pure: no database, no HTTP, no clock, no context.
func Decide(rep scan.Report, q *QualityState, p Policy) Decision {
	d := Decision{Outcome: Allow, Reasons: []Reason{}}

	var unappealable, appealable int

	for _, f := range rep.Findings {
		r := Reason{
			Rule:     f.Rule,
			Class:    f.Class,
			Severity: f.Severity,
			File:     f.File,
			Line:     f.Line,
			Message:  f.Message,
		}

		if !blocking(f.Severity) {
			r.Clears = "advisory only; this finding does not block publishing"
			d.Reasons = append(d.Reasons, r)
			continue
		}

		switch Class(f.Class) {
		case ClassExfiltration, ClassSecret:
			unappealable++
			r.Clears = "nothing: credential-theft findings are unappealable. " +
				"Remove the finding from the bundle; no evaluation and no override clears it."
		case ClassExecution, ClassInjection, ClassHeuristic:
			appealable++
			r.Clears = appealableClears(p.Floor)
		default:
			// Unreachable from the native rule set — TestEveryRuleHasAClass
			// keeps it so. Fail closed if it happens anyway: an unclassified
			// blocking finding is one nobody has decided is safe.
			unappealable++
			r.Clears = fmt.Sprintf("nothing: finding has no recognised class (%q), which is treated as unappealable", f.Class)
		}
		d.Reasons = append(d.Reasons, r)
	}

	switch {
	case unappealable > 0:
		d.Outcome = Block
	case appealable > 0:
		if q.clears(p.Floor) || p.AdminOverride {
			d.Outcome = Allow
		} else {
			d.Outcome = NeedsReview
		}
	case len(d.Reasons) > 0:
		d.Outcome = AllowWithWarning
	}

	return d
}
