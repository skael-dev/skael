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

// Hold reason kinds. These are persisted in skill_versions.hold_reasons and
// in version_approvals.reason, so they are a wire contract: renaming one
// makes every in-flight hold unclearable.
const (
	ReasonScan      = "scan"
	ReasonOwnership = "ownership"
)

// OwnerRef identifies one owner well enough for a CLI or a UI to name them.
// Decide is pure and cannot look users up, so the caller passes them in.
type OwnerRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// OwnerState is what is known about who owns the skill being published to.
//
// Evaluated is false when ownership was not determined at all — the CLI's
// local pre-scan, which has no way to ask. An unevaluated state contributes
// nothing, so local behaviour is byte-for-byte what it was before ownership
// existed.
//
// IsOwner already folds in instance privilege: an owner or admin is an
// implicit owner of everything. Resolving that at the HTTP boundary keeps
// this package free of auth and keeps the privilege check to one call site,
// exactly as Policy.AdminOverride does.
type OwnerState struct {
	Evaluated   bool
	IsOwner     bool
	Unowned     bool
	RulePattern string
	Owners      []OwnerRef
}

// holds reports whether o by itself keeps the version out of circulation.
// Unowned deliberately does not hold: protection is self-enabling per
// namespace, so an install that has written no rules behaves exactly as it
// did before this feature shipped.
func (o OwnerState) holds() bool {
	return o.Evaluated && !o.IsOwner && !o.Unowned
}

// OwnershipDetail is the ownership half of a decision, rendered by the CLI
// and the review screen. Naming the humans who can unblock a publisher is
// what turns a wall into a next step.
type OwnershipDetail struct {
	RulePattern string     `json:"rule_pattern,omitempty"`
	Owners      []OwnerRef `json:"owners"`
	Clears      string     `json:"clears"`
}

// Decision is the whole verdict. It is persisted as an object rather than a
// bare array of reasons: json.Marshal on a nil slice emits null, and a null
// in a column declared NOT NULL DEFAULT '[]' makes "no reasons" and "never
// written" indistinguishable while breaking jsonb_array_length.
type Decision struct {
	Outcome Outcome  `json:"outcome"`
	Reasons []Reason `json:"reasons"`
	// HoldReasons are the reason kinds that must each be cleared before this
	// version may be served. Empty means nothing is holding it. It is a set,
	// not a state, because a scan finding and an unowned publisher are two
	// different questions and neither may be used to launder the other.
	HoldReasons []string `json:"hold_reasons"`
	// Ownership is present only when ownership was evaluated.
	Ownership *OwnershipDetail `json:"ownership,omitempty"`
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

// Decide maps a scan report, an optional quality state, and the publisher's
// ownership standing onto an outcome. It is pure: no database, no HTTP, no
// clock, no context.
func Decide(rep scan.Report, q *QualityState, o OwnerState, p Policy) Decision {
	d := Decision{Outcome: Allow, Reasons: []Reason{}, HoldReasons: []string{}}

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

	if o.Evaluated {
		d.Ownership = &OwnershipDetail{
			RulePattern: o.RulePattern,
			Owners:      o.Owners,
			Clears:      "approval by an owner of this skill, or by an instance admin",
		}
	}

	switch {
	case unappealable > 0:
		// Unappealable findings create no version row at all, so there is
		// nothing to hold and ownership cannot be relevant.
		d.Outcome = Block
		return d
	case appealable > 0:
		if !q.clears(p.Floor) && !p.AdminOverride {
			d.HoldReasons = append(d.HoldReasons, ReasonScan)
		}
	}

	if o.holds() {
		d.HoldReasons = append(d.HoldReasons, ReasonOwnership)
	}

	switch {
	case len(d.HoldReasons) > 0:
		d.Outcome = NeedsReview
	case appealable > 0:
		// An appealable finding was present but cleared (no ReasonScan
		// added) and ownership did not hold either: this is a full Allow
		// even though d.Reasons carries the (non-advisory) finding — falling
		// through to the generic advisory branch below would misreport a
		// cleared appealable finding as merely AllowWithWarning.
		d.Outcome = Allow
	case len(d.Reasons) > 0:
		d.Outcome = AllowWithWarning
	}

	return d
}
