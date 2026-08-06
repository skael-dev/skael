package gate_test

import (
	"testing"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/scan"
)

func clean() scan.Report { return scan.Report{} }

func appealable() scan.Report {
	return scan.Report{Findings: []scan.Finding{{
		Rule: "DATA_EXFILTRATION", Class: "execution", Severity: "critical",
		File: "SKILL.md", Line: 3, Message: "pipe to shell",
	}}}
}

func owners() []gate.OwnerRef {
	return []gate.OwnerRef{{ID: "u1", Name: "Alice Chen", Email: "alice@acme.com"}}
}

func has(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

func TestOwnershipHoldsANonOwner(t *testing.T) {
	o := gate.OwnerState{Evaluated: true, IsOwner: false, RulePattern: "payments:*", Owners: owners()}
	d := gate.Decide(clean(), nil, o, gate.Policy{})

	if d.Outcome != gate.NeedsReview {
		t.Fatalf("outcome = %s, want needs_review", d.Outcome)
	}
	if !has(d.HoldReasons, gate.ReasonOwnership) {
		t.Fatalf("hold_reasons = %v, want to contain %q", d.HoldReasons, gate.ReasonOwnership)
	}
	if d.Ownership == nil || len(d.Ownership.Owners) != 1 {
		t.Fatal("decision carries no ownership detail; the CLI cannot name who unblocks the publisher")
	}
	if d.Ownership.RulePattern != "payments:*" {
		t.Fatalf("rule pattern = %q, want payments:*", d.Ownership.RulePattern)
	}
}

func TestOwnerPublishesDirectly(t *testing.T) {
	o := gate.OwnerState{Evaluated: true, IsOwner: true, RulePattern: "payments:*", Owners: owners()}
	if d := gate.Decide(clean(), nil, o, gate.Policy{}); d.Outcome != gate.Allow {
		t.Fatalf("outcome = %s, want allow (O4: ownership is the right to change the thing)", d.Outcome)
	}
}

// O5: unowned does not hold a publish. Existing installs must see no change
// until someone writes their first rule.
func TestUnownedDoesNotHold(t *testing.T) {
	o := gate.OwnerState{Evaluated: true, IsOwner: false, Unowned: true}
	if d := gate.Decide(clean(), nil, o, gate.Policy{}); d.Outcome != gate.Allow {
		t.Fatalf("outcome = %s, want allow (O5: unowned does not hold a publish)", d.Outcome)
	}
}

// The CLI's local pre-scan cannot know ownership. An unevaluated state must
// contribute nothing at all, so local behaviour is byte-for-byte unchanged.
func TestUnevaluatedOwnershipContributesNothing(t *testing.T) {
	if d := gate.Decide(clean(), nil, gate.OwnerState{}, gate.Policy{}); d.Outcome != gate.Allow {
		t.Fatalf("outcome = %s, want allow", d.Outcome)
	}
	if len(gate.Decide(clean(), nil, gate.OwnerState{}, gate.Policy{}).HoldReasons) != 0 {
		t.Fatal("an unevaluated ownership state produced a hold reason")
	}
}

// O8: the two reasons are independent. A perfect score clears scan and leaves
// ownership standing; nothing about ownership touches scan.
func TestReasonsAreIndependent(t *testing.T) {
	nonOwner := gate.OwnerState{Evaluated: true, IsOwner: false, RulePattern: "payments:*", Owners: owners()}

	d := gate.Decide(appealable(), nil, nonOwner, gate.Policy{})
	if !has(d.HoldReasons, gate.ReasonScan) || !has(d.HoldReasons, gate.ReasonOwnership) {
		t.Fatalf("hold_reasons = %v, want both scan and ownership", d.HoldReasons)
	}

	perfect := &gate.QualityState{Verified: true, PanelComplete: true, Headline: 100}
	d = gate.Decide(appealable(), perfect, nonOwner, gate.Policy{})
	if has(d.HoldReasons, gate.ReasonScan) {
		t.Fatal("a verified score did not clear the scan reason")
	}
	if !has(d.HoldReasons, gate.ReasonOwnership) {
		t.Fatal("a verified score cleared the OWNERSHIP reason — the feature is decorative")
	}
	if d.Outcome != gate.NeedsReview {
		t.Fatalf("outcome = %s, want needs_review", d.Outcome)
	}
}

// An unappealable finding creates no version row at all, so ownership is
// irrelevant: Block must stay Block and must not be diluted into a hold.
func TestUnappealableStillBlocksRegardlessOfOwnership(t *testing.T) {
	rep := scan.Report{Findings: []scan.Finding{{
		Rule: "SECRET_EXPOSURE", Class: "secrets", Severity: "critical",
		File: "SKILL.md", Line: 1, Message: "hardcoded key",
	}}}
	o := gate.OwnerState{Evaluated: true, IsOwner: true}
	if d := gate.Decide(rep, nil, o, gate.Policy{}); d.Outcome != gate.Block {
		t.Fatalf("outcome = %s, want block", d.Outcome)
	}
}
