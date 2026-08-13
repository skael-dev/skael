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

// Reason is one finding that drove the decision, rendered by the CLI and
// consumed by the review UI.
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

// Hold reason kinds. Persisted wire names: renaming one makes every in-flight
// hold unclearable.
const (
	ReasonScan      = "scan"
	ReasonOwnership = "ownership"
)

// OwnerRef identifies one owner for display in the CLI and review UI.
type OwnerRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// OwnerState is the publisher's ownership standing. Evaluated is false when
// ownership was not determined (the CLI's local pre-scan). IsOwner folds in
// instance privilege, resolved at the HTTP boundary.
type OwnerState struct {
	Evaluated   bool
	IsOwner     bool
	Unowned     bool
	RulePattern string
	Owners      []OwnerRef
}

// holds reports whether o keeps the version out of circulation. Unowned does
// not hold: protection is self-enabling per namespace.
func (o OwnerState) holds() bool {
	return o.Evaluated && !o.IsOwner && !o.Unowned
}

// OwnershipDetail is the ownership half of a decision.
type OwnershipDetail struct {
	RulePattern string     `json:"rule_pattern,omitempty"`
	Owners      []OwnerRef `json:"owners"`
	Clears      string     `json:"clears"`
}

// Decision is the whole verdict.
type Decision struct {
	Outcome     Outcome          `json:"outcome"`
	Reasons     []Reason         `json:"reasons"`
	HoldReasons []string         `json:"hold_reasons"`
	Ownership   *OwnershipDetail `json:"ownership,omitempty"`
}

// Held reports whether this decision keeps the version out of circulation.
func (d Decision) Held() bool { return d.Outcome == NeedsReview }

// Policy is the deployment's configuration.
type Policy struct {
	Floor         float64 // minimum headline score to clear a hold; 0 accepts any verified report
	AdminOverride bool    // resolved at the HTTP boundary
}

// QualityState is what is known about the version's measured behaviour. Nil
// means no measurement exists — not a zero score.
type QualityState struct {
	Verified                 bool
	PanelComplete            bool
	Headline                 float64
	CriticalForbidViolations int
}

// clears reports whether q is strong enough to release a held version.
func (q *QualityState) clears(floor float64) bool {
	switch {
	case q == nil:
		return false
	case !q.Verified:
		return false
	case !q.PanelComplete:
		// An incomplete panel means a member's adapter failed; letting it
		// clear a gate turns CLI churn into a publish approval.
		return false
	case q.Headline < floor:
		return false
	case q.CriticalForbidViolations > 0:
		// The eval is evidence against the skill, not for it.
		return false
	}
	return true
}

func blocking(severity string) bool {
	return severity == "critical" || severity == "high"
}

// appealableClears returns the sentence a held version shows its publisher.
// Zero is special-cased because it is the default QUALITY_FLOOR.
func appealableClears(floor float64) string {
	const tail = "with a complete panel and no critical contract violations, or an admin approval"
	if floor <= 0 {
		return "a verified evaluation " + tail
	}
	return fmt.Sprintf("a verified evaluation scoring at least %.0f %s", floor, tail)
}

// Decide maps a scan report, quality state, and ownership onto an outcome.
// Pure: no database, HTTP, or clock.
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
			// Fail closed: an unclassified blocking finding is unappealable.
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
		d.Outcome = Allow
	case len(d.Reasons) > 0:
		d.Outcome = AllowWithWarning
	}

	return d
}
