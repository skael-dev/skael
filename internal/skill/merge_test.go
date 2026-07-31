package skill

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestMerge(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	source, _ := store.Create(ctx, "superpowers:brainstorming", "", "source", "", json.RawMessage(`{}`))
	target, _ := store.Create(ctx, "brainstorming", "", "target", "", json.RawMessage(`{}`))

	if _, err := store.CreateVersion(ctx, source.ID, "s/archive.tar.gz", "checksum1", "", "", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "test@example.com", allowDecision()); err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}
	if _, err := store.CreateVersion(ctx, target.ID, "t/archive.tar.gz", "checksum2", "", "", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "test@example.com", allowDecision()); err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}

	merged, err := store.Merge(ctx, "superpowers:brainstorming", "brainstorming")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if merged.LatestVersion != 2 {
		t.Errorf("latest_version = %d, want 2", merged.LatestVersion)
	}

	gone, _ := store.GetByName(ctx, "superpowers:brainstorming")
	if gone != nil {
		t.Error("source skill should be deleted after merge")
	}

	canonical, _ := store.ResolveAlias(ctx, "superpowers:brainstorming")
	if canonical != "brainstorming" {
		t.Errorf("alias canonical = %q, want %q", canonical, "brainstorming")
	}

	versions, _ := store.ListVersions(ctx, "brainstorming")
	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2", len(versions))
	}
}

// allowDecision is the clean-scan gate decision: nothing to hold on.
func allowDecision() gate.Decision {
	return gate.Decision{Outcome: gate.Allow, Reasons: []gate.Reason{}}
}

func heldMergeDecision() gate.Decision {
	return gate.Decision{Outcome: gate.NeedsReview, Reasons: []gate.Reason{}}
}

// Merge numbers reparented versions from the target's MAX(version), not from
// its latest_version. A target holding a gated v2 has latest_version = 1 while
// MAX(version) = 2; numbering from the pointer collides on
// UNIQUE(skill_id, version).
func TestMergeNumbersFromMaxNotFromLatestPointer(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	source, _ := store.Create(ctx, "src-skill", "", "source", "", json.RawMessage(`{}`))
	target, _ := store.Create(ctx, "dst-skill", "", "target", "", json.RawMessage(`{}`))

	// Target: released v1, held v2 => latest_version 1, MAX(version) 2.
	if _, err := store.CreateVersion(ctx, target.ID, "t/1.tar.gz", "tc1", "", "", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t", allowDecision()); err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}
	if _, err := store.CreateVersion(ctx, target.ID, "t/2.tar.gz", "tc2", "", "", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t", heldMergeDecision()); err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}
	if _, err := store.CreateVersion(ctx, source.ID, "s/1.tar.gz", "sc1", "", "", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t", allowDecision()); err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}

	merged, err := store.Merge(ctx, "src-skill", "dst-skill")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	versions, err := store.ListVersions(ctx, "dst-skill")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("got %d versions, want 3", len(versions))
	}
	// ListVersions orders by version DESC.
	if versions[0].Version != 3 || versions[1].Version != 2 || versions[2].Version != 1 {
		t.Fatalf("versions = %d,%d,%d, want 3,2,1",
			versions[0].Version, versions[1].Version, versions[2].Version)
	}
	if merged.LatestVersion != 3 {
		t.Errorf("latest_version = %d, want 3 (the reparented released version)", merged.LatestVersion)
	}
}

// Merging a source whose newest version is held must not aim the target's
// pointer at that held version. sync/manifest.go joins on
// sv.version = s.latest_version, so a pointer aimed at a held version syncs
// the gated bundle to every client.
func TestMergeDoesNotPointLatestAtAHeldVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	source, _ := store.Create(ctx, "held-src", "", "source", "", json.RawMessage(`{}`))
	target, _ := store.Create(ctx, "held-dst", "", "target", "", json.RawMessage(`{}`))

	if _, err := store.CreateVersion(ctx, target.ID, "t/1.tar.gz", "tc1", "", "", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t", allowDecision()); err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}
	// Source: released v1 then held v2. The held one is newest.
	if _, err := store.CreateVersion(ctx, source.ID, "s/1.tar.gz", "sc1", "", "", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t", allowDecision()); err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}
	if _, err := store.CreateVersion(ctx, source.ID, "s/2.tar.gz", "sc2", "", "", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t", heldMergeDecision()); err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}

	merged, err := store.Merge(ctx, "held-src", "held-dst")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	// v1 target released, v2 = source v1 released, v3 = source v2 held.
	if merged.LatestVersion != 2 {
		t.Fatalf("latest_version = %d, want 2 — the pointer must stop at the highest released version, not the held v3", merged.LatestVersion)
	}
	v3, err := store.GetVersion(ctx, "held-dst", 3)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if v3.GateState != "needs_review" {
		t.Errorf("reparented v3 gate_state = %q, want needs_review — a merge must not launder a held version", v3.GateState)
	}
}

// When every reparented version is held, the pointer must not move at all.
func TestMergeAllHeldLeavesPointerAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	store := NewStore(pool)

	source, _ := store.Create(ctx, "allheld-src", "", "source", "", json.RawMessage(`{}`))
	target, _ := store.Create(ctx, "allheld-dst", "", "target", "", json.RawMessage(`{}`))

	if _, err := store.CreateVersion(ctx, target.ID, "t/1.tar.gz", "tc1", "", "", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t", allowDecision()); err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}
	if _, err := store.CreateVersion(ctx, source.ID, "s/1.tar.gz", "sc1", "", "", "", json.RawMessage(`{}`), nil, json.RawMessage(`{}`), "t", heldMergeDecision()); err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}

	merged, err := store.Merge(ctx, "allheld-src", "allheld-dst")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if merged.LatestVersion != 1 {
		t.Errorf("latest_version = %d, want 1 (unmoved)", merged.LatestVersion)
	}
}
