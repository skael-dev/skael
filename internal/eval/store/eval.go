package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Run statuses. ClaimRun treats StatusOK and StatusFailed as done: a run
// that executed and whose verifier failed is still a completed
// measurement — a failed measurement is a measurement — and re-running it
// would waste a session without changing the answer. StatusError and
// StatusTimeout mean the run could not be performed at all and are retried,
// and a claimed-but-unfinished run (StatusClaimed) always is, because a
// process killed mid-run must not silently drop a session from a
// denominator.
const (
	StatusClaimed = "claimed"
	StatusOK      = "ok"
	StatusFailed  = "failed"
	StatusError   = "error"
	StatusTimeout = "timeout"
)

// evalTimeLayout is how StartedAt/FinishedAt are stored: application-supplied
// timestamps, not SQLite's own datetime('now') (which specs.created_at uses
// and formats differently), so they round-trip through time.Parse exactly.
const evalTimeLayout = time.RFC3339Nano

// EvalRecord is one evaluation run: the skill, spec version, and suite it was
// measured against, plus its lifecycle status.
type EvalRecord struct {
	ID            int64
	Skill         string
	SpecVersion   int
	Tier          string
	SuiteRef      string
	EngineVersion string
	ModelPanel    []byte
	Seed          int64
	StartedAt     time.Time
	FinishedAt    time.Time
	Status        string
}

// RunKey identifies one session within an eval: a task, an agent/model pair,
// the skill/baseline condition, and an attempt number. The UNIQUE constraint
// on runs is keyed on exactly these fields.
type RunKey struct {
	TaskID    string
	Agent     string
	Model     string
	Condition string
	Attempt   int
}

// RunOutcome is what a finished (or failed-to-run) session reported.
type RunOutcome struct {
	// VerifierExit is nil when the verifier never ran — a trigger probe (which
	// has no verifier at all) or a run that failed before reaching it — and
	// the verifier's exit code otherwise, including 0. A caller that reads
	// *VerifierExit == 0 without checking it is non-nil first is the one bug
	// this distinction exists to prevent: "not measured" and "measured and
	// passed" must never collapse into the same value.
	VerifierExit *int
	InputTokens  int64
	OutputTokens int64
	DurationMS   int64
	AgentVersion string
	RateLimited  bool
	Status       string
	Error        string
	ArtifactDir  string
}

// RunRecord is one stored run.
type RunRecord struct {
	ID      int64
	Key     RunKey
	Outcome RunOutcome
}

// Judgment is one LLM-judge (or rule-based) verdict recorded against an eval.
type Judgment struct {
	TaskID   string
	Model    string
	Kind     string
	RuleID   string
	Winner   string
	Margin   float64
	Evidence string
	Votes    int
	Swapped  bool
}

// ScoreRow is one agent/model pair's computed scores for an eval.
type ScoreRow struct {
	Agent         string
	Model         string
	TriggerF1     float64
	Reliability   float64
	Uplift        float64
	Efficiency    float64
	Effectiveness float64
	Adherence     float64
	Drift         float64
	Grade         string
	// Healthy is false when this member's adapter failed its probe, mirroring
	// score.PanelEntry.Healthy. Every other field on such a row is a zero
	// value that must not be read as a real measurement.
	Healthy bool
}

// ReportMeta is the small set of report fields the store indexes directly,
// alongside the opaque report document itself.
type ReportMeta struct {
	Headline      float64
	PanelComplete bool
	// RobustnessGap is nil when it was not computed for this report (the
	// strong/floor comparison was not well defined), mirroring
	// report.Report.RobustnessGap. A bare float64 here would collapse "not
	// computed" and "computed at zero" to the same stored value.
	RobustnessGap *float64
}

// SuiteCheckRow records whether one task in a suite was found void (its
// oracle or verifier no longer applies) when the suite was last checked.
type SuiteCheckRow struct {
	TaskID string
	Void   bool
	Reason string
}

// boolToInt converts a Go bool to the 0/1 SQLite stores it as. Binding a bool
// directly is not relied on here — better an explicit conversion than a
// silent driver-dependent behavior.
func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// CreateEval starts a new eval record and returns its id.
func (s *Store) CreateEval(e EvalRecord) (int64, error) {
	modelPanel := e.ModelPanel
	if modelPanel == nil {
		modelPanel = []byte("[]")
	}
	started := e.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}

	res, err := s.db.Exec(
		`INSERT INTO evals (skill_name, spec_version, tier, suite_ref, engine_version, model_panel, seed, started_at, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Skill, e.SpecVersion, e.Tier, e.SuiteRef, e.EngineVersion, string(modelPanel), e.Seed,
		started.UTC().Format(evalTimeLayout), e.Status)
	if err != nil {
		return 0, fmt.Errorf("store.CreateEval: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store.CreateEval: %w", err)
	}
	return id, nil
}

// FinishEval marks an eval finished with a terminal status and stamps
// finished_at.
func (s *Store) FinishEval(id int64, status string) error {
	res, err := s.db.Exec(`UPDATE evals SET status = ?, finished_at = ? WHERE id = ?`,
		status, time.Now().UTC().Format(evalTimeLayout), id)
	if err != nil {
		return fmt.Errorf("store.FinishEval: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store.FinishEval: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store.FinishEval: no eval %d", id)
	}
	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so scanEvalRow
// works for both Eval/LatestEval (single row) and a future list query.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanEvalRow reads one evals row, including the two timestamp columns which
// are stored in evalTimeLayout rather than SQLite's own datetime() format.
func scanEvalRow(row rowScanner) (*EvalRecord, error) {
	var (
		e          EvalRecord
		modelPanel string
		started    string
		finished   sql.NullString
	)
	err := row.Scan(&e.ID, &e.Skill, &e.SpecVersion, &e.Tier, &e.SuiteRef, &e.EngineVersion,
		&modelPanel, &e.Seed, &started, &finished, &e.Status)
	if err != nil {
		return nil, err
	}
	e.ModelPanel = []byte(modelPanel)

	st, err := time.Parse(evalTimeLayout, started)
	if err != nil {
		return nil, fmt.Errorf("store: parsing started_at %q: %w", started, err)
	}
	e.StartedAt = st

	if finished.Valid && finished.String != "" {
		ft, err := time.Parse(evalTimeLayout, finished.String)
		if err != nil {
			return nil, fmt.Errorf("store: parsing finished_at %q: %w", finished.String, err)
		}
		e.FinishedAt = ft
	}
	return &e, nil
}

const evalColumns = `id, skill_name, spec_version, tier, suite_ref, engine_version, model_panel, seed, started_at, finished_at, status`

// Eval returns one eval by id.
func (s *Store) Eval(id int64) (*EvalRecord, error) {
	row := s.db.QueryRow(`SELECT `+evalColumns+` FROM evals WHERE id = ?`, id)
	e, err := scanEvalRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store.Eval: no eval %d", id)
	}
	if err != nil {
		return nil, fmt.Errorf("store.Eval: %w", err)
	}
	return e, nil
}

// LatestEval returns the most recently created eval for a skill.
func (s *Store) LatestEval(skill string) (*EvalRecord, error) {
	row := s.db.QueryRow(`SELECT `+evalColumns+` FROM evals WHERE skill_name = ? ORDER BY id DESC LIMIT 1`, skill)
	e, err := scanEvalRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store.LatestEval: no eval for %q", skill)
	}
	if err != nil {
		return nil, fmt.Errorf("store.LatestEval: %w", err)
	}
	return e, nil
}

// ClaimRun records the intent to execute one run and reports whether it has
// already finished. Resume is built entirely on this: a finished key is
// skipped and a claimed-but-unfinished key is handed back for another
// attempt, because a process killed mid-run must not silently drop a session
// from a denominator.
func (s *Store) ClaimRun(evalID int64, k RunKey) (int64, bool, error) {
	var id int64
	var status string
	err := s.db.QueryRow(
		`SELECT id, status FROM runs
		 WHERE eval_id = ? AND task_id = ? AND agent = ? AND model = ? AND condition = ? AND attempt = ?`,
		evalID, k.TaskID, k.Agent, k.Model, k.Condition, k.Attempt).Scan(&id, &status)
	switch {
	case err == nil:
		return id, status == StatusOK || status == StatusFailed, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, false, fmt.Errorf("store.ClaimRun: %w", err)
	}

	res, err := s.db.Exec(
		`INSERT INTO runs (eval_id, task_id, agent, model, condition, attempt, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		evalID, k.TaskID, k.Agent, k.Model, k.Condition, k.Attempt, StatusClaimed)
	if err != nil {
		return 0, false, fmt.Errorf("store.ClaimRun: %w", err)
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, false, fmt.Errorf("store.ClaimRun: %w", err)
	}
	return id, false, nil
}

// FinishRun records a run's outcome. o.Status is expected to be one of
// StatusOK, StatusFailed, StatusError, or StatusTimeout: the first two make a
// later ClaimRun of the same key report done, the latter two make it retry.
func (s *Store) FinishRun(id int64, o RunOutcome) error {
	res, err := s.db.Exec(
		`UPDATE runs SET status = ?, verifier_exit = ?, input_tokens = ?, output_tokens = ?, duration_ms = ?,
		 agent_version = ?, rate_limited = ?, error = ?, artifact_dir = ? WHERE id = ?`,
		o.Status, o.VerifierExit, o.InputTokens, o.OutputTokens, o.DurationMS,
		o.AgentVersion, boolToInt(o.RateLimited), o.Error, o.ArtifactDir, id)
	if err != nil {
		return fmt.Errorf("store.FinishRun: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store.FinishRun: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store.FinishRun: no run %d", id)
	}
	return nil
}

// Runs lists every run recorded for an eval, in claim order.
func (s *Store) Runs(evalID int64) ([]RunRecord, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, agent, model, condition, attempt, artifact_dir, verifier_exit,
		 input_tokens, output_tokens, duration_ms, agent_version, rate_limited, status, error
		 FROM runs WHERE eval_id = ? ORDER BY id`, evalID)
	if err != nil {
		return nil, fmt.Errorf("store.Runs: %w", err)
	}
	defer rows.Close()

	var out []RunRecord
	for rows.Next() {
		var (
			r            RunRecord
			verifierExit sql.NullInt64
			rateLimited  int64
		)
		if err := rows.Scan(&r.ID, &r.Key.TaskID, &r.Key.Agent, &r.Key.Model, &r.Key.Condition, &r.Key.Attempt,
			&r.Outcome.ArtifactDir, &verifierExit, &r.Outcome.InputTokens, &r.Outcome.OutputTokens,
			&r.Outcome.DurationMS, &r.Outcome.AgentVersion, &rateLimited, &r.Outcome.Status, &r.Outcome.Error); err != nil {
			return nil, fmt.Errorf("store.Runs scan: %w", err)
		}
		if verifierExit.Valid {
			v := int(verifierExit.Int64)
			r.Outcome.VerifierExit = &v
		}
		r.Outcome.RateLimited = rateLimited != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveJudgment records one judge verdict against an eval.
func (s *Store) SaveJudgment(evalID int64, j Judgment) error {
	_, err := s.db.Exec(
		`INSERT INTO judgments (eval_id, task_id, model, kind, rule_id, winner, margin, evidence, votes, swapped)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		evalID, j.TaskID, j.Model, j.Kind, j.RuleID, j.Winner, j.Margin, j.Evidence, j.Votes, boolToInt(j.Swapped))
	if err != nil {
		return fmt.Errorf("store.SaveJudgment: %w", err)
	}
	return nil
}

// SaveScore upserts one agent/model pair's scores for an eval. Upsert rather
// than insert: a re-scored eval recomputes the same (eval_id, agent, model)
// key, and a unique-constraint failure there would abort the run.
func (s *Store) SaveScore(evalID int64, sc ScoreRow) error {
	_, err := s.db.Exec(
		`INSERT INTO scores (eval_id, agent, model, trigger_f1, reliability, uplift, efficiency, effectiveness, adherence, drift, grade, healthy)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(eval_id, agent, model) DO UPDATE SET
		   trigger_f1 = excluded.trigger_f1, reliability = excluded.reliability, uplift = excluded.uplift,
		   efficiency = excluded.efficiency, effectiveness = excluded.effectiveness, adherence = excluded.adherence,
		   drift = excluded.drift, grade = excluded.grade, healthy = excluded.healthy`,
		evalID, sc.Agent, sc.Model, sc.TriggerF1, sc.Reliability, sc.Uplift, sc.Efficiency, sc.Effectiveness,
		sc.Adherence, sc.Drift, sc.Grade, boolToInt(sc.Healthy))
	if err != nil {
		return fmt.Errorf("store.SaveScore: %w", err)
	}
	return nil
}

// SaveReport stores a report as opaque JSON bytes, alongside the few fields
// the store indexes directly. Reports are stored opaque rather than typed so
// that this package never imports report — report imports score and drift,
// and importing report back here would create a cycle.
func (s *Store) SaveReport(evalID int64, doc []byte, m ReportMeta) error {
	_, err := s.db.Exec(
		`INSERT INTO reports (eval_id, doc, headline, panel_complete, robustness_gap)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(eval_id) DO UPDATE SET
		   doc = excluded.doc, headline = excluded.headline, panel_complete = excluded.panel_complete,
		   robustness_gap = excluded.robustness_gap`,
		evalID, string(doc), m.Headline, boolToInt(m.PanelComplete), sqlNullFloat(m.RobustnessGap))
	if err != nil {
		return fmt.Errorf("store.SaveReport: %w", err)
	}
	return nil
}

// sqlNullFloat converts a *float64 into the sql.NullFloat64 the database/sql
// driver needs to bind a nullable column: a nil pointer becomes SQL NULL
// rather than a bound 0, which would collapse "not computed" into "computed
// at zero" the same way the old NOT NULL DEFAULT 0 column did.
func sqlNullFloat(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}

// Report returns the stored report document for one eval.
func (s *Store) Report(evalID int64) ([]byte, error) {
	var doc string
	err := s.db.QueryRow(`SELECT doc FROM reports WHERE eval_id = ?`, evalID).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store.Report: no report for eval %d", evalID)
	}
	if err != nil {
		return nil, fmt.Errorf("store.Report: %w", err)
	}
	return []byte(doc), nil
}

// LatestReport returns the report document for a skill's most recent eval,
// and that eval's id.
func (s *Store) LatestReport(skill string) ([]byte, int64, error) {
	var (
		doc string
		id  int64
	)
	err := s.db.QueryRow(
		`SELECT r.eval_id, r.doc FROM reports r JOIN evals e ON e.id = r.eval_id
		 WHERE e.skill_name = ? ORDER BY r.eval_id DESC LIMIT 1`, skill).Scan(&id, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, fmt.Errorf("store.LatestReport: no report for %q", skill)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("store.LatestReport: %w", err)
	}
	return []byte(doc), id, nil
}

// SaveSuiteCheck upserts, per task, whether it was found void the last time
// the suite was checked.
func (s *Store) SaveSuiteCheck(skill, suiteRef string, rows []SuiteCheckRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store.SaveSuiteCheck begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(
		`INSERT INTO suite_checks (skill_name, suite_ref, task_id, void, reason)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(skill_name, suite_ref, task_id) DO UPDATE SET
		   void = excluded.void, reason = excluded.reason, checked_at = datetime('now')`)
	if err != nil {
		return fmt.Errorf("store.SaveSuiteCheck prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, r := range rows {
		if _, err := stmt.Exec(skill, suiteRef, r.TaskID, boolToInt(r.Void), r.Reason); err != nil {
			return fmt.Errorf("store.SaveSuiteCheck: %w", err)
		}
	}
	return tx.Commit()
}

// SuiteChecks lists the last-known void status of every checked task in a
// suite.
func (s *Store) SuiteChecks(skill, suiteRef string) ([]SuiteCheckRow, error) {
	rows, err := s.db.Query(
		`SELECT task_id, void, reason FROM suite_checks WHERE skill_name = ? AND suite_ref = ? ORDER BY task_id`,
		skill, suiteRef)
	if err != nil {
		return nil, fmt.Errorf("store.SuiteChecks: %w", err)
	}
	defer rows.Close()

	var out []SuiteCheckRow
	for rows.Next() {
		var (
			r    SuiteCheckRow
			void int64
		)
		if err := rows.Scan(&r.TaskID, &void, &r.Reason); err != nil {
			return nil, fmt.Errorf("store.SuiteChecks scan: %w", err)
		}
		r.Void = void != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// sanitizePathComponent replaces characters that could escape or subdivide a
// path component RunDir composes. Model, agent, condition, and task id all
// end up as directory names and all ultimately trace back to CLI input or a
// model-authored suite (see suite.safeJoin), so none of them are trusted not
// to contain a path separator or a traversal segment.
//
// Every literal underscore is also mapped away (to "-"), so no sanitized
// component can ever contain "_". That is what makes RunDir's "__" field
// separator unambiguous: a run-of-the-mill hyphen in a model name
// ("claude-code", "gpt-4o-mini") is common enough to be the normal case, and
// without a separator guaranteed absent from every component, two different
// RunKeys can flatten to the same leaf and silently overwrite each other's
// transcript.
func sanitizePathComponent(s string) string {
	s = strings.ReplaceAll(s, string(filepath.Separator), "-")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\x00", "-")
	s = strings.ReplaceAll(s, "_", "-")
	switch s {
	case "", ".", "..":
		return "-"
	default:
		return s
	}
}

// RunDir is the artifact directory for one run, inside the skill's eval
// sidecar: <evalDir>/runs/<evalID>/<taskID>/<agent>__<model>__<condition>__<attempt>/.
// skill is validated the same way SkillDir validates it; every RunKey field
// is sanitised before joining, since each one traces back to untrusted
// input — and sanitizePathComponent guarantees none of them can contain the
// "__" used to join them, so the leaf splits back into its four fields
// unambiguously.
func (s *Store) RunDir(skill string, evalID int64, k RunKey) (string, error) {
	dir, err := s.EvalDir(skill)
	if err != nil {
		return "", err
	}

	taskID := sanitizePathComponent(k.TaskID)
	agent := sanitizePathComponent(k.Agent)
	model := sanitizePathComponent(k.Model)
	condition := sanitizePathComponent(k.Condition)
	leaf := fmt.Sprintf("%s__%s__%s__%d", agent, model, condition, k.Attempt)

	return filepath.Join(dir, "runs", strconv.FormatInt(evalID, 10), taskID, leaf), nil
}
