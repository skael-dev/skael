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

// Condition identifies which side of a comparison a run belongs to: the
// skill condition, the baseline condition, or a trigger probe. It is the
// shared representation store.RunKey and report.ConditionReport both use, so
// the concept has one type across the eval engine rather than a bare string
// re-encoded at each consumer.
//
// This is a different vocabulary from a judge's verdict (which candidate
// won a pairwise comparison — "skill"/"baseline"/"tie") even though the two
// happen to share two words: a Condition names which run produced a
// measurement, a verdict names which measurement came out ahead.
type Condition string

// RunKey identifies one session within an eval: a task, an agent/model pair,
// the skill/baseline condition, and an attempt number. The UNIQUE constraint
// on runs is keyed on exactly these fields.
type RunKey struct {
	TaskID    string
	Agent     string
	Model     string
	Condition Condition
	Attempt   int
}

// RunOutcome is what a finished (or failed-to-run) session reported.
type RunOutcome struct {
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

// RunGrade is one session's graded expectations, as stored. Doc is the
// per-expectation detail, opaque here so this package never imports score.
type RunGrade struct {
	Key    RunKey
	Passed int
	Total  int
	Doc    []byte
}

// ScoreRow is one agent/model pair's computed score for an eval.
type ScoreRow struct {
	Agent         string
	Model         string
	Effectiveness float64
	// Healthy is false when this member's adapter failed its probe. Every
	// other field on such a row is a zero value that must not be read as a
	// real measurement.
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
		`UPDATE runs SET status = ?, input_tokens = ?, output_tokens = ?, duration_ms = ?,
		 agent_version = ?, rate_limited = ?, error = ?, artifact_dir = ? WHERE id = ?`,
		o.Status, o.InputTokens, o.OutputTokens, o.DurationMS,
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
		`SELECT id, task_id, agent, model, condition, attempt, artifact_dir,
		 input_tokens, output_tokens, duration_ms, agent_version, rate_limited, status, error
		 FROM runs WHERE eval_id = ? ORDER BY id`, evalID)
	if err != nil {
		return nil, fmt.Errorf("store.Runs: %w", err)
	}
	defer rows.Close()

	var out []RunRecord
	for rows.Next() {
		var (
			r           RunRecord
			rateLimited int64
		)
		if err := rows.Scan(&r.ID, &r.Key.TaskID, &r.Key.Agent, &r.Key.Model, &r.Key.Condition, &r.Key.Attempt,
			&r.Outcome.ArtifactDir, &r.Outcome.InputTokens, &r.Outcome.OutputTokens,
			&r.Outcome.DurationMS, &r.Outcome.AgentVersion, &rateLimited, &r.Outcome.Status, &r.Outcome.Error); err != nil {
			return nil, fmt.Errorf("store.Runs scan: %w", err)
		}
		r.Outcome.RateLimited = rateLimited != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveGrade upserts one session's grade. Upsert rather than insert: a
// re-graded run recomputes the same key, and a unique-constraint failure there
// would abort the eval.
func (s *Store) SaveGrade(evalID int64, g RunGrade) error {
	doc := g.Doc
	if len(doc) == 0 {
		doc = []byte("[]")
	}
	_, err := s.db.Exec(
		`INSERT INTO run_grades (eval_id, task_id, agent, model, condition, attempt, passed, total, doc)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(eval_id, task_id, agent, model, condition, attempt) DO UPDATE SET
		   passed = excluded.passed, total = excluded.total, doc = excluded.doc`,
		evalID, g.Key.TaskID, g.Key.Agent, g.Key.Model, string(g.Key.Condition), g.Key.Attempt,
		g.Passed, g.Total, string(doc))
	if err != nil {
		return fmt.Errorf("store.SaveGrade: %w", err)
	}
	return nil
}

// Grades lists every stored grade for an eval, the read side of SaveGrade.
func (s *Store) Grades(evalID int64) ([]RunGrade, error) {
	rows, err := s.db.Query(
		`SELECT task_id, agent, model, condition, attempt, passed, total, doc
		 FROM run_grades WHERE eval_id = ? ORDER BY task_id, agent, model, condition, attempt`, evalID)
	if err != nil {
		return nil, fmt.Errorf("store.Grades: %w", err)
	}
	defer rows.Close()

	var out []RunGrade
	for rows.Next() {
		var (
			g   RunGrade
			doc string
		)
		if err := rows.Scan(&g.Key.TaskID, &g.Key.Agent, &g.Key.Model, &g.Key.Condition,
			&g.Key.Attempt, &g.Passed, &g.Total, &doc); err != nil {
			return nil, fmt.Errorf("store.Grades scan: %w", err)
		}
		g.Doc = []byte(doc)
		out = append(out, g)
	}
	return out, rows.Err()
}

// SaveScore upserts one agent/model pair's scores for an eval. Upsert rather
// than insert: a re-scored eval recomputes the same (eval_id, agent, model)
// key, and a unique-constraint failure there would abort the run.
func (s *Store) SaveScore(evalID int64, sc ScoreRow) error {
	_, err := s.db.Exec(
		`INSERT INTO scores (eval_id, agent, model, effectiveness, healthy)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(eval_id, agent, model) DO UPDATE SET
		   effectiveness = excluded.effectiveness, healthy = excluded.healthy`,
		evalID, sc.Agent, sc.Model, sc.Effectiveness, boolToInt(sc.Healthy))
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

// ReportMeta returns the small set of report fields the store indexes
// directly for evalID, the read side of SaveReport's ReportMeta write.
// RobustnessGap round-trips as nil when it was stored as SQL NULL (not
// computed), never silently as 0 — mirroring the write-side comment on
// ReportMeta itself.
func (s *Store) ReportMeta(evalID int64) (ReportMeta, error) {
	var (
		m             ReportMeta
		panelComplete int64
		gap           sql.NullFloat64
	)
	err := s.db.QueryRow(`SELECT headline, panel_complete, robustness_gap FROM reports WHERE eval_id = ?`, evalID).
		Scan(&m.Headline, &panelComplete, &gap)
	if errors.Is(err, sql.ErrNoRows) {
		return ReportMeta{}, fmt.Errorf("store.ReportMeta: no report for eval %d", evalID)
	}
	if err != nil {
		return ReportMeta{}, fmt.Errorf("store.ReportMeta: %w", err)
	}
	m.PanelComplete = panelComplete != 0
	if gap.Valid {
		g := gap.Float64
		m.RobustnessGap = &g
	}
	return m, nil
}

// Scores lists every agent/model pair's stored scores for an eval, the read
// side of SaveScore.
func (s *Store) Scores(evalID int64) ([]ScoreRow, error) {
	rows, err := s.db.Query(
		`SELECT agent, model, effectiveness, healthy
		 FROM scores WHERE eval_id = ? ORDER BY agent, model`, evalID)
	if err != nil {
		return nil, fmt.Errorf("store.Scores: %w", err)
	}
	defer rows.Close()

	var out []ScoreRow
	for rows.Next() {
		var (
			r       ScoreRow
			healthy int64
		)
		if err := rows.Scan(&r.Agent, &r.Model, &r.Effectiveness, &healthy); err != nil {
			return nil, fmt.Errorf("store.Scores scan: %w", err)
		}
		r.Healthy = healthy != 0
		out = append(out, r)
	}
	return out, rows.Err()
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
	condition := sanitizePathComponent(string(k.Condition))
	leaf := fmt.Sprintf("%s__%s__%s__%d", agent, model, condition, k.Attempt)

	return filepath.Join(dir, "runs", strconv.FormatInt(evalID, 10), taskID, leaf), nil
}
