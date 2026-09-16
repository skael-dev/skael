package contest_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/contest"
	"github.com/skael-dev/skael/internal/testutil"
)

func insertSkill(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO skills (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func newContest(t *testing.T, pool *pgxpool.Pool) (*contest.Store, *contest.Contest) {
	t.Helper()
	s := contest.NewStore(pool)
	c := &contest.Contest{
		SuiteProvenance: contest.ProvenanceDerivedAll,
		Tier:            "full",
		Attempts:        contest.DefaultAttempts,
		RequestedBy:     "nathan@example.com",
		Candidates: []contest.Candidate{
			{SkillID: insertSkill(t, pool, "payments:deploy"), SkillName: "payments:deploy", Version: 3, Label: "payments:deploy@3"},
			{SkillID: insertSkill(t, pool, "platform:deploy"), SkillName: "platform:deploy", Version: 7, Label: "platform:deploy@7"},
		},
	}
	if err := s.Create(context.Background(), c); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return s, c
}

func TestStore_CarriesAVerdictAndItsEvidence(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s, c := newContest(t, pool)

	verdict := contest.Verdict{
		Outcome: contest.OutcomeWinner,
		Winner:  "payments:deploy@3",
		Detail:  "payments:deploy@3 wins",
		Pairings: []contest.Pairing{{
			A: "payments:deploy@3", B: "platform:deploy@7", AWins: 6, BWins: 0, Ties: 6, PValue: 0.03,
		}},
	}
	reports := map[string]json.RawMessage{
		"payments:deploy@3": json.RawMessage(`{"skill":"payments:deploy"}`),
		"platform:deploy@7": json.RawMessage(`{"skill":"platform:deploy"}`),
	}
	losses := []contest.TaskLoss{
		{Label: "platform:deploy@7", TaskID: "4", Missed: []string{"tags the release"}, Evidence: "no tag was created"},
	}
	if err := s.Finish(ctx, c.ID, verdict, reports, losses); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	got, err := s.Get(ctx, c.ID)
	if err != nil || got == nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != contest.StatusDone || got.Winner != "payments:deploy@3" {
		t.Errorf("status %s winner %q, want done/payments:deploy@3", got.Status, got.Winner)
	}
	if len(got.Pairings) != 1 || got.Pairings[0].AWins != 6 {
		t.Errorf("pairings lost: %+v", got.Pairings)
	}
	if len(got.Losses) != 1 || got.Losses[0].Missed[0] != "tags the release" {
		t.Fatalf("losses lost: %+v", got.Losses)
	}
	for _, cand := range got.Candidates {
		if len(cand.Report) == 0 {
			t.Errorf("%s kept no report; a verdict without its evidence cannot be argued with", cand.Label)
		}
	}
}

// A contest between two different skills is not a chain link, and the answer
// comes from the candidates rather than a stored flag that could disagree.
func TestStore_ChainLinkIsReadFromTheCandidates(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s, c := newContest(t, pool)

	got, err := s.Get(ctx, c.ID)
	if err != nil || got == nil {
		t.Fatalf("Get: %v", err)
	}
	if got.IsChainLink() {
		t.Error("two different skills read as a chain link")
	}

	skillID := insertSkill(t, pool, "deploy-helper")
	link := &contest.Contest{
		SuiteProvenance: contest.ProvenanceDerivedAll, Tier: "full", Attempts: 3,
		Candidates: []contest.Candidate{
			{SkillID: skillID, SkillName: "deploy-helper", Version: 3, Label: "deploy-helper@3"},
			{SkillID: skillID, SkillName: "deploy-helper", Version: 4, Label: "deploy-helper@4"},
		},
	}
	if err := s.Create(ctx, link); err != nil {
		t.Fatalf("Create: %v", err)
	}
	stored, err := s.Get(ctx, link.ID)
	if err != nil || stored == nil {
		t.Fatalf("Get: %v", err)
	}
	if !stored.IsChainLink() {
		t.Error("two versions of one skill did not read as a chain link")
	}
}

// The claim path must hand the worker every candidate, in order, with the
// attempts the contest asked for.
func TestStore_ServesEveryCandidateToTheClaimingWorker(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	s, c := newContest(t, pool)

	var jobID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO eval_jobs (skill_id, skill_name, version, suite_ref, contest_id)
		VALUES ($1, $2, $3, '', $4) RETURNING id`,
		c.Candidates[0].SkillID, c.Candidates[0].SkillName, c.Candidates[0].Version, c.ID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachJob(ctx, c.ID, jobID); err != nil {
		t.Fatalf("AttachJob: %v", err)
	}

	cands, attempts, err := s.CandidatesForJob(ctx, jobID)
	if err != nil {
		t.Fatalf("CandidatesForJob: %v", err)
	}
	if len(cands) != 2 || cands[0].Label != "payments:deploy@3" || cands[1].Label != "platform:deploy@7" {
		t.Fatalf("candidates = %+v, want both in order", cands)
	}
	if attempts != contest.DefaultAttempts {
		t.Errorf("attempts = %d, want %d", attempts, contest.DefaultAttempts)
	}
}

func TestStore_RefusesASingleCandidate(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := contest.NewStore(pool)
	err := s.Create(context.Background(), &contest.Contest{
		SuiteProvenance: contest.ProvenanceDerivedAll, Tier: "full", Attempts: 3,
		Candidates: []contest.Candidate{
			{SkillID: insertSkill(t, pool, "solo"), SkillName: "solo", Version: 1, Label: "solo@1"},
		},
	})
	if err == nil {
		t.Fatal("a one-candidate contest was created")
	}
}
