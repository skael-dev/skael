package skill

import (
	"context"
	"fmt"
)

// OutstandingReasons returns the hold reasons on a version that have no
// approval row yet, in the order they were recorded.
func (s *Store) OutstandingReasons(ctx context.Context, name string, version int) ([]string, error) {
	const q = `
		SELECT COALESCE(array_agg(hr ORDER BY ord)
		                FILTER (WHERE a.id IS NULL), '{}')
		FROM skill_versions v
		JOIN skills s ON s.id = v.skill_id
		CROSS JOIN LATERAL unnest(v.hold_reasons) WITH ORDINALITY AS u(hr, ord)
		LEFT JOIN version_approvals a
		       ON a.version_id = v.id AND a.reason = u.hr AND a.decision = 'approved'
		WHERE s.name = $1 AND v.version = $2`

	var out []string
	if err := s.pool.QueryRow(ctx, q, name, version).Scan(&out); err != nil {
		return nil, fmt.Errorf("skill.Store.OutstandingReasons: %w", err)
	}
	return out, nil
}

// ApproveReason records an approval for one hold reason and releases the
// version if that was the last one outstanding. It reports whether the
// version was released.
//
// actorID is nil for an automated release — the verified-evaluation path —
// so "released by a score" and "released by a person" stay distinguishable
// forever. actorEmail is always written: it is an audit snapshot that
// survives the user being deleted.
//
// It writes through e so a caller can compose the release with whatever
// justified it in one transaction. Report ingestion depends on that.
func (s *Store) ApproveReason(
	ctx context.Context,
	e Executor,
	name string,
	version int,
	reason string,
	actorID *string,
	actorEmail, note string,
) (bool, error) {
	const ins = `
		INSERT INTO version_approvals (version_id, reason, decision, actor, actor_email, note)
		SELECT v.id, $3, 'approved', $4, $5, $6
		FROM skill_versions v
		JOIN skills s ON s.id = v.skill_id
		WHERE s.name = $1 AND v.version = $2
		ON CONFLICT (version_id, reason) DO NOTHING`
	if _, err := e.Exec(ctx, ins, name, version, reason, actorID, actorEmail, note); err != nil {
		return false, fmt.Errorf("skill.Store.ApproveReason: %w", err)
	}

	// Re-read outstanding reasons through e, not the pool: inside a
	// transaction the pool would not see the insert above.
	const outstanding = `
		SELECT count(*)
		FROM skill_versions v
		JOIN skills s ON s.id = v.skill_id
		CROSS JOIN LATERAL unnest(v.hold_reasons) AS u(hr)
		LEFT JOIN version_approvals a
		       ON a.version_id = v.id AND a.reason = u.hr AND a.decision = 'approved'
		WHERE s.name = $1 AND v.version = $2 AND a.id IS NULL`
	var remaining int
	if err := e.QueryRow(ctx, outstanding, name, version).Scan(&remaining); err != nil {
		return false, fmt.Errorf("skill.Store.ApproveReason count outstanding: %w", err)
	}
	if remaining > 0 {
		return false, nil
	}

	if err := s.ReleaseVersion(ctx, e, name, version, actorEmail, note); err != nil {
		return false, fmt.Errorf("skill.Store.ApproveReason: %w", err)
	}
	return true, nil
}

// RejectReason records a rejection for one reason and marks the version
// terminal. Rejecting any single reason rejects the version: there is no
// state in which a version is partly refused.
func (s *Store) RejectReason(
	ctx context.Context,
	name string,
	version int,
	reason, actorEmail, note string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("skill.Store.RejectReason begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	const ins = `
		INSERT INTO version_approvals (version_id, reason, decision, actor_email, note)
		SELECT v.id, $3, 'rejected', $4, $5
		FROM skill_versions v
		JOIN skills s ON s.id = v.skill_id
		WHERE s.name = $1 AND v.version = $2
		ON CONFLICT (version_id, reason) DO NOTHING`
	if _, err := tx.Exec(ctx, ins, name, version, reason, actorEmail, note); err != nil {
		return fmt.Errorf("skill.Store.RejectReason: %w", err)
	}

	const rej = `
		UPDATE skill_versions v
		SET gate_state = 'rejected', gated_by = $3, gated_at = now(), gate_note = $4
		FROM skills s
		WHERE v.skill_id = s.id AND s.name = $1 AND v.version = $2
		  AND v.gate_state = 'needs_review'`
	tag, err := tx.Exec(ctx, rej, name, version, actorEmail, note)
	if err != nil {
		return fmt.Errorf("skill.Store.RejectReason update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("skill.Store.RejectReason: %s v%d is not awaiting review", name, version)
	}
	return tx.Commit(ctx)
}
