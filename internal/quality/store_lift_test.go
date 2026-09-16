package quality_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestStore_LiftKeepsNotMeasuredApartFromZero(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "deploy-helper")

	base := quality.Record{SkillID: skillID, SuiteRef: "r", Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`{}`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`),
		Headline: 38, ScoredAt: time.Now()}

	measured := base
	measured.Version = 1
	lift, baseline, primary := 27.0, 44.0, 71.0
	measured.Lift, measured.Baseline, measured.PrimaryScore = &lift, &baseline, &primary
	measured.UpliftSource = "reused"
	if err := s.Upsert(ctx, measured); err != nil {
		t.Fatal(err)
	}

	unmeasured := base
	unmeasured.Version = 2
	if err := s.Upsert(ctx, unmeasured); err != nil {
		t.Fatal(err)
	}

	got, err := s.Latest(ctx, skillID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lift == nil || *got.Lift != 27 || got.Baseline == nil || *got.Baseline != 44 {
		t.Fatalf("measured row lost the comparison: %+v", got)
	}
	if got.PrimaryScore == nil || *got.PrimaryScore != 71 {
		t.Fatalf("measured row lost the primary score: %+v", got)
	}
	if got.UpliftSource != "reused" {
		t.Fatalf("uplift source = %q, want reused", got.UpliftSource)
	}

	got, err = s.Latest(ctx, skillID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lift != nil || got.Baseline != nil || got.PrimaryScore != nil {
		t.Fatalf("unmeasured row came back as a measurement: %+v", got)
	}
}
