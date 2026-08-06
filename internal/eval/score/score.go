package score

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/skael-dev/skael/internal/eval/drift"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// Pillars are the four rates Effectiveness composes, each in [0,1].
type Pillars struct {
	TriggerF1   float64
	Reliability float64
	Uplift      float64
	Efficiency  float64
}

// Validate reports whether every pillar is a rate in [0,1]. Effectiveness
// calls it before computing anything; a caller that needs to check
// judge-derived or otherwise unhealthy-sourced pillars before they reach a
// report should call it directly rather than duplicating the range check.
func (p Pillars) Validate() error {
	for name, v := range map[string]float64{
		"TriggerF1": p.TriggerF1, "Reliability": p.Reliability,
		"Uplift": p.Uplift, "Efficiency": p.Efficiency,
	} {
		if v < 0 || v > 1 {
			return fmt.Errorf("score.Pillars.Validate: pillar %s = %v is not a rate in [0,1]", name, v)
		}
	}
	return nil
}

// Exponents weight the geometric mean. They sum to 1 so that a perfect skill
// scores exactly 100 — provided every pillar was actually measured. At the
// Smoke tier, trigger firing is not probed at all and Uplift has no baseline
// to compare against, so those two pillars default to their unmeasured
// neutral values (TriggerF1 = 1.0, Uplift = 0.5) rather than being scored;
// a flawless Smoke-tier skill tops out around 87, not 100.
type Exponents struct {
	Trigger     float64
	Reliability float64
	Uplift      float64
	Efficiency  float64
}

// DefaultExponents is the shipped distribution.
//
// Trigger and Reliability carry 0.35 each because they are the two questions a
// quality score exists to answer: does the skill fire when it should, and does
// it work when it fires. Uplift is 0.20 — it is judge-derived, so it is the
// softest of the four. Efficiency is 0.10, enough to flag bloat and not enough
// to outrank working.
//
// Uplift is deliberately *only* the judge win-rate. An earlier form averaged it
// with a pass-rate delta, but pass rate is already the whole of Reliability:
// that gave pass rate roughly 0.55 effective weight and put two different
// scales on one axis.
var DefaultExponents = Exponents{Trigger: 0.35, Reliability: 0.35, Uplift: 0.20, Efficiency: 0.10}

// Effectiveness is the weighted geometric mean of the four pillars, ×100.
//
// Geometric rather than arithmetic, and that is the whole design: no pillar can
// compensate for a zeroed one. A skill that never fires scores zero however
// reliable it is when someone invokes it by hand.
func Effectiveness(p Pillars, e Exponents) (float64, error) {
	if err := p.Validate(); err != nil {
		return 0, fmt.Errorf("score.Effectiveness: %w", err)
	}
	if sum := e.Trigger + e.Reliability + e.Uplift + e.Efficiency; math.Abs(sum-1) > 1e-9 {
		return 0, fmt.Errorf("score.Effectiveness: exponents sum to %v, not 1", sum)
	}
	// math.Pow(0, x) is 0 for positive x, so a zeroed pillar propagates without
	// a special case — but it is asserted by a test rather than assumed.
	v := math.Pow(p.TriggerF1, e.Trigger) *
		math.Pow(p.Reliability, e.Reliability) *
		math.Pow(p.Uplift, e.Uplift) *
		math.Pow(p.Efficiency, e.Efficiency)
	return 100 * v, nil
}

// EfficiencyFloor bounds how far token bloat can pull the score down. At the
// 0.10 exponent a floored value costs about 11% — a warning, not a verdict,
// because a verbose skill that works still works.
const EfficiencyFloor = 0.3

// Efficiency compares median token spend against the baseline's. Spending less
// than the baseline is not rewarded: the pillar exists to catch bloat, and a
// bonus for terseness would push skills toward saying too little.
func Efficiency(skillMedian, baselineMedian float64) (float64, error) {
	if baselineMedian <= 0 {
		return 0, errors.New("score.Efficiency: baseline median token count is not positive")
	}
	if skillMedian < 0 {
		return 0, errors.New("score.Efficiency: negative skill median token count")
	}
	o := skillMedian / baselineMedian
	if o <= 1 {
		return 1, nil
	}
	return math.Max(EfficiencyFloor, 1/o), nil
}

// UpliftSource records which measurement produced Uplift, so a report never
// presents the fallback as though it were the judge.
type UpliftSource string

const (
	UpliftJudge    UpliftSource = "judge"
	UpliftPassRate UpliftSource = "passrate-fallback"
)

// UpliftFromJudge is the pairwise win rate, ties counted as half.
func UpliftFromJudge(vs []Verdict) (float64, error) {
	if len(vs) == 0 {
		return 0, errors.New("score.UpliftFromJudge: no verdicts")
	}
	var sum float64
	for _, v := range vs {
		switch v.Winner {
		case "skill":
			sum += 1
		case "tie":
			sum += 0.5
		}
	}
	return sum / float64(len(vs)), nil
}

// UpliftFromPassRates is the documented degrade path for a judge whose κ is
// below the floor: a pass-rate delta mapped onto [0,1]. It is a worse
// measurement — it re-uses the signal Reliability already carries — which is
// why it travels with UpliftPassRate on the report rather than replacing the
// judge quietly.
func UpliftFromPassRates(skill, baseline float64) float64 {
	return math.Min(1, math.Max(0, 0.5+(skill-baseline)/2))
}

// Member identifies one panel entry. score defines its own rather than
// importing the runner's: scoring must be usable against recorded results with
// no orchestration in the picture.
type Member struct {
	Agent string
	Model string
	Class spec.ModelTier
}

// PanelEntry is one member's complete result.
type PanelEntry struct {
	Member        Member
	Pillars       Pillars
	Effectiveness float64
	Drift         drift.Agg
	// Healthy is false when the member's adapter failed its probe. Such a member
	// contributes nothing rather than a zero.
	Healthy bool
	Detail  string
}

// Matrix is every member's measured result. Deliberately not called Panel:
// runner.Panel is the planned list of members, this is what they produced, and
// one name for both across adjacent packages is how a boundary defect starts.
type Matrix struct{ Entries []PanelEntry }

// Headline is the minimum Effectiveness across healthy members.
//
// Minimum rather than mean: the claim a published score makes is "this works",
// and it only works if it works on the weakest model someone will run it on.
// A member whose adapter failed is excluded rather than scored zero — otherwise
// an expired token becomes a publish block, which is infrastructure flakiness
// presented as a quality verdict.
func (m Matrix) Headline() (float64, error) {
	min, found := math.Inf(1), false
	var details []string
	for _, e := range m.Entries {
		if !e.Healthy {
			details = append(details, fmt.Sprintf("%s/%s: %s", e.Member.Agent, e.Member.Model, e.Detail))
			continue
		}
		found = true
		if e.Effectiveness < min {
			min = e.Effectiveness
		}
	}
	if !found {
		return 0, fmt.Errorf("score.Headline: no panel member produced a result (%s)", strings.Join(details, "; "))
	}
	return min, nil
}

// Mean is the arithmetic mean of Effectiveness across healthy members. Unlike
// Headline it is not the gating statistic — it is report sugar for "how did
// the panel do on average", which Headline's min-gate deliberately does not
// answer. As with Headline, an unhealthy member is excluded rather than
// scored zero, and a panel with no healthy member is an error.
func (m Matrix) Mean() (float64, error) {
	var sum float64
	var details []string
	n := 0
	for _, e := range m.Entries {
		if !e.Healthy {
			details = append(details, fmt.Sprintf("%s/%s: %s", e.Member.Agent, e.Member.Model, e.Detail))
			continue
		}
		sum += e.Effectiveness
		n++
	}
	if n == 0 {
		return 0, fmt.Errorf("score.Mean: no panel member produced a result (%s)", strings.Join(details, "; "))
	}
	return sum / float64(n), nil
}

// ByClass returns the single healthy entry in Entries whose Member.Class
// matches c, and whether exactly one was found.
//
// ParsePanel can produce several members of one class on a multi-agent,
// multi-model panel — it only rejects a panel with zero of a class, not more
// than one. ByClass reports ok == false for zero matches, more than one, and
// an unhealthy match: with several members of one capability tier, "the
// strong member" and "the floor member" are not defined, so a robustness gap
// computed by comparing them is not defined either; an unhealthy member was
// never measured at all, so returning it would let a caller read its zero
// Drift as a real (adherence-zero) result rather than an absent one — the
// same distinction Headline draws when no member is healthy. Returning
// whichever member happened to be first in Entries would produce a number
// indistinguishable from a real one — an absent measurement must be reported
// as absent, not silently guessed at. A caller that wants a specific member
// out of a duplicated class should iterate m.Entries directly and say which
// member it means, rather than asking ByClass to disambiguate for it.
func (m Matrix) ByClass(c spec.ModelTier) (PanelEntry, bool) {
	var found PanelEntry
	count := 0
	for _, e := range m.Entries {
		if e.Member.Class != c || !e.Healthy {
			continue
		}
		found = e
		count++
	}
	if count != 1 {
		return PanelEntry{}, false
	}
	return found, true
}
