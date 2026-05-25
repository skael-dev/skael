package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Event represents a single skill activation telemetry record.
type Event struct {
	SkillName     string `json:"skill_name"`
	Agent         string `json:"agent"`
	TriggerType   string `json:"trigger_type"`
	ProjectHash   string `json:"project_hash"`
	DeveloperHash string `json:"developer_hash"`
}

// ActivationSummary summarises activations for a specific skill over a time window.
type ActivationSummary struct {
	TotalCount    int            `json:"total_count"`
	UniqueDevs    int            `json:"unique_devs"`
	LastTriggered *time.Time     `json:"last_triggered"`
	ByAgent       map[string]int `json:"by_agent"`
}

// Store handles Postgres persistence for skill_events.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Insert writes a single activation event into skill_events.
func (s *Store) Insert(ctx context.Context, e Event) error {
	const q = `
		INSERT INTO skill_events (skill_name, agent, trigger_type, project_hash, developer_hash)
		VALUES ($1, $2, $3, $4, $5)
	`
	if _, err := s.pool.Exec(ctx, q,
		e.SkillName, e.Agent, e.TriggerType, e.ProjectHash, e.DeveloperHash,
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
	Security         SecuritySummary `json:"security"`
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
	Activations    int        `json:"activations"`
	UniqueDevs     int        `json:"unique_devs"`
	LastTriggered  *time.Time `json:"last_triggered"`
	SecurityStatus string     `json:"security_status"`
	ReviewedAt     *time.Time `json:"reviewed_at"`
	LatestVersion  int        `json:"latest_version"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// GetOverview returns aggregate KPI data covering the last `days` days.
func (s *Store) GetOverview(ctx context.Context, days int) (*OverviewData, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM skills`).Scan(&total); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetOverview total_skills: %w", err)
	}

	const activeQ = `
		SELECT COUNT(DISTINCT se.skill_name)
		FROM skill_events se
		JOIN skills s ON s.name = se.skill_name
		WHERE se.created_at > now() - make_interval(days => $1)
	`
	var active int
	if err := s.pool.QueryRow(ctx, activeQ, days).Scan(&active); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetOverview active_skills: %w", err)
	}

	const totalEventsQ = `
		SELECT COUNT(*)
		FROM skill_events se
		JOIN skills s ON s.name = se.skill_name
		WHERE se.created_at > now() - make_interval(days => $1)
	`
	var totalActivations int
	if err := s.pool.QueryRow(ctx, totalEventsQ, days).Scan(&totalActivations); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetOverview total_activations: %w", err)
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

	return &OverviewData{
		TotalSkills:      total,
		ActiveSkills:     active,
		TotalActivations: totalActivations,
		Security:         sec,
	}, nil
}

// GetSkillsAnalytics returns per-skill analytics for all skills over the last
// `days` days, ordered by activation count descending.
func (s *Store) GetSkillsAnalytics(ctx context.Context, days int) ([]SkillAnalytics, error) {
	const q = `
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
			s.frontmatter->'tags'                                 AS raw_tags
		FROM skills s
		LEFT JOIN (
			SELECT
				skill_name,
				COUNT(*)                    AS activation_count,
				COUNT(DISTINCT developer_hash) AS unique_devs,
				MAX(created_at)             AS last_triggered
			FROM skill_events
			WHERE created_at > now() - make_interval(days => $1)
			GROUP BY skill_name
		) e ON e.skill_name = s.name
		LEFT JOIN skill_versions sv
			ON sv.skill_id = s.id AND sv.version = s.latest_version
		ORDER BY activations DESC, s.name ASC
	`
	rows, err := s.pool.Query(ctx, q, days)
	if err != nil {
		return nil, fmt.Errorf("analytics.Store.GetSkillsAnalytics query: %w", err)
	}
	defer rows.Close()

	var results []SkillAnalytics
	for rows.Next() {
		var sa SkillAnalytics
		var rawTags []byte
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
			&rawTags,
		); err != nil {
			return nil, fmt.Errorf("analytics.Store.GetSkillsAnalytics scan: %w", err)
		}
		if rawTags != nil {
			_ = json.Unmarshal(rawTags, &sa.Tags)
		}
		if sa.Tags == nil {
			sa.Tags = []string{}
		}
		results = append(results, sa)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetSkillsAnalytics rows: %w", err)
	}
	if results == nil {
		results = []SkillAnalytics{}
	}
	return results, nil
}

// GetActivations returns an ActivationSummary for the given skill over the
// last `days` days. Returns a zero-value summary (no error) when no events exist.
func (s *Store) GetActivations(ctx context.Context, skillName string, days int) (*ActivationSummary, error) {
	// Query 1: aggregate counts.
	const aggQ = `
		SELECT COUNT(*), COUNT(DISTINCT developer_hash), MAX(created_at)
		FROM skill_events
		WHERE skill_name = $1
		  AND created_at > now() - make_interval(days => $2)
	`
	var totalCount int
	var uniqueDevs int
	var lastTriggered *time.Time
	if err := s.pool.QueryRow(ctx, aggQ, skillName, days).Scan(
		&totalCount, &uniqueDevs, &lastTriggered,
	); err != nil {
		return nil, fmt.Errorf("analytics.Store.GetActivations agg: %w", err)
	}

	// Query 2: per-agent breakdown.
	const agentQ = `
		SELECT agent, COUNT(*)
		FROM skill_events
		WHERE skill_name = $1
		  AND created_at > now() - make_interval(days => $2)
		GROUP BY agent
	`
	rows, err := s.pool.Query(ctx, agentQ, skillName, days)
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

	return &ActivationSummary{
		TotalCount:    totalCount,
		UniqueDevs:    uniqueDevs,
		LastTriggered: lastTriggered,
		ByAgent:       byAgent,
	}, nil
}

// DailyCount holds the activation count for a single day.
type DailyCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// GetTimeSeries returns daily activation counts for the last `days` days.
// Days with zero activations are included (filled with 0).
func (s *Store) GetTimeSeries(ctx context.Context, days int) ([]DailyCount, error) {
	const q = `
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
			AND EXISTS (SELECT 1 FROM skills s WHERE s.name = se.skill_name)
		GROUP BY d.day
		ORDER BY d.day
	`
	rows, err := s.pool.Query(ctx, q, days)
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
