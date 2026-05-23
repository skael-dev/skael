package analytics

import (
	"context"
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
