package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/scan"
)

// QualityEvidence is the subset of quality.Record that the gate decision
// depends on. It is declared here rather than taking a quality.Record
// directly because internal/quality imports internal/skill (for its route
// wiring, and transitively through internal/eval/report), so importing it
// back would be a cycle. The caller — which already has both — maps one onto
// the other; see internal/evalqueue.
type QualityEvidence struct {
	Verified                 bool
	PanelComplete            bool
	Headline                 float64
	CriticalForbidViolations int
}

// Releaser re-runs the publish decision for a held version once a quality
// record exists for it. Without this path nothing clears a hold by itself and
// the gate is permanent.
type Releaser struct{ store *Store }

// NewReleaser builds a Releaser over store.
func NewReleaser(store *Store) *Releaser { return &Releaser{store: store} }

// Reconsider re-decides one version. It returns the decision and whether the
// version was released. A version that is not held is a no-op: most evals run
// against versions that published cleanly, and re-deciding them would be a
// redundant write at best and a regression at worst.
//
// e is the executor to write through, so the caller can compose the release
// with the quality upsert that justifies it in one transaction.
//
// It calls gate.Decide directly rather than DecidePublish: DecidePublish
// exists to encode "no measurement exists yet" for the create-a-version
// routes, and passes a nil *QualityState. This path has a real measurement,
// which is the whole point of it. The rules themselves stay in gate.Decide,
// so there is still only one definition of them.
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

	var rep scan.Report
	if err := json.Unmarshal(ver.ScanResult, &rep); err != nil {
		// The stored scan is the only record of why this version was held.
		// Failing to read it must not silently release the version.
		return gate.Decision{}, false, fmt.Errorf(
			"skill.Releaser.Reconsider: unmarshal stored scan for %s v%d: %w", skillName, version, err)
	}

	q := &gate.QualityState{
		Verified:                 rec.Verified,
		PanelComplete:            rec.PanelComplete,
		Headline:                 rec.Headline,
		CriticalForbidViolations: rec.CriticalForbidViolations,
	}
	// AdminOverride is false: this path is an automated re-decision on
	// evidence, and no human is asking for anything. OwnerState is zero:
	// Reconsider only re-evaluates the scan/quality half of the gate. Task 9
	// makes that deliberate rather than incidental.
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

	note := fmt.Sprintf("verified score %.1f cleared the floor of %.1f", rec.Headline, floor)
	if err := r.store.ReleaseVersion(ctx, e, skillName, version, "evaluation", note); err != nil {
		return d, false, fmt.Errorf("skill.Releaser.Reconsider: %w", err)
	}
	log.Info().
		Str("skill", skillName).
		Int("version", version).
		Float64("headline", rec.Headline).
		Msg("held version released by a verified evaluation")
	return d, true, nil
}
