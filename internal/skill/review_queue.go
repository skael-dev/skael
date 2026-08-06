package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
)

// HeldVersion is one version the publish gate is holding, joined to the name
// of the skill it belongs to. A held version has a number and an archive but
// is not pointed at by skills.latest_version, so it is servable by nothing —
// this listing is the only place it is visible.
type HeldVersion struct {
	SkillName    string          `json:"skill_name"`
	Version      int             `json:"version"`
	GateState    string          `json:"gate_state"`
	GateDecision json.RawMessage `json:"gate_decision,omitempty"`
	// ScanResult is included because the review screen shows the findings
	// beside the decision: the decision says what may clear the hold, and the
	// findings say what caused it. One request, both halves.
	ScanResult  json.RawMessage `json:"scan_result,omitempty"`
	PublishedBy string          `json:"published_by,omitempty"`
	GatedAt     *time.Time      `json:"gated_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	// HoldReasons is every reason the version was held for, in the order
	// recorded at publish time. Outstanding is the subset with no approval
	// row yet — these can differ once one reason clears but the version
	// stays held on another, which is the case the review screen most needs
	// to render honestly.
	HoldReasons []string `json:"hold_reasons"`
	Outstanding []string `json:"outstanding"`
	// RulePattern, Owners and Unowned are hydrated per row from the
	// OwnerResolver after the query runs — the query has no way to resolve
	// ownership itself.
	RulePattern string          `json:"rule_pattern,omitempty"`
	Owners      []gate.OwnerRef `json:"owners"`
	Unowned     bool            `json:"unowned"`
}

// ListHeldVersions returns every version awaiting review, newest first. A
// SQL NULL in gate_decision or scan_result (neither of which is expected in
// practice, since CreateVersion always marshals a decision and a report) is
// scanned into a nil byte slice and wrapped as an empty json.RawMessage
// rather than erroring the whole listing — one row's missing detail must not
// hide every other hold.
func (s *Store) ListHeldVersions(ctx context.Context) ([]HeldVersion, error) {
	const q = `
		SELECT sk.name, v.version, v.gate_state, v.gate_decision, v.scan_result,
		       v.published_by, v.gated_at, v.created_at, v.hold_reasons,
		       COALESCE(outstanding.reasons, '{}')
		FROM skill_versions v
		JOIN skills sk ON sk.id = v.skill_id
		LEFT JOIN LATERAL (
			SELECT array_agg(hr ORDER BY ord) FILTER (WHERE a.id IS NULL) AS reasons
			FROM unnest(v.hold_reasons) WITH ORDINALITY AS u(hr, ord)
			LEFT JOIN version_approvals a
			       ON a.version_id = v.id AND a.reason = u.hr AND a.decision = 'approved'
		) outstanding ON true
		WHERE v.gate_state = 'needs_review'
		ORDER BY v.created_at DESC`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.ListHeldVersions: %w", err)
	}
	defer rows.Close()

	held := []HeldVersion{}
	for rows.Next() {
		var h HeldVersion
		var decision, scanResult []byte
		if err := rows.Scan(&h.SkillName, &h.Version, &h.GateState, &decision, &scanResult,
			&h.PublishedBy, &h.GatedAt, &h.CreatedAt, &h.HoldReasons, &h.Outstanding); err != nil {
			return nil, fmt.Errorf("skill.Store.ListHeldVersions scan: %w", err)
		}
		h.GateDecision = json.RawMessage(decision)
		h.ScanResult = json.RawMessage(scanResult)
		held = append(held, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("skill.Store.ListHeldVersions rows: %w", err)
	}
	return held, nil
}

// reviewQueueBody is the wire shape of GET /api/review/queue.
type reviewQueueBody struct {
	Held  []HeldVersion `json:"held"`
	Total int           `json:"total"`
}

// reviewQueueOutput wraps reviewQueueBody for Huma. reviewQueueBody is
// exported (unlike an inline anonymous struct would need to be) because
// SchemaLinkTransformer rebuilds this response by reflection and only copies
// exported fields — an unexported embedded type silently drops every field.
type reviewQueueOutput struct {
	Body reviewQueueBody
}

// RegisterReviewQueueRoutes wires the cross-skill held-version listing.
// Reading is open to any authenticated member: a hold that only its approver
// can see is a hold nobody discovers. Acting on one stays owner/admin, on
// POST /api/skills/{name}/versions/{version}/review.
//
// owners hydrates RulePattern/Owners/Unowned per row, same as the publish and
// per-version review handlers. A nil owners leaves every row's ownership
// fields zero-valued, matching how a nil RouteOptions.Ownership behaves
// elsewhere — no resolver means ownership contributes nothing.
func RegisterReviewQueueRoutes(api huma.API, store *Store, owners OwnerResolver) {
	huma.Register(api, huma.Operation{
		OperationID: "get-review-queue",
		Method:      http.MethodGet,
		Path:        "/api/review/queue",
		Summary:     "List every version held for review",
	}, func(ctx context.Context, _ *struct{}) (*reviewQueueOutput, error) {
		held, err := store.ListHeldVersions(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("review queue: internal error", err)
		}
		if owners != nil {
			caller := auth.UserFromContext(ctx)
			for i := range held {
				st, err := owners.ResolveForPublish(ctx, held[i].SkillName, caller)
				if err != nil {
					return nil, fmt.Errorf("review queue: resolve ownership for %s: %w", held[i].SkillName, err)
				}
				held[i].RulePattern = st.RulePattern
				held[i].Owners = st.Owners
				held[i].Unowned = st.Unowned
			}
		}
		return &reviewQueueOutput{Body: reviewQueueBody{Held: held, Total: len(held)}}, nil
	})
}
