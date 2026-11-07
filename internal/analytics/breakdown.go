package analytics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/skael-dev/skael/internal/auth"
)

// Member visibility of per-person activation data.
const (
	VisibilityAggregate = "aggregate"
	VisibilityPerson    = "person"
)

// MinGroup is the smallest group a member sees in an aggregate. A smaller one
// could point at one person.
const MinGroup = 5

// BreakdownRow is one model, harness or person.
type BreakdownRow struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Activations int    `json:"activations"`
	People      int    `json:"people"`
}

// Breakdown is who used skills, on which model and harness, over a window.
type Breakdown struct {
	Days             int            `json:"days"`
	Visibility       string         `json:"visibility"`
	IdentityRecorded bool           `json:"identity_recorded"`
	ByModel          []BreakdownRow `json:"by_model"`
	ByHarness        []BreakdownRow `json:"by_harness"`
	// ByPerson is set for an admin, and for a member when visibility is person.
	ByPerson []BreakdownRow `json:"by_person"`
	// Mine is the caller's own activations.
	Mine int `json:"mine"`
	// Hidden counts the groups below MinGroup left out for a member.
	Hidden int `json:"hidden"`
}

// people counts distinct users, or distinct machines for events with no user.
const peopleSQL = `COUNT(DISTINCT COALESCE(se.user_id::text, NULLIF(se.developer_hash, '')))`

func (s *Store) breakdownRows(ctx context.Context, keyExpr, labelExpr, join string, since time.Time, skill string) ([]BreakdownRow, error) {
	q := fmt.Sprintf(`
		SELECT %s AS k, %s AS l, COUNT(*), %s
		FROM skill_events se %s
		WHERE se.created_at >= $1 AND se.event_source <> 'control_plane_report' AND ($2 = '' OR se.skill_name = $2)
		GROUP BY 1, 2 ORDER BY 3 DESC LIMIT 50`, keyExpr, labelExpr, peopleSQL, join)
	rows, err := s.pool.Query(ctx, q, since, skill)
	if err != nil {
		return nil, fmt.Errorf("analytics: breakdown: %w", err)
	}
	defer rows.Close()
	out := []BreakdownRow{}
	for rows.Next() {
		var r BreakdownRow
		if err := rows.Scan(&r.Key, &r.Label, &r.Activations, &r.People); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetBreakdown returns the breakdown the caller may see.
func (s *Store) GetBreakdown(ctx context.Context, days int, skill, userID string, admin bool, visibility string) (*Breakdown, error) {
	since := time.Now().AddDate(0, 0, -days)
	b := &Breakdown{Days: days, Visibility: visibility}
	var err error
	if b.ByModel, err = s.breakdownRows(ctx, `COALESCE(se.model, '')`, `COALESCE(se.model, 'unknown')`, "", since, skill); err != nil {
		return nil, err
	}
	if b.ByHarness, err = s.breakdownRows(ctx, `se.agent || ' ' || COALESCE(se.harness_version, '')`,
		`se.agent || COALESCE(' ' || se.harness_version, '')`, "", since, skill); err != nil {
		return nil, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM skill_events WHERE user_id IS NOT NULL AND created_at >= $1)`, since).Scan(&b.IdentityRecorded); err != nil {
		return nil, err
	}
	if userID != "" {
		if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM skill_events WHERE user_id = $1::uuid AND created_at >= $2 AND ($3 = '' OR skill_name = $3)`,
			userID, since, skill).Scan(&b.Mine); err != nil {
			return nil, err
		}
	}
	if admin || visibility == VisibilityPerson {
		if b.ByPerson, err = s.breakdownRows(ctx, `se.user_id::text`, `COALESCE(NULLIF(u.display_name, ''), u.email)`,
			`JOIN auth_users u ON u.id = se.user_id`, since, skill); err != nil {
			return nil, err
		}
	}
	if !admin {
		b.ByModel, b.Hidden = hideSmall(b.ByModel, b.Hidden)
		b.ByHarness, b.Hidden = hideSmall(b.ByHarness, b.Hidden)
	}
	return b, nil
}

func hideSmall(rows []BreakdownRow, hidden int) ([]BreakdownRow, int) {
	out := rows[:0]
	for _, r := range rows {
		if r.People < MinGroup {
			hidden++
			continue
		}
		out = append(out, r)
	}
	return out, hidden
}

type breakdownInput struct {
	Days  int    `query:"days" default:"30" minimum:"1" maximum:"365"`
	Skill string `query:"skill" maxLength:"128"`
}

type breakdownOutput struct {
	Body *Breakdown
}

func registerBreakdown(api huma.API, store *Store, opts Options) {
	huma.Register(api, huma.Operation{
		OperationID: "analytics-breakdown",
		Method:      http.MethodGet,
		Path:        "/api/analytics/breakdown",
		Summary:     "Activations by model, harness and person, as far as the caller may see",
	}, func(ctx context.Context, in *breakdownInput) (*breakdownOutput, error) {
		u := auth.UserFromContext(ctx)
		if u == nil {
			return nil, huma.Error401Unauthorized("not authenticated")
		}
		days := in.Days
		if days == 0 {
			days = 30
		}
		b, err := store.GetBreakdown(ctx, days, in.Skill, u.ID, u.IsPrivileged(), opts.memberVisibility())
		if err != nil {
			return nil, huma.Error500InternalServerError("analytics breakdown: internal error", err)
		}
		return &breakdownOutput{Body: b}, nil
	})
}
