package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/eval/report"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/quality"
	"github.com/skael-dev/skael/internal/skill"
)

// SampleSuite marks the sample scores and the sample contest.
const SampleSuite = "sample:onboarding"

type sampleVersion struct {
	body                        string
	headline, primary, baseline float64
}

type sampleSkill struct {
	name, description string
	versions          []sampleVersion
}

var sampleSkills = []sampleSkill{
	{"sample-code-review", "Reviews a diff for correctness bugs before a pull request is opened.", []sampleVersion{
		{"Read the diff. List each bug with the file and line. Skip style.", 61, 64, 52},
		{"Read the diff and the tests it touches. List each correctness bug with the file, the line and a failing input. Skip style and naming.", 74, 78, 52},
	}},
	{"sample-commit-messages", "Writes a commit subject and a short paragraph of why.", []sampleVersion{
		{"Write a subject under 60 characters in the imperative mood. Then one paragraph that says why the change is needed.", 82, 85, 71},
	}},
	{"sample-incident-runbook", "Walks an on-call engineer through the first fifteen minutes of an incident.", []sampleVersion{
		{"Confirm the impact. Page the owner. Freeze deploys. Open the incident channel. Write the first status update.", 47, 49, 55},
	}},
}

var sampleModels = []string{"claude-sonnet-5", "claude-sonnet-5", "claude-opus-5", "gpt-5-codex", "claude-haiku-4-5"}
var sampleHarnesses = []struct{ agent, version string }{
	{"claude-code", "2.1.4"}, {"claude-code", "2.1.4"}, {"claude-code", "2.0.9"}, {"codex", "0.48.0"}, {"cursor", ""},
}

// Sample loads and removes the sample workspace.
type Sample struct {
	pool      *pgxpool.Pool
	skills    *skill.Store
	publisher *skill.Publisher
	quality   *quality.Store
	storage   platform.Storage
}

// NewSample returns the loader.
func NewSample(pool *pgxpool.Pool, skills *skill.Store, publisher *skill.Publisher, q *quality.Store, storage platform.Storage) *Sample {
	return &Sample{pool: pool, skills: skills, publisher: publisher, quality: q, storage: storage}
}

// Loaded reports whether sample data exists.
func (s *Sample) Loaded(ctx context.Context) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM skills WHERE sample)`).Scan(&ok)
	return ok, err
}

// Load adds the sample skills, scores, a contest verdict, six sample people and
// 30 days of activations. It refuses when sample data exists.
func (s *Sample) Load(ctx context.Context, actor string) error {
	if loaded, err := s.Loaded(ctx); err != nil || loaded {
		if err == nil {
			err = fmt.Errorf("sample data is already loaded")
		}
		return err
	}
	ids := map[string]string{}
	for _, sk := range sampleSkills {
		created, err := s.skills.Create(ctx, sk.name, "", sk.description, "", json.RawMessage(`{}`))
		if err != nil {
			return fmt.Errorf("sample: create %s: %w", sk.name, err)
		}
		ids[sk.name] = created.ID
		if _, err := s.pool.Exec(ctx, `UPDATE skills SET sample = true WHERE id = $1`, created.ID); err != nil {
			return err
		}
		for i, v := range sk.versions {
			archive, err := packSample(sk.name, sk.description, v.body)
			if err != nil {
				return err
			}
			if _, err := s.publisher.Publish(ctx, sk.name, archive, false); err != nil {
				return fmt.Errorf("sample: publish %s v%d: %w", sk.name, i+1, err)
			}
			if err := s.score(ctx, created.ID, sk.name, i+1, v); err != nil {
				return err
			}
		}
	}
	if err := s.contest(ctx, ids["sample-code-review"]); err != nil {
		return err
	}
	users, err := s.people(ctx)
	if err != nil {
		return err
	}
	if err := s.activations(ctx, users); err != nil {
		return err
	}
	return audit.Record(ctx, s.pool, actor, "sample.load", "sample", nil)
}

func packSample(name, description, body string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "skael-sample-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir) //nolint:errcheck
	md := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", name, description, body)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o600); err != nil {
		return nil, err
	}
	archive, _, _, err := skill.Pack(dir)
	return archive, err
}

func (s *Sample) score(ctx context.Context, skillID, name string, version int, v sampleVersion) error {
	panel := []report.PanelMember{
		{Agent: "claude-code", Model: "sonnet", Class: "strong", CLIVersion: "2.1.4"},
		{Agent: "claude-code", Model: "haiku", Class: "floor", CLIVersion: "2.1.4"},
	}
	rep := report.Report{SchemaVersion: report.SchemaVersion, Skill: name, Tier: "full", SuiteRef: SampleSuite,
		EngineVersion: "sample", ModelPanel: panel, PanelComplete: true, Headline: v.headline,
		FinishedAt: time.Now().Add(-time.Duration(3-version) * 24 * time.Hour)}
	raw, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	panelJSON, _ := json.Marshal(panel)
	primary, baseline, lift := v.primary, v.baseline, v.primary-v.baseline
	judge := "claude-opus-5"
	return s.quality.Upsert(ctx, quality.Record{
		SkillID: skillID, Version: version, Headline: v.headline, PrimaryScore: &primary, Baseline: &baseline, Lift: &lift,
		Pillars: json.RawMessage(`{}`), PanelMatrix: json.RawMessage(`[]`), DriftBreakdown: json.RawMessage(`{}`),
		Verified: true, PanelComplete: true, SuiteRef: SampleSuite, EngineVersion: "sample", ModelPanel: panelJSON,
		Tier: "full", UpliftSource: "measured", JudgeModel: &judge, ScoredAt: rep.FinishedAt, ReportJSON: raw,
	})
}

func (s *Sample) contest(ctx context.Context, skillID string) error {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO contests (suite_ref, suite_provenance, tier, attempts, status, outcome, winner, detail, requested_by, finished_at)
		VALUES ($1, 'authored', 'full', 3, 'done', 'winner', 'v2',
			'v2 won 11 of 14 tasks against v1; the sign test separates it from noise.', 'sample', now())
		RETURNING id`, SampleSuite).Scan(&id)
	if err != nil {
		return fmt.Errorf("sample: contest: %w", err)
	}
	for i, label := range []string{"v1", "v2"} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO contest_candidates (contest_id, position, skill_id, skill_name, version, label)
			VALUES ($1, $2, $3, 'sample-code-review', $4, $5)`, id, i, skillID, i+1, label); err != nil {
			return fmt.Errorf("sample: contest candidate: %w", err)
		}
	}
	return nil
}

func (s *Sample) people(ctx context.Context) ([]string, error) {
	names := []string{"Ada", "Grace", "Linus", "Margaret", "Ken", "Barbara"}
	var ids []string
	for i, n := range names {
		var id string
		email := fmt.Sprintf("sample-%d%s", i+1, SampleDomain)
		err := s.pool.QueryRow(ctx, `INSERT INTO auth_users (id, email, email_normalized, display_name, role, kind, created_at)
			VALUES (gen_random_uuid(), $1, $1, $2, 'member', 'human', now()) RETURNING id`, email, n+" (sample)").Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("sample: person: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *Sample) activations(ctx context.Context, users []string) error {
	r := rand.New(rand.NewPCG(7, 11))
	weights := []int{9, 5, 2}
	now := time.Now()
	for day := 0; day < 30; day++ {
		for si, sk := range sampleSkills {
			n := weights[si] + r.IntN(weights[si]+1)
			for range n {
				u := r.IntN(len(users))
				h := sampleHarnesses[u%len(sampleHarnesses)]
				at := now.Add(-time.Duration(day)*24*time.Hour - time.Duration(r.IntN(86400))*time.Second)
				version := len(sk.versions)
				_, err := s.pool.Exec(ctx, `
					INSERT INTO skill_events (skill_name, agent, trigger_type, project_hash, developer_hash, event_source,
						registered, user_id, model, version, harness_version, sample, created_at)
					VALUES ($1, $2, 'auto', $3, $4, 'tool_invocation', true, $5, $6, $7, NULLIF($8, ''), true, $9)`,
					sk.name, h.agent, fmt.Sprintf("sample%02d", u%3), fmt.Sprintf("sample-dev-%d", u), users[u],
					sampleModels[(u+day)%len(sampleModels)], version, h.version, at)
				if err != nil {
					return fmt.Errorf("sample: activation: %w", err)
				}
			}
		}
	}
	return nil
}

// Remove deletes every sample row and archive.
func (s *Sample) Remove(ctx context.Context, actor string) error {
	rows, err := s.pool.Query(ctx, `SELECT v.archive_path FROM skill_versions v JOIN skills sk ON sk.id = v.skill_id WHERE sk.sample`)
	if err != nil {
		return err
	}
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		paths = append(paths, p)
	}
	rows.Close()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for _, q := range []string{
		`DELETE FROM skill_events WHERE sample`,
		`DELETE FROM contests WHERE suite_ref = '` + SampleSuite + `'`,
		`DELETE FROM skills WHERE sample`,
		`DELETE FROM auth_users WHERE kind = 'human' AND email_normalized LIKE '%` + SampleDomain + `'`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return fmt.Errorf("sample: remove: %w", err)
		}
	}
	if err := audit.Record(ctx, tx, actor, "sample.remove", "sample", nil); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, p := range paths {
		_ = s.storage.Delete(ctx, p)
	}
	return nil
}
