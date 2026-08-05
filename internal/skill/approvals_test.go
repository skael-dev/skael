package skill_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

// heldWithBothReasons publishes a version held for scan and ownership and
// returns its store and name.
func heldWithBothReasons(t *testing.T) (*skill.Store, string) {
	t.Helper()
	ctx := context.Background()
	store := skill.NewStore(testutil.SetupTestDB(t))

	sk, err := store.Create(ctx, "payments:refunds", "refunds", "", "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	d := gate.Decision{
		Outcome:     gate.NeedsReview,
		HoldReasons: []string{gate.ReasonScan, gate.ReasonOwnership},
	}
	if _, err := store.CreateVersion(ctx, sk.ID, "a.tar.gz", "sum", "", "d", "b",
		[]byte(`{}`), nil, []byte(`{}`), "carol@acme.com", d); err != nil {
		t.Fatalf("create version: %v", err)
	}
	return store, "payments:refunds"
}

func TestCreateVersionPersistsHoldReasons(t *testing.T) {
	ctx := context.Background()
	store, name := heldWithBothReasons(t)

	ver, err := store.GetVersion(ctx, name, 1)
	if err != nil || ver == nil {
		t.Fatalf("GetVersion = (%v, %v)", ver, err)
	}
	if len(ver.HoldReasons) != 2 {
		t.Fatalf("hold_reasons = %v, want two entries", ver.HoldReasons)
	}
	if ver.GateState != "needs_review" {
		t.Fatalf("gate_state = %q, want needs_review", ver.GateState)
	}
}

// The whole point of O8. Clearing one reason must not release the version.
func TestClearingOneReasonDoesNotRelease(t *testing.T) {
	ctx := context.Background()
	store, name := heldWithBothReasons(t)

	released, err := store.ApproveReason(ctx, store.Pool(), name, 1,
		gate.ReasonOwnership, nil, "alice@acme.com", "looks right")
	if err != nil {
		t.Fatalf("approve ownership: %v", err)
	}
	if released {
		t.Fatal("clearing ownership alone released the version; scan was still outstanding")
	}

	ver, _ := store.GetVersion(ctx, name, 1)
	if ver.GateState != "needs_review" {
		t.Fatalf("gate_state = %q, want needs_review", ver.GateState)
	}

	out, err := store.OutstandingReasons(ctx, name, 1)
	if err != nil {
		t.Fatalf("outstanding: %v", err)
	}
	if len(out) != 1 || out[0] != gate.ReasonScan {
		t.Fatalf("outstanding = %v, want [scan]", out)
	}

	released, err = store.ApproveReason(ctx, store.Pool(), name, 1,
		gate.ReasonScan, nil, "root@acme.com", "reviewed the cradle")
	if err != nil {
		t.Fatalf("approve scan: %v", err)
	}
	if !released {
		t.Fatal("clearing the last outstanding reason did not release the version")
	}

	ver, _ = store.GetVersion(ctx, name, 1)
	if ver.GateState != "released" {
		t.Fatalf("gate_state = %q, want released", ver.GateState)
	}
}

func TestRejectingOneReasonRejectsTheVersion(t *testing.T) {
	ctx := context.Background()
	store, name := heldWithBothReasons(t)

	if err := store.RejectReason(ctx, name, 1, gate.ReasonOwnership,
		"alice@acme.com", "not the direction we want"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	ver, _ := store.GetVersion(ctx, name, 1)
	if ver.GateState != "rejected" {
		t.Fatalf("gate_state = %q, want rejected", ver.GateState)
	}
}

// An automatic release from an evaluation must stay distinguishable from a
// human's approval forever. actor is NULL and actor_email is the sentinel.
func TestSystemApprovalIsDistinguishable(t *testing.T) {
	ctx := context.Background()
	store, name := heldWithBothReasons(t)

	if _, err := store.ApproveReason(ctx, store.Pool(), name, 1,
		gate.ReasonScan, nil, "system:eval", "verified score 82.0"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	var actorEmail string
	var actor *string
	err := store.Pool().QueryRow(ctx, `
		SELECT a.actor, a.actor_email FROM version_approvals a
		JOIN skill_versions v ON v.id = a.version_id
		JOIN skills s ON s.id = v.skill_id
		WHERE s.name = $1 AND v.version = 1 AND a.reason = 'scan'`, name).Scan(&actor, &actorEmail)
	if err != nil {
		t.Fatalf("read approval: %v", err)
	}
	if actor != nil {
		t.Fatalf("actor = %v, want NULL for a system release", *actor)
	}
	if actorEmail != "system:eval" {
		t.Fatalf("actor_email = %q, want system:eval", actorEmail)
	}
}
