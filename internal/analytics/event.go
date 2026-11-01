package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event represents a single skill activation telemetry record.
type Event struct {
	SkillName     string `json:"skill_name"`
	Agent         string `json:"agent"`
	TriggerType   string `json:"trigger_type"`
	ProjectHash   string `json:"project_hash"`
	DeveloperHash string `json:"developer_hash"`

	// EventSource records how the activation was observed. "tool_invocation" is
	// an agent reporting an explicit skill call; "transcript_scan" is a hook
	// deriving activations by reading a session transcript afterwards. The two
	// are not the same measurement and must never be summed unlabelled.
	EventSource string `json:"event_source"`
}

// ActivationSummary summarises activations for a specific skill over a time window.
type ActivationSummary struct {
	TotalCount    int            `json:"total_count"`
	UniqueDevs    int            `json:"unique_devs"`
	LastTriggered *time.Time     `json:"last_triggered"`
	ByAgent       map[string]int `json:"by_agent"`
	BySource      map[string]int `json:"by_source"`

	// HookActivations and AutomatedActivations split TotalCount by who observed
	// the activation: a hook on a developer machine or a consumer's report.
	HookActivations      int            `json:"hook_activations"`
	AutomatedActivations int            `json:"automated_activations"`
	ByConsumer           map[string]int `json:"by_consumer"`
}

// EventFilter narrows activation counts to one consumer and one role. An empty
// field applies no filter. Hook events have neither, so any filter excludes them.
type EventFilter struct {
	Consumer string `query:"consumer" doc:"Count only activations this consumer reported"`
	Role     string `query:"role" doc:"Count only activations reported for this role"`
}

func (f EventFilter) active() bool { return f.Consumer != "" || f.Role != "" }

// sql returns the predicate on skill_events alias se for the two numbered args.
func (f EventFilter) sql(consumerArg, roleArg int) string {
	return fmt.Sprintf(` AND ($%[1]d::text = '' OR se.consumer = $%[1]d::text) AND ($%[2]d::text = '' OR se.role = $%[2]d::text)`, consumerArg, roleArg)
}

// uniqueDevsSQL counts developers from hook events only. A reported event has an
// empty developer hash, and a machine is not a developer.
const uniqueDevsSQL = `COUNT(DISTINCT se.developer_hash) FILTER (WHERE se.event_source <> 'control_plane_report')`

// Store handles Postgres persistence for skill_events.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// CleanupOldEvents deletes events older than retentionDays and returns the
// number of rows removed. Pass 0 to disable (caller should check before calling).
func (s *Store) CleanupOldEvents(ctx context.Context, retentionDays int) (int64, error) {
	// make_interval takes the day count as an integer. Concatenating it into a
	// string instead makes Postgres infer a text parameter, which pgx cannot
	// encode an int into — the purge then failed on every startup.
	const q = `DELETE FROM skill_events WHERE created_at < now() - make_interval(days => $1)`
	tag, err := s.pool.Exec(ctx, q, retentionDays)
	if err != nil {
		return 0, fmt.Errorf("analytics.Store.CleanupOldEvents: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Insert writes a single activation event into skill_events.
func (s *Store) Insert(ctx context.Context, e Event) error {
	// The registration check runs inside the INSERT so ingest stays one round
	// trip. Alias resolution matches the read path.
	const q = `
		INSERT INTO skill_events (skill_name, agent, trigger_type, project_hash, developer_hash, event_source, registered)
		VALUES ($1, $2, $3, $4, $5, $6, EXISTS (
			SELECT 1 FROM skills s
			WHERE s.name = COALESCE((SELECT a.canonical FROM skill_aliases a WHERE a.alias = $1), $1)
		))
	`
	source := e.EventSource
	if source == "" {
		// Events from hook scripts predating the column are tool invocations.
		source = "tool_invocation"
	}
	if _, err := s.pool.Exec(ctx, q,
		e.SkillName, e.Agent, e.TriggerType, e.ProjectHash, e.DeveloperHash, source,
	); err != nil {
		return fmt.Errorf("analytics.Store.Insert: %w", err)
	}
	return nil
}

// OverviewData holds top-level KPI metrics for the analytics overview strip.
type OverviewData struct {
	TotalSkills      int             `json:"total_skills"`
	ActiveSkills     int             `json:"active_skills"`
	TotalActivations int             `json:"total_activations"`
	UnreviewedSkills int             `json:"unreviewed_skills"`
	Security         SecuritySummary `json:"security"`

	// UnregisteredActivations counts events whose skill name was not in the
	// registry when they arrived. They are deliberately excluded from
	// TotalActivations — unvalidated names are not proof of use.
	UnregisteredActivations int `json:"unregistered_activations"`
}

// SecuritySummary aggregates skill security statuses.
// The scanner produces "warn" and "high" status strings; both are counted
// under the Warning field. "critical" maps to Critical. Everything else
// (including "clean", "info", and skills with no scan results) maps to Clean.
type SecuritySummary struct {
	Clean    int `json:"clean"`
	Warning  int `json:"warning"`
	Critical int `json:"critical"`
}

// SkillAnalytics holds per-skill analytics data for the table view.
type SkillAnalytics struct {
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Tags           []string   `json:"tags"`
	Author         string     `json:"author"`
	SpecCompliance string     `json:"spec_compliance"`
	Activations    int        `json:"activations"`
	UniqueDevs     int        `json:"unique_devs"`
	LastTriggered  *time.Time `json:"last_triggered"`
	SecurityStatus string     `json:"security_status"`
	ReviewedAt     *time.Time `json:"reviewed_at"`
	LatestVersion  int        `json:"latest_version"`
	UpdatedAt      time.Time  `json:"updated_at"`

	// Quality is nil when the skill has never been scored. Nil, not a zero
	// value: an unscored skill and a skill that scored zero are different
	// facts, and every surface renders them differently.
	Quality *QualitySummary `json:"quality,omitempty"`
}

// QualitySummary is the compact quality state a list row needs to render a
// badge: enough to draw the meter and to say whether it may be trusted, and
// nothing more. The full report is a separate endpoint.
type QualitySummary struct {
	Version       int       `json:"version"`
	Headline      float64   `json:"headline_score"`
	Verified      bool      `json:"verified"`
	PanelComplete bool      `json:"panel_complete"`
	ScoredAt      time.Time `json:"scored_at"`
	Lift          *float64  `json:"lift,omitempty"`
}

// GetOverview returns aggregate KPI data covering the last `days` days.
func (s *Store) GetOverview(ctx context.Context, days int, filter EventFilter) (*OverviewData, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM skills`).Scan(&total); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetOverview total_skills: %w", err)
	}

	activeQ := `
		SELECT COUNT(DISTINCT COALESCE(a.canonical, se.skill_name))
		FROM skill_events se
		LEFT JOIN skill_aliases a ON a.alias = se.skill_name
		WHERE se.created_at > now() - make_interval(days => $1)
		  AND EXISTS (SELECT 1 FROM skills s WHERE s.name = COALESCE(a.canonical, se.skill_name))
	` + filter.sql(2, 3)
	var active int
	if err := s.pool.QueryRow(ctx, activeQ, days, filter.Consumer, filter.Role).Scan(&active); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetOverview active_skills: %w", err)
	}

	totalEventsQ := `
		SELECT COUNT(*)
		FROM skill_events se
		LEFT JOIN skill_aliases a ON a.alias = se.skill_name
		WHERE se.created_at > now() - make_interval(days => $1)
		  AND EXISTS (SELECT 1 FROM skills s WHERE s.name = COALESCE(a.canonical, se.skill_name))
	` + filter.sql(2, 3)
	var totalActivations int
	if err := s.pool.QueryRow(ctx, totalEventsQ, days, filter.Consumer, filter.Role).Scan(&totalActivations); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetOverview total_activations: %w", err)
	}

	unregisteredQ := `
		SELECT COUNT(*)
		FROM skill_events se
		WHERE se.registered = FALSE
		  AND se.created_at > now() - make_interval(days => $1)
	` + filter.sql(2, 3)
	var unregisteredActivations int
	if err := s.pool.QueryRow(ctx, unregisteredQ, days, filter.Consumer, filter.Role).Scan(&unregisteredActivations); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetOverview unregistered_activations: %w", err)
	}

	// Count skills by scan status of their latest version.
	// Skills with no versions are treated as "clean".
	const secQ = `
		SELECT
			COALESCE(sv.scan_result->>'status', 'clean') AS status,
			COUNT(*) AS cnt
		FROM skills s
		LEFT JOIN skill_versions sv
			ON sv.skill_id = s.id AND sv.version = s.latest_version
		GROUP BY status
	`
	rows, err := s.pool.Query(ctx, secQ)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetOverview security: %w", err)
	}
	defer rows.Close()

	var sec SecuritySummary
	for rows.Next() {
		var status string
		var cnt int
		if err := rows.Scan(&status, &cnt); err != nil {
			return nil, fmt.Errorf("analytics.Store.GetOverview security scan: %w", err)
		}
		switch status {
		case "critical":
			sec.Critical += cnt
		case "warn", "high":
			sec.Warning += cnt
		default:
			// "clean", "info", and anything else counts as clean
			sec.Clean += cnt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetOverview security rows: %w", err)
	}

	var unreviewed int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM skills WHERE reviewed_at IS NULL`).Scan(&unreviewed); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetOverview unreviewed_skills: %w", err)
	}

	return &OverviewData{
		TotalSkills:             total,
		ActiveSkills:            active,
		TotalActivations:        totalActivations,
		UnreviewedSkills:        unreviewed,
		Security:                sec,
		UnregisteredActivations: unregisteredActivations,
	}, nil
}

// GetSkillsAnalytics returns per-skill analytics for all skills over the last
// `days` days, ordered by activation count descending.
// SkillsQuery holds optional list filters/pagination for GetSkillsAnalytics.
type SkillsQuery struct {
	Limit  int
	Offset int
	Sort   string // "activations" (default) | "name" | "updated" | "quality" | "lift"
	Query  string // case-insensitive substring on name/description
	Tag    string // frontmatter tag membership
	// Order is "asc" or "desc"; anything else uses the sort clause's own
	// default direction.
	Order string
	// Scored filters to skills that have ("yes") or have not ("no") been
	// scored. Empty means no filter.
	Scored string
	// Filter counts only matching activations. An active filter also drops the
	// skills with no matching activation, so the table shows what was used.
	Filter EventFilter
}

var skillsSortClauses = map[string]string{
	"activations": "activations DESC, s.name ASC",
	"name":        "s.name ASC",
	"updated":     "s.updated_at DESC, s.name ASC",
	// NULLS LAST is applied in both directions on purpose: an unscored skill
	// is not a zero-scoring one, and letting it sort first under `asc` would
	// present "never measured" as "worst".
	"quality": "q.headline_score DESC NULLS LAST, s.name ASC",
	// NULLS LAST in both directions, for the reason above.
	"lift": "q.lift DESC NULLS LAST, s.name ASC",
}

// skillsSortClausesAsc holds the ascending form of any sort whose ascending
// form is not simply the default. Only the score sorts need one, because
// flipping DESC to ASC must not also flip NULLS LAST to NULLS FIRST.
var skillsSortClausesAsc = map[string]string{
	"quality": "q.headline_score ASC NULLS LAST, s.name ASC",
	"lift":    "q.lift ASC NULLS LAST, s.name ASC",
}

func (s *Store) GetSkillsAnalytics(ctx context.Context, days int, opts SkillsQuery) ([]SkillAnalytics, int, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	orderBy, ok := skillsSortClauses[opts.Sort]
	if !ok {
		orderBy = skillsSortClauses["activations"]
	} else if opts.Order == "asc" {
		if asc, has := skillsSortClausesAsc[opts.Sort]; has {
			orderBy = asc
		}
	}
	args := []any{days}
	next := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	where := ""
	if opts.Query != "" {
		p := next("%" + opts.Query + "%")
		where += fmt.Sprintf(" AND (s.name ILIKE %s OR s.description ILIKE %s)", p, p)
	}
	if opts.Tag != "" {
		where += fmt.Sprintf(" AND %s = ANY(s.tags)", next(opts.Tag))
	}
	switch opts.Scored {
	case "yes":
		where += " AND q.version IS NOT NULL"
	case "no":
		where += " AND q.version IS NULL"
	}
	eventFilter := opts.Filter.sql(len(args)+1, len(args)+2)
	args = append(args, opts.Filter.Consumer, opts.Filter.Role)
	if opts.Filter.active() {
		where += " AND e.resolved_name IS NOT NULL"
	}
	q := `
		SELECT
			s.name,
			s.description,
			COALESCE(e.activation_count, 0)                       AS activations,
			COALESCE(e.unique_devs, 0)                            AS unique_devs,
			e.last_triggered,
			COALESCE(sv.scan_result->>'status', 'clean')          AS security_status,
			s.reviewed_at,
			s.latest_version,
			s.updated_at,
			s.tags                                                AS raw_tags,
			s.author,
			s.spec_compliance,
			q.version, q.headline_score, q.verified, q.panel_complete, q.scored_at, q.lift,
			COUNT(*) OVER() AS total_count
		FROM skills s
		LEFT JOIN (
			SELECT
				COALESCE(a.canonical, se.skill_name) AS resolved_name,
				COUNT(*)                    AS activation_count,
				` + uniqueDevsSQL + ` AS unique_devs,
				MAX(se.created_at)          AS last_triggered
			FROM skill_events se
			LEFT JOIN skill_aliases a ON a.alias = se.skill_name
			WHERE se.created_at > now() - make_interval(days => $1)` + eventFilter + `
			GROUP BY resolved_name
		) e ON e.resolved_name = s.name
		LEFT JOIN skill_versions sv
			ON sv.skill_id = s.id AND sv.version = s.latest_version
		LEFT JOIN LATERAL (
			SELECT version, headline_score, verified, panel_complete, scored_at, lift
			FROM skill_quality
			WHERE skill_id = s.id
			ORDER BY scored_at DESC, id DESC
			LIMIT 1
		) q ON TRUE
		WHERE 1=1` + where + `
		ORDER BY ` + orderBy + `
		LIMIT ` + next(opts.Limit) + ` OFFSET ` + next(opts.Offset)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("analytics.Store.GetSkillsAnalytics query: %w", err)
	}
	defer rows.Close()

	var results []SkillAnalytics
	total := 0
	for rows.Next() {
		var sa SkillAnalytics
		var rowTotal int
		var qVersion *int
		var qHeadline *float64
		var qVerified, qPanelComplete *bool
		var qScoredAt *time.Time
		var qLift *float64
		if err := rows.Scan(
			&sa.Name,
			&sa.Description,
			&sa.Activations,
			&sa.UniqueDevs,
			&sa.LastTriggered,
			&sa.SecurityStatus,
			&sa.ReviewedAt,
			&sa.LatestVersion,
			&sa.UpdatedAt,
			&sa.Tags,
			&sa.Author,
			&sa.SpecCompliance,
			&qVersion,
			&qHeadline,
			&qVerified,
			&qPanelComplete,
			&qScoredAt,
			&qLift,
			&rowTotal,
		); err != nil {
			return nil, 0, fmt.Errorf("analytics.Store.GetSkillsAnalytics scan: %w", err)
		}
		if sa.Tags == nil {
			sa.Tags = []string{}
		}
		if qVersion != nil {
			sa.Quality = &QualitySummary{
				Version:       *qVersion,
				Headline:      *qHeadline,
				Verified:      *qVerified,
				PanelComplete: *qPanelComplete,
				ScoredAt:      *qScoredAt,
				Lift:          qLift,
			}
		}
		total = rowTotal
		results = append(results, sa)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("analytics.Store.GetSkillsAnalytics rows: %w", err)
	}
	if results == nil {
		results = []SkillAnalytics{}
	}
	return results, total, nil
}

// GetAllTags returns the distinct, sorted set of tags across all skills.
func (s *Store) GetAllTags(ctx context.Context) ([]string, error) {
	const q = `
		SELECT DISTINCT unnest(tags) AS tag
		FROM skills
		WHERE tags != '{}'
		ORDER BY tag`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetAllTags query: %w", err)
	}
	defer rows.Close()
	tags := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("analytics.Store.GetAllTags scan: %w", err)
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// GetActivations returns an ActivationSummary for the given skill over the
// last `days` days. Returns a zero-value summary (no error) when no events exist.
func (s *Store) GetActivations(ctx context.Context, skillName string, days int, filter EventFilter) (*ActivationSummary, error) {
	eventFilter := filter.sql(3, 4)
	// Query 1: aggregate counts.
	aggQ := `
		SELECT COUNT(*), ` + uniqueDevsSQL + `, MAX(se.created_at),
		       COUNT(*) FILTER (WHERE se.event_source <> 'control_plane_report')
		FROM skill_events se
		LEFT JOIN skill_aliases a ON a.alias = se.skill_name
		WHERE COALESCE(a.canonical, se.skill_name) = $1
		  AND se.created_at > now() - make_interval(days => $2)
	` + eventFilter
	var totalCount int
	var uniqueDevs int
	var lastTriggered *time.Time
	var hook int
	if err := s.pool.QueryRow(ctx, aggQ, skillName, days, filter.Consumer, filter.Role).Scan(
		&totalCount, &uniqueDevs, &lastTriggered, &hook,
	); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetActivations agg: %w", err)
	}

	// Query 2: per-agent breakdown.
	agentQ := `
		SELECT agent, COUNT(*)
		FROM skill_events se
		LEFT JOIN skill_aliases a ON a.alias = se.skill_name
		WHERE COALESCE(a.canonical, se.skill_name) = $1
		  AND se.created_at > now() - make_interval(days => $2)` + eventFilter + `
		GROUP BY agent
	`
	rows, err := s.pool.Query(ctx, agentQ, skillName, days, filter.Consumer, filter.Role)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetActivations agents: %w", err)
	}
	defer rows.Close()

	byAgent := make(map[string]int)
	for rows.Next() {
		var agent string
		var count int
		if err := rows.Scan(&agent, &count); err != nil {
			return nil, fmt.Errorf("analytics.Store.GetActivations agents scan: %w", err)
		}
		byAgent[agent] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetActivations agents rows: %w", err)
	}

	// Sources are reported separately, never merged: see the comment on
	// Event.EventSource.
	sourceQ := `
		SELECT se.event_source, COUNT(*)
		FROM skill_events se
		LEFT JOIN skill_aliases a ON a.alias = se.skill_name
		WHERE COALESCE(a.canonical, se.skill_name) = $1
		  AND se.created_at > now() - make_interval(days => $2)` + eventFilter + `
		GROUP BY se.event_source
	`
	sourceRows, err := s.pool.Query(ctx, sourceQ, skillName, days, filter.Consumer, filter.Role)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetActivations sources: %w", err)
	}
	defer sourceRows.Close()

	bySource := make(map[string]int)
	for sourceRows.Next() {
		var source string
		var count int
		if err := sourceRows.Scan(&source, &count); err != nil {
			return nil, fmt.Errorf("analytics.Store.GetActivations sources scan: %w", err)
		}
		bySource[source] = count
	}
	if err := sourceRows.Err(); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetActivations sources rows: %w", err)
	}

	consumerQ := `
		SELECT se.consumer, COUNT(*)
		FROM skill_events se
		LEFT JOIN skill_aliases a ON a.alias = se.skill_name
		WHERE COALESCE(a.canonical, se.skill_name) = $1
		  AND se.created_at > now() - make_interval(days => $2)
		  AND se.event_source = 'control_plane_report'
		  AND se.consumer IS NOT NULL` + eventFilter + `
		GROUP BY se.consumer
	`
	consumerRows, err := s.pool.Query(ctx, consumerQ, skillName, days, filter.Consumer, filter.Role)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetActivations consumers: %w", err)
	}
	defer consumerRows.Close()

	byConsumer := make(map[string]int)
	for consumerRows.Next() {
		var consumer string
		var count int
		if err := consumerRows.Scan(&consumer, &count); err != nil {
			return nil, fmt.Errorf("analytics.Store.GetActivations consumers scan: %w", err)
		}
		byConsumer[consumer] = count
	}
	if err := consumerRows.Err(); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetActivations consumers rows: %w", err)
	}

	return &ActivationSummary{
		TotalCount:           totalCount,
		UniqueDevs:           uniqueDevs,
		LastTriggered:        lastTriggered,
		ByAgent:              byAgent,
		BySource:             bySource,
		HookActivations:      hook,
		AutomatedActivations: totalCount - hook,
		ByConsumer:           byConsumer,
	}, nil
}

// AnalyticsConsumersBody lists the values the consumer and role filters accept.
type AnalyticsConsumersBody struct {
	Consumers []string `json:"consumers"`
	Roles     []string `json:"roles"`
}

// GetConsumers returns the distinct consumers and roles in stored events.
func (s *Store) GetConsumers(ctx context.Context) (*AnalyticsConsumersBody, error) {
	consumers, err := s.distinctEventValues(ctx, `SELECT DISTINCT consumer FROM skill_events WHERE consumer IS NOT NULL ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetConsumers consumers: %w", err)
	}
	roles, err := s.distinctEventValues(ctx, `SELECT DISTINCT role FROM skill_events WHERE consumer IS NOT NULL AND role IS NOT NULL ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetConsumers roles: %w", err)
	}
	return &AnalyticsConsumersBody{Consumers: consumers, Roles: roles}, nil
}

func (s *Store) distinctEventValues(ctx context.Context, q string) ([]string, error) {
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	values, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

// UnregisteredSkill represents a skill name found in events but not in the registry.
type UnregisteredSkill struct {
	Name          string     `json:"name"`
	Activations   int        `json:"activations"`
	UniqueDevs    int        `json:"unique_devs"`
	LastTriggered *time.Time `json:"last_triggered"`
	FirstSeen     *time.Time `json:"first_seen"`
}

// GetUnregisteredSkills returns skill names from events that don't exist in the
// skills table or dismissed_skills table, with activation stats.
func (s *Store) GetUnregisteredSkills(ctx context.Context, days int) ([]UnregisteredSkill, error) {
	q := `
		SELECT se.skill_name,
		       COUNT(*)::int                          AS activations,
		       (` + uniqueDevsSQL + `)::int AS unique_devs,
		       MAX(se.created_at)                     AS last_triggered,
		       MIN(se.created_at)                     AS first_seen
		FROM skill_events se
		WHERE se.created_at > now() - make_interval(days => $1)
		  AND NOT EXISTS (SELECT 1 FROM skills s WHERE s.name = se.skill_name)
		  AND NOT EXISTS (SELECT 1 FROM dismissed_skills d WHERE d.name = se.skill_name)
		  AND NOT EXISTS (SELECT 1 FROM skill_aliases a WHERE a.alias = se.skill_name)
		GROUP BY se.skill_name
		ORDER BY activations DESC
	`
	rows, err := s.pool.Query(ctx, q, days)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetUnregisteredSkills query: %w", err)
	}
	defer rows.Close()

	var results []UnregisteredSkill
	for rows.Next() {
		var u UnregisteredSkill
		if err := rows.Scan(&u.Name, &u.Activations, &u.UniqueDevs, &u.LastTriggered, &u.FirstSeen); err != nil {
			return nil, fmt.Errorf("analytics.Store.GetUnregisteredSkills scan: %w", err)
		}
		results = append(results, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetUnregisteredSkills rows: %w", err)
	}
	if results == nil {
		results = []UnregisteredSkill{}
	}
	return results, nil
}

// DismissSkill inserts a name into dismissed_skills. Idempotent.
func (s *Store) DismissSkill(ctx context.Context, name string) error {
	const q = `INSERT INTO dismissed_skills (name) VALUES ($1) ON CONFLICT DO NOTHING`
	if _, err := s.pool.Exec(ctx, q, name); err != nil {
		return fmt.Errorf("analytics.Store.DismissSkill: %w", err)
	}
	return nil
}

// DailyCount holds the activation count for a single day.
type DailyCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// GetTimeSeries returns daily activation counts for the last `days` days.
// Days with zero activations are included (filled with 0).
func (s *Store) GetTimeSeries(ctx context.Context, days int, filter EventFilter) ([]DailyCount, error) {
	q := `
		WITH days AS (
			SELECT generate_series(
				(now() - make_interval(days => $1))::date,
				now()::date,
				'1 day'::interval
			)::date AS day
		)
		SELECT d.day::text, COALESCE(COUNT(se.id), 0)::int
		FROM days d
		LEFT JOIN skill_events se
			ON se.created_at::date = d.day
			AND EXISTS (
				SELECT 1 FROM skills s
				WHERE s.name = COALESCE(
					(SELECT canonical FROM skill_aliases WHERE alias = se.skill_name),
					se.skill_name
				)
			)` + filter.sql(2, 3) + `
		GROUP BY d.day
		ORDER BY d.day
	`
	rows, err := s.pool.Query(ctx, q, days, filter.Consumer, filter.Role)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetTimeSeries query: %w", err)
	}
	defer rows.Close()

	var results []DailyCount
	for rows.Next() {
		var dc DailyCount
		if err := rows.Scan(&dc.Date, &dc.Count); err != nil {
			return nil, fmt.Errorf("analytics.Store.GetTimeSeries scan: %w", err)
		}
		results = append(results, dc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetTimeSeries rows: %w", err)
	}
	if results == nil {
		results = []DailyCount{}
	}
	return results, nil
}

// AgentDailyCount holds per-agent activation counts for a single day.
type AgentDailyCount struct {
	Date   string         `json:"date"`
	Agents map[string]int `json:"-"`
}

// MarshalJSON produces a flat object: {"date":"2026-05-20","claude-code":5,"cursor":2}
func (a AgentDailyCount) MarshalJSON() ([]byte, error) {
	m := make(map[string]interface{}, len(a.Agents)+1)
	m["date"] = a.Date
	for agent, count := range a.Agents {
		m[agent] = count
	}
	return json.Marshal(m)
}

// GetSkillTimeSeries returns daily per-agent activation counts for a specific
// skill over the last `days` days. Days with zero activations are included.
func (s *Store) GetSkillTimeSeries(ctx context.Context, skillName string, days int, filter EventFilter) ([]AgentDailyCount, error) {
	q := `
		WITH days AS (
			SELECT generate_series(
				(now() - make_interval(days => $2))::date,
				now()::date,
				'1 day'::interval
			)::date AS day
		),
		counts AS (
			SELECT se.created_at::date AS day, se.agent, COUNT(*)::int AS cnt
			FROM skill_events se
			LEFT JOIN skill_aliases a ON a.alias = se.skill_name
			WHERE COALESCE(a.canonical, se.skill_name) = $1
			  AND se.created_at > now() - make_interval(days => $2)` + filter.sql(3, 4) + `
			GROUP BY se.created_at::date, se.agent
		)
		SELECT d.day::text, COALESCE(c.agent, ''), COALESCE(c.cnt, 0)
		FROM days d
		LEFT JOIN counts c ON c.day = d.day
		ORDER BY d.day, c.agent
	`
	rows, err := s.pool.Query(ctx, q, skillName, days, filter.Consumer, filter.Role)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetSkillTimeSeries query: %w", err)
	}
	defer rows.Close()

	dayMap := make(map[string]*AgentDailyCount)
	var orderedDates []string

	for rows.Next() {
		var date, agent string
		var count int
		if err := rows.Scan(&date, &agent, &count); err != nil {
			return nil, fmt.Errorf("analytics.Store.GetSkillTimeSeries scan: %w", err)
		}
		entry, exists := dayMap[date]
		if !exists {
			entry = &AgentDailyCount{Date: date, Agents: make(map[string]int)}
			dayMap[date] = entry
			orderedDates = append(orderedDates, date)
		}
		if agent != "" && count > 0 {
			entry.Agents[agent] = count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetSkillTimeSeries rows: %w", err)
	}

	results := make([]AgentDailyCount, 0, len(orderedDates))
	for _, d := range orderedDates {
		results = append(results, *dayMap[d])
	}
	if results == nil {
		results = []AgentDailyCount{}
	}
	return results, nil
}
