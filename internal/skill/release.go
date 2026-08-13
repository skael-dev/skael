package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/scan"
)

// QualityEvidence is the subset of quality.Record the gate depends on.
// Declared here to avoid a cycle with internal/quality.
type QualityEvidence struct {
	Verified                 bool
	PanelComplete            bool
	Headline                 float64
	CriticalForbidViolations int
	// SuiteDerived: a machine-derived suite grades the skill against its own
	// claims and cannot clear a scan hold.
	SuiteDerived bool
}

// Releaser re-runs the publish decision for a held version once a quality
// record exists.
type Releaser struct{ store *Store }

// NewReleaser builds a Releaser over store.
func NewReleaser(store *Store) *Releaser { return &Releaser{store: store} }

// Reconsider re-decides one held version. A non-held version is a no-op.
func (r *Releaser) Reconsider(
	ctx context.Context,
	e Executor,
	skillName string,
	version int,
	rec QualityEvidence,
	floor float64,
) (gate.Decision, bool, error) {
	ver, err := r.store.GetVersion(ctx, skillName, version)
	if err != nil {
		return gate.Decision{}, false, fmt.Errorf("skill.Releaser.Reconsider: %w", err)
	}
	if ver == nil {
		return gate.Decision{}, false, fmt.Errorf("skill.Releaser.Reconsider: %s v%d not found", skillName, version)
	}
	if ver.GateState != "needs_review" {
		return gate.Decision{}, false, nil
	}

	// A derived suite grades the skill against its own claims — clearing a
	// scan hold on that evidence would let a skill write its own exam.
	if rec.SuiteDerived {
		log.Info().
			Str("skill", skillName).
			Int("version", version).
			Float64("headline", rec.Headline).
			Msg("held version stays held: the score came from a derived suite, which cannot clear a scan hold")
		return gate.Decision{}, false, nil
	}

	var rep scan.Report
	if err := json.Unmarshal(ver.ScanResult, &rep); err != nil {
		return gate.Decision{}, false, fmt.Errorf(
			"skill.Releaser.Reconsider: unmarshal stored scan for %s v%d: %w", skillName, version, err)
	}

	q := &gate.QualityState{
		Verified:                 rec.Verified,
		PanelComplete:            rec.PanelComplete,
		Headline:                 rec.Headline,
		CriticalForbidViolations: rec.CriticalForbidViolations,
	}
	// Empty OwnerState: an evaluation re-decides scan, not ownership.
	d := gate.Decide(rep, q, gate.OwnerState{}, gate.Policy{Floor: floor})

	if d.Outcome != gate.Allow {
		log.Info().
			Str("skill", skillName).
			Int("version", version).
			Str("outcome", string(d.Outcome)).
			Float64("headline", rec.Headline).
			Float64("floor", floor).
			Bool("verified", rec.Verified).
			Bool("panel_complete", rec.PanelComplete).
			Int("critical_forbid_violations", rec.CriticalForbidViolations).
			Msg("held version stays held: the evaluation did not clear it")
		return d, false, nil
	}

	// Clears scan only — clearing ownership here would make the review path
	// decorative.
	note := fmt.Sprintf("verified score %.1f cleared the floor of %.1f", rec.Headline, floor)
	released, err := r.store.ApproveReason(ctx, e, skillName, version,
		gate.ReasonScan, nil, "system:eval", note)
	if err != nil {
		return d, false, fmt.Errorf("skill.Releaser.Reconsider: %w", err)
	}
	if !released {
		log.Info().
			Str("skill", skillName).
			Int("version", version).
			Msg("evaluation cleared the scan hold; the version stays held on its remaining reasons")
		return d, false, nil
	}
	log.Info().
		Str("skill", skillName).
		Int("version", version).
		Float64("headline", rec.Headline).
		Msg("held version released by a verified evaluation")
	return d, true, nil
}
