package skill_test

import (
	"context"
	"testing"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/skill"
)

// The single most important guarantee in the feature: a perfect verified
// score must not release a version that a non-owner published.
func TestReconsiderNeverClearsOwnership(t *testing.T) {
	ctx := context.Background()
	store, name := heldWithBothReasons(t)

	rel := skill.NewReleaser(store)
	_, released, err := rel.Reconsider(ctx, store.Pool(), name, 1, skill.QualityEvidence{
		Verified: true, PanelComplete: true, Headline: 100,
	}, 0)
	if err != nil {
		t.Fatalf("reconsider: %v", err)
	}
	if released {
		t.Fatal("a verified score released a version held for ownership review")
	}

	out, err := store.OutstandingReasons(ctx, name, 1)
	if err != nil {
		t.Fatalf("outstanding: %v", err)
	}
	if len(out) != 1 || out[0] != gate.ReasonOwnership {
		t.Fatalf("outstanding = %v, want [ownership] — the score should have cleared scan and only scan", out)
	}
}

// A version held for scan alone must still auto-release, exactly as before.
func TestReconsiderStillReleasesAScanOnlyHold(t *testing.T) {
	ctx := context.Background()
	store, name := heldForScanOnly(t)

	rel := skill.NewReleaser(store)
	_, released, err := rel.Reconsider(ctx, store.Pool(), name, 1, skill.QualityEvidence{
		Verified: true, PanelComplete: true, Headline: 100,
	}, 0)
	if err != nil {
		t.Fatalf("reconsider: %v", err)
	}
	if !released {
		t.Fatal("a verified score no longer releases a scan-only hold; this is a regression")
	}
}
