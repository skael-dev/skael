package evalqueue_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestPool_SubmitAndGet(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")

	id, err := q.Submit(context.Background(), evalqueue.Job{
		SkillID: skillID, SkillName: "deploy-helper", Version: 3,
		SuiteRef: "sha256:abcd", Tier: "full",
		Panel:       evalqueue.Panel{Agents: []string{"claude-code"}, Models: []string{"opus", "haiku"}},
		RequestedBy: "nate@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := q.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != evalqueue.StatusQueued {
		t.Fatalf("status = %q, want queued", got.Status)
	}
	if len(got.Panel.Models) != 2 || got.Panel.Models[1] != "haiku" {
		t.Fatalf("panel round-trip lost data: %+v", got.Panel)
	}
	if got.MaxAttempts != 3 {
		t.Fatalf("max attempts = %d, want 3", got.MaxAttempts)
	}
}

func TestPool_CancelQueuedJob(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	q := evalqueue.NewPool(pool)
	skillID := insertSkill(t, pool, "deploy-helper")
	id, _ := q.Submit(context.Background(), evalqueue.Job{
		SkillID: skillID, SkillName: "deploy-helper", Version: 1, SuiteRef: "sha256:abcd",
	})
	if err := q.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	got, _ := q.Get(context.Background(), id)
	if got.Status != evalqueue.StatusCancelled {
		t.Fatalf("status = %q, want cancelled", got.Status)
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
