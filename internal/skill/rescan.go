package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/scan"
)

// Rescan outcomes, as the API reports them.
const (
	// RescanStored replaced the report and left the gate state alone.
	RescanStored = "stored"
	// RescanWithdrawn withdrew a released version.
	RescanWithdrawn = "withdrawn"
	// RescanRejected rejected a held version on an unappealable finding.
	RescanRejected = "rejected"
	// RescanReleased cleared the last hold reason of a held version.
	RescanReleased = "released"
	// RescanHeld left a held version held.
	RescanHeld = "held"
)

// RescanActor is the actor of a rescan no person asked for directly: an
// admin's rescan of every released version, and a cleared scan reason.
const RescanActor = "system:rescan"

// WithdrawEvent describes a released version a rescan withdrew.
type WithdrawEvent struct {
	Skill    string
	Version  int
	Checksum string
	Reason   string
	// LatestVersion is the version served after the withdrawal, or 0.
	LatestVersion int
}

// SkillScan is one version's stored scan report. NewFindings lists the
// findings that the report the version was released with did not have.
type SkillScan struct {
	Skill       string         `json:"skill"`
	Version     int            `json:"version"`
	GateState   string         `json:"gate_state"`
	GateNote    string         `json:"gate_note,omitempty"`
	RescannedAt *time.Time     `json:"rescanned_at,omitempty"`
	Report      scan.Report    `json:"report"`
	NewFindings []scan.Finding `json:"new_findings"`
	// Outcome is set only on the answer to a rescan.
	Outcome string `json:"outcome,omitempty"`
}

// errNoReport means the version exists and has no stored report.
var errNoReport = errors.New("no scan report")

// ScanView returns the stored report of one version, or of the newest version
// when version is 0. releasedOnly limits the choice to released versions. It
// returns nil, nil when no version matches, and errNoReport with the version
// when that version has no report.
func (s *Store) ScanView(ctx context.Context, name string, version int, releasedOnly bool) (*SkillScan, error) {
	const q = `
		SELECT sv.version, sv.gate_state, sv.gate_note, sv.scan_result, sv.release_scan_result, sv.rescanned_at
		FROM skill_versions sv
		JOIN skills s ON s.id = sv.skill_id
		WHERE s.name = $1 AND ($2 = 0 OR sv.version = $2)
		  AND (NOT $3 OR sv.gate_state = 'released')
		ORDER BY sv.version DESC
		LIMIT 1`
	var view SkillScan
	var current, baseline []byte
	err := s.pool.QueryRow(ctx, q, name, version, releasedOnly).Scan(
		&view.Version, &view.GateState, &view.GateNote, &current, &baseline, &view.RescannedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skill.Store.ScanView: %w", err)
	}
	view.Skill = name
	view.NewFindings = []scan.Finding{}
	if len(current) > 0 {
		if err := json.Unmarshal(current, &view.Report); err != nil {
			return nil, fmt.Errorf("skill.Store.ScanView: unmarshal report of %s v%d: %w", name, view.Version, err)
		}
	}
	if view.Report.Status == "" {
		return &view, errNoReport
	}
	if view.Report.Findings == nil {
		view.Report.Findings = []scan.Finding{}
	}
	if len(baseline) > 0 && (view.GateState == "released" || view.GateState == "withdrawn") {
		var released scan.Report
		if err := json.Unmarshal(baseline, &released); err != nil {
			return nil, fmt.Errorf("skill.Store.ScanView: unmarshal release report of %s v%d: %w", name, view.Version, err)
		}
		view.NewFindings = newFindings(released, view.Report)
	}
	return &view, nil
}

// findingKey identifies a finding across two scans of one archive. The archive
// does not change, so a rule, a file and a line name the same text.
func findingKey(f scan.Finding) string {
	return fmt.Sprintf("%s\x00%s\x00%d", f.Rule, f.File, f.Line)
}

// newFindings returns the findings of now that before did not have.
func newFindings(before, now scan.Report) []scan.Finding {
	seen := make(map[string]bool, len(before.Findings))
	for _, f := range before.Findings {
		seen[findingKey(f)] = true
	}
	out := []scan.Finding{}
	for _, f := range now.Findings {
		if !seen[findingKey(f)] {
			out = append(out, f)
		}
	}
	return out
}

// newBlocking reports whether now has a blocking finding that before did not.
func newBlocking(before, now scan.Report) bool {
	for _, f := range newFindings(before, now) {
		if f.Severity == "critical" || f.Severity == "high" {
			return true
		}
	}
	return false
}

// withdrawalReason names the unappealable findings in one sentence.
func withdrawalReason(d gate.Decision) string {
	var parts []string
	for _, r := range d.Reasons {
		if gate.Unappealable(r) {
			parts = append(parts, fmt.Sprintf("%s (%s) in %s:%d", r.Rule, r.Class, r.File, r.Line))
		}
	}
	return "a rescan found an unappealable finding: " + strings.Join(parts, "; ")
}

// RescanAudit is what a rescan's audit entry records besides the actor.
type RescanAudit struct {
	// RequestedByID names the person who queued a rescan of every released
	// version. Empty for a direct rescan, whose actor is that person.
	RequestedByID string `json:"requested_by_id,omitempty"`
}

// ApplyRescan stores a new report for one version and re-decides what the
// report decides for the version's state, in one transaction with its audit
// entry and its events:
//
//   - A released version with an unappealable finding is withdrawn, and
//     latest_version falls back to the newest released version below it, or 0.
//     Any other finding on a released version changes nothing but the report.
//   - A held version has its scan reason re-decided, and only that reason. An
//     unappealable finding rejects it, because a held version must never be
//     released past one.
//   - A rejected or withdrawn version only stores the report. A new version is
//     the only way past either.
//
// It returns the outcome and, for a withdrawal, its event.
func (s *Store) ApplyRescan(ctx context.Context, name string, version int, report *scan.Report, decision gate.Decision, actor string, extra RescanAudit) (string, *WithdrawEvent, error) {
	newJSON, err := json.Marshal(report)
	if err != nil {
		return "", nil, fmt.Errorf("skill.Store.ApplyRescan: marshal report: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("skill.Store.ApplyRescan: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Both rows are locked: a withdrawal moves the skill's pointer, and a
	// concurrent release or publish of the same skill must wait for it.
	var versionID, skillID, state, checksum string
	var prior []byte
	var holdReasons []string
	var latest int
	err = tx.QueryRow(ctx, `
		SELECT sv.id, sv.skill_id, sv.gate_state, sv.scan_result, sv.checksum, sv.hold_reasons, s.latest_version
		FROM skill_versions sv JOIN skills s ON s.id = sv.skill_id
		WHERE s.name = $1 AND sv.version = $2
		FOR UPDATE`, name, version).Scan(&versionID, &skillID, &state, &prior, &checksum, &holdReasons, &latest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, huma.Error404NotFound(fmt.Sprintf("%s v%d not found", name, version))
	}
	if err != nil {
		return "", nil, fmt.Errorf("skill.Store.ApplyRescan: lock version: %w", err)
	}
	var priorReport scan.Report
	if len(prior) > 0 {
		if err := json.Unmarshal(prior, &priorReport); err != nil {
			return "", nil, fmt.Errorf("skill.Store.ApplyRescan: unmarshal prior report: %w", err)
		}
	}

	// The SET expressions read the row as it was, so the release report is the
	// one the version carried before this rescan.
	const store = `
		UPDATE skill_versions
		SET scan_result = $2, rescanned_at = now(),
		    release_scan_result = CASE WHEN gate_state = 'released'
		                               THEN COALESCE(release_scan_result, scan_result)
		                               ELSE release_scan_result END
		WHERE id = $1`
	if _, err := tx.Exec(ctx, store, versionID, newJSON); err != nil {
		return "", nil, fmt.Errorf("skill.Store.ApplyRescan: store report: %w", err)
	}

	outcome := RescanStored
	action := "skill.version.rescan"
	var ev *WithdrawEvent
	clearedApproval := false
	block := decision.Outcome == gate.Block

	switch {
	case state == "released" && block:
		outcome, action = RescanWithdrawn, "skill.version.withdraw"
		reason := withdrawalReason(decision)
		if _, err := tx.Exec(ctx, `
			UPDATE skill_versions
			SET gate_state = 'withdrawn', gated_by = $2, gated_at = now(), gate_note = $3
			WHERE id = $1`, versionID, actor, reason); err != nil {
			return "", nil, fmt.Errorf("skill.Store.ApplyRescan: withdraw: %w", err)
		}
		if latest == version {
			if err := tx.QueryRow(ctx, `
				UPDATE skills
				SET latest_version = COALESCE((SELECT MAX(version) FROM skill_versions
				                               WHERE skill_id = $1 AND gate_state = 'released'), 0),
				    updated_at = now()
				WHERE id = $1
				RETURNING latest_version`, skillID).Scan(&latest); err != nil {
				return "", nil, fmt.Errorf("skill.Store.ApplyRescan: move latest: %w", err)
			}
			if err := servePointedProse(ctx, tx, name); err != nil {
				return "", nil, fmt.Errorf("skill.Store.ApplyRescan: %w", err)
			}
		}
		ev = &WithdrawEvent{Skill: name, Version: version, Checksum: checksum, Reason: reason, LatestVersion: latest}
		if s.notifier != nil {
			if err := s.notifier.VersionWithdrawn(ctx, tx, *ev); err != nil {
				return "", nil, fmt.Errorf("skill.Store.ApplyRescan: notify: %w", err)
			}
		}

	case state == "needs_review" && block:
		outcome = RescanRejected
		note := withdrawalReason(decision)
		if _, err := tx.Exec(ctx, `
			INSERT INTO version_approvals (version_id, reason, decision, actor_email, note)
			VALUES ($1, $2, 'rejected', $3, $4)
			ON CONFLICT (version_id, reason)
			DO UPDATE SET decision = 'rejected', actor = NULL, actor_email = $3, note = $4, created_at = now()`,
			versionID, gate.ReasonScan, actor, note); err != nil {
			return "", nil, fmt.Errorf("skill.Store.ApplyRescan: reject scan reason: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE skill_versions
			SET gate_state = 'rejected', gated_by = $2, gated_at = now(), gate_note = $3
			WHERE id = $1`, versionID, actor, note); err != nil {
			return "", nil, fmt.Errorf("skill.Store.ApplyRescan: reject: %w", err)
		}

	case state == "needs_review":
		outcome = RescanHeld
		var approved bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM version_approvals
			               WHERE version_id = $1 AND reason = $2 AND decision = 'approved')`,
			versionID, gate.ReasonScan).Scan(&approved); err != nil {
			return "", nil, fmt.Errorf("skill.Store.ApplyRescan: read scan approval: %w", err)
		}
		held := slices.Contains(holdReasons, gate.ReasonScan)
		switch {
		case slices.Contains(decision.HoldReasons, gate.ReasonScan) && !held:
			if _, err := tx.Exec(ctx, `
				UPDATE skill_versions SET hold_reasons = array_append(hold_reasons, $2) WHERE id = $1`,
				versionID, gate.ReasonScan); err != nil {
				return "", nil, fmt.Errorf("skill.Store.ApplyRescan: hold on scan: %w", err)
			}
		case slices.Contains(decision.HoldReasons, gate.ReasonScan) && approved && newBlocking(priorReport, *report):
			// The approval covered the findings it saw. A new blocking
			// finding needs its own decision.
			clearedApproval = true
			if _, err := tx.Exec(ctx, `
				DELETE FROM version_approvals WHERE version_id = $1 AND reason = $2`,
				versionID, gate.ReasonScan); err != nil {
				return "", nil, fmt.Errorf("skill.Store.ApplyRescan: clear scan approval: %w", err)
			}
		case !slices.Contains(decision.HoldReasons, gate.ReasonScan) && held && !approved:
			released, err := s.ApproveReason(ctx, tx, name, version, gate.ReasonScan, nil, RescanActor,
				"a rescan found no finding that holds this version")
			if err != nil {
				return "", nil, fmt.Errorf("skill.Store.ApplyRescan: %w", err)
			}
			if released {
				outcome = RescanReleased
			}
		}
	}

	detail := struct {
		Version         int             `json:"version"`
		StateBefore     string          `json:"state_before"`
		Outcome         string          `json:"outcome"`
		PriorReport     json.RawMessage `json:"prior_report"`
		ClearedApproval bool            `json:"cleared_scan_approval,omitempty"`
		RescanAudit
	}{version, state, outcome, json.RawMessage(prior), clearedApproval, extra}
	if len(detail.PriorReport) == 0 {
		detail.PriorReport = json.RawMessage(`{}`)
	}
	if err := audit.Record(ctx, tx, actor, action, fmt.Sprintf("skill:%s@%d", name, version), detail); err != nil {
		return "", nil, fmt.Errorf("skill.Store.ApplyRescan: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", nil, fmt.Errorf("skill.Store.ApplyRescan: commit: %w", err)
	}
	return outcome, ev, nil
}

// VersionRef names the version behind a skill_versions id. found is false when
// the version is gone.
func (s *Store) VersionRef(ctx context.Context, id string) (name string, version int, state string, found bool, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT s.name, sv.version, sv.gate_state
		FROM skill_versions sv JOIN skills s ON s.id = sv.skill_id
		WHERE sv.id = $1`, id).Scan(&name, &version, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, "", false, nil
	}
	if err != nil {
		return "", 0, "", false, fmt.Errorf("skill.Store.VersionRef: %w", err)
	}
	return name, version, state, true, nil
}

// ReleasedVersionIDs lists the id of every released version.
func (s *Store) ReleasedVersionIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM skill_versions WHERE gate_state = 'released' ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("skill.Store.ReleasedVersionIDs: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("skill.Store.ReleasedVersionIDs: %w", err)
	}
	return ids, nil
}

// Rescan runs a stored version's archive through the same steps as a publish
// and applies the report. It returns the version's report as stored after it.
func (p *Publisher) Rescan(ctx context.Context, name string, version int, actor string, extra RescanAudit) (*SkillScan, error) {
	ver, err := p.store.GetVersion(ctx, name, version)
	if err != nil {
		return nil, fmt.Errorf("rescan: %w", err)
	}
	if ver == nil {
		return nil, huma.Error404NotFound(fmt.Sprintf("%s v%d not found", name, version))
	}
	rc, err := p.storage.Read(ctx, ver.ArchivePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, huma.Error404NotFound(fmt.Sprintf("the archive of %s v%d is missing from storage", name, version))
		}
		return nil, fmt.Errorf("rescan: read archive: %w", err)
	}
	defer rc.Close()
	dir, err := os.MkdirTemp("", "skael-rescan-*")
	if err != nil {
		return nil, fmt.Errorf("rescan: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := Unpack(rc, dir); err != nil {
		return nil, fmt.Errorf("rescan: unpack %s v%d: %w", name, version, err)
	}
	report, err := p.scanDir(ctx, dir)
	if err != nil {
		return nil, err
	}
	decision := gate.Decide(*report, nil, gate.OwnerState{}, gate.Policy{Floor: p.opts.floor()})
	outcome, ev, err := p.store.ApplyRescan(ctx, name, version, report, decision, actor, extra)
	if err != nil {
		return nil, err
	}
	if ev != nil {
		log.Warn().Str("skill", name).Int("version", version).Int("latest_version", ev.LatestVersion).
			Str("reason", ev.Reason).Msg("rescan withdrew a released version")
		p.mailOwners(ctx, *ev)
	}
	view, err := p.store.ScanView(ctx, name, version, false)
	if err != nil {
		return nil, fmt.Errorf("rescan: read back: %w", err)
	}
	view.Outcome = outcome
	return view, nil
}

// mailOwners tells the skill's owners of a withdrawal. A send failure does not
// undo the withdrawal: the audit entry, the webhook and the skill page carry it.
func (p *Publisher) mailOwners(ctx context.Context, ev WithdrawEvent) {
	if p.opts.MailOwners == nil {
		return
	}
	served := "No released version of the skill remains, so clients remove it on their next sync."
	if ev.LatestVersion > 0 {
		served = fmt.Sprintf("Clients install v%d on their next sync.", ev.LatestVersion)
	}
	subject := fmt.Sprintf("Skael withdrew %s v%d", ev.Skill, ev.Version)
	text := fmt.Sprintf("Skael withdrew %s v%d. It is not served again.\n\nReason: %s.\n\n%s\n\nTo release the skill again, publish a new version without the finding.",
		ev.Skill, ev.Version, ev.Reason, served)
	if err := p.opts.MailOwners(context.WithoutCancel(ctx), ev.Skill, subject, text); err != nil {
		log.Warn().Err(err).Str("skill", ev.Skill).Int("version", ev.Version).Msg("rescan: could not email the owners of a withdrawn version")
	}
}
