package quality_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestStore_UpsertThenLatestReturnsTheNewestRow(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s := quality.NewStore(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	base := quality.Record{SkillID: skillID, Version: 2, SuiteRef: "r", Tier: "full",
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`{}`),
		DriftBreakdown: json.RawMessage(`{}`), ModelPanel: json.RawMessage(`[]`)}

	base.Headline, base.ScoredAt = 50, time.Now().Add(-time.Hour)
	if err := s.Upsert(ctx, base); err != nil {
		t.Fatal(err)
	}
	base.Headline, base.ScoredAt, base.Verified = 80, time.Now(), true
	if err := s.Upsert(ctx, base); err != nil {
		t.Fatal(err)
	}
	got, err := s.Latest(ctx, skillID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Headline != 80 || !got.Verified {
		t.Fatalf("latest = %+v, want headline 80 verified", got)
	}
	hist, _ := s.History(ctx, skillID)
	if len(hist) != 2 {
		t.Fatalf("history = %d rows, want 2 — a re-score must not destroy the earlier measurement", len(hist))
	}
}

// insertSkill creates the skills row the foreign key needs.
func insertSkill(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO skills (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
