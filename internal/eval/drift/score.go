package drift

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/skael-dev/skael/internal/eval/spec"
)

// Weights distribute Adherence across its components. They sum to 1: Adherence
// is 100 × Σ wᵢcᵢ, so a set that does not sum to 1 rescales every score and
// every grade boundary with it.
type Weights struct {
	StepCoverage float64
	Order        float64
	Violation    float64
	Checkpoint   float64
	Semantic     float64
	Focus        float64
}

// DefaultWeights is the shipped distribution. Coverage and violations carry the
// most because they are the two things a contract exists to state: what must
// happen, and what must not.
var DefaultWeights = Weights{
	StepCoverage: 0.25,
	Order:        0.15,
	Violation:    0.25,
	Checkpoint:   0.15,
	Semantic:     0.15,
	Focus:        0.05,
}

// Sum is exported so a caller supplying custom weights can check them.
func (w Weights) Sum() float64 {
	return w.StepCoverage + w.Order + w.Violation + w.Checkpoint + w.Semantic + w.Focus
}

// SeverityWeight is the penalty each severity contributes to the violation
// exponent. Critical is deliberately far from major: a skill that exfiltrates
// once should not be able to average that away against a hundred correct steps.
var SeverityWeight = map[spec.Severity]float64{
	spec.SeverityCritical: 3,
	spec.SeverityMajor:    1,
	spec.SeverityMinor:    0.3,
}

// Components are the six normalized rates, each in [0,1].
type Components struct {
	StepCoverage float64
	Order        float64
	Violation    float64
	Checkpoint   float64
	Semantic     float64
	Focus        float64
}

// Result is one run's drift. Grade is deliberately left unset here: grading is
// a property of an aggregate across runs, not of a single run, so callers set
// it from Grade(agg.Mean, agg.Worst) after aggregating.
type Result struct {
	Components Components `json:"components"`
	Adherence  float64    `json:"adherence"`
	// Drift is 100-Adherence, kept as a stored field for in-process callers
	// that want it computed once alongside Adherence, but never serialized:
	// a report is one fact (Adherence) with two encodings otherwise, and a
	// hand-edited or third-party report.json could set them inconsistently
	// with no cross-field validation to catch it.
	Drift float64 `json:"-"`
	// Grade is not set by Score. Set it from Grade(agg.Mean, agg.Worst) after
	// aggregating this run with its siblings.
	Grade string `json:"grade,omitempty"`
	// Unevaluable and UnevaluableDetail mirror Observation's fields of the same
	// name: checks that could not be performed at all, surfaced here rather
	// than folded into any of Components — a missed violation must not look
	// like a clean run.
	Unevaluable       int      `json:"unevaluable,omitempty"`
	UnevaluableDetail []string `json:"unevaluable_detail,omitempty"`
}

// Score turns an Observation plus a judge-scored semantic rate into Adherence.
func Score(o *Observation, semantic float64, w Weights) (Result, error) {
	if o == nil {
		return Result{}, errors.New("drift.Score: no observation")
	}
	if semantic < 0 || semantic > 1 {
		return Result{}, fmt.Errorf("drift.Score: semantic component %v is not a rate in [0,1]", semantic)
	}
	if math.Abs(w.Sum()-1) > 1e-9 {
		return Result{}, fmt.Errorf("drift.Score: weights sum to %v, not 1", w.Sum())
	}

	c := Components{Semantic: semantic}

	var required, covered int
	for _, s := range o.Steps {
		if !s.Required {
			continue
		}
		required++
		if s.Matched {
			covered++
		}
	}
	// A contract with no required steps makes no claim about steps, so coverage,
	// order, and checkpoints are vacuously satisfied rather than zero. Scoring
	// them as zero would penalise an entirely-semantic specification, which is a
	// legitimate shape for a skill whose value is judgement rather than mechanics.
	c.StepCoverage = 1
	if required > 0 {
		c.StepCoverage = float64(covered) / float64(required)
	}

	c.Order = orderScore(o.Steps)

	var exponent float64
	for _, v := range o.Violations {
		exponent += SeverityWeight[v.Severity] * float64(v.Hits)
	}
	c.Violation = math.Exp(-exponent)

	c.Checkpoint = 1
	if len(o.Checkpoints) > 0 {
		ran := 0
		for _, ok := range o.Checkpoints {
			if ok {
				ran++
			}
		}
		c.Checkpoint = float64(ran) / float64(len(o.Checkpoints))
	}

	c.Focus = 1
	if o.Total > 0 {
		c.Focus = math.Max(0, 1-float64(o.OffContract)/float64(o.Total))
	}

	adherence := 100 * (w.StepCoverage*c.StepCoverage +
		w.Order*c.Order +
		w.Violation*c.Violation +
		w.Checkpoint*c.Checkpoint +
		w.Semantic*c.Semantic +
		w.Focus*c.Focus)

	return Result{
		Components:        c,
		Adherence:         adherence,
		Drift:             100 - adherence,
		Unevaluable:       o.Unevaluable,
		UnevaluableDetail: o.UnevaluableDetail,
	}, nil
}

// orderScore is 1 minus the normalized count of inversions among the matched,
// required steps: how far the observed order is from the contract's order,
// scaled by the worst possible disagreement for that many steps.
//
// Kendall tau rather than "how many steps were in the right position" because
// one early step displacing everything after it is one mistake, not n.
func orderScore(steps []StepObs) float64 {
	var seqs []int
	for _, s := range steps {
		if s.Required && s.Matched {
			seqs = append(seqs, s.Seq)
		}
	}
	n := len(seqs)
	if n < 2 {
		return 1
	}
	// steps arrive in contract order, so an inversion is a pair whose observed
	// sequences run backwards.
	inversions := 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if seqs[i] > seqs[j] {
				inversions++
			}
		}
	}
	max := n * (n - 1) / 2
	return 1 - float64(inversions)/float64(max)
}

// Agg summarises one member's runs. Worst and Sigma are reported alongside Mean
// because they are different failure modes: a skill that works four runs in five
// is not the same as one that half-works every run, and a mean cannot tell them
// apart. Sigma is trajectory instability — same input, divergent behaviour.
//
// Sigma is the population standard deviation over the runs actually performed
// (divide by N, not N-1): it describes the spread of this member's own
// observed runs, not an estimate of some wider population those runs were
// drawn from, so Bessel's correction does not apply. This also matters at
// n=2 (the Full tier's budget): a sample stddev from two points is unstable
// and inflated relative to the spread anyone actually observed, which would
// overstate instability beyond what the evidence supports.
type Agg struct {
	Mean  float64
	Worst float64
	Sigma float64
	N     int
}

// Aggregate summarises runs. Zero runs is an error, not a zero: an absent
// measurement must not be indistinguishable from total failure.
func Aggregate(rs []Result) (Agg, error) {
	if len(rs) == 0 {
		return Agg{}, errors.New("drift.Aggregate: no runs")
	}
	vals := make([]float64, len(rs))
	sum := 0.0
	for i, r := range rs {
		vals[i] = r.Adherence
		sum += r.Adherence
	}
	sort.Float64s(vals)
	mean := sum / float64(len(rs))

	// Population standard deviation: these runs are the entire measured
	// behaviour for this member at this budget, not a sample drawn from it.
	// Deliberately ÷N, not ÷(N-1) — do not "fix" this to Bessel's correction.
	sigma := 0.0
	if len(rs) > 1 {
		var ss float64
		for _, v := range vals {
			ss += (v - mean) * (v - mean)
		}
		sigma = math.Sqrt(ss / float64(len(rs)))
	}
	return Agg{Mean: mean, Worst: vals[0], Sigma: sigma, N: len(rs)}, nil
}

// Grade is report sugar over mean and worst-run adherence. Thresholds are the
// shipped defaults; they are configuration, not a claim about skills in general.
func Grade(mean, worst float64) string {
	switch {
	case mean >= 90 && worst >= 80:
		return "A"
	case mean >= 75 && worst >= 65:
		return "B"
	case mean >= 60:
		return "C"
	default:
		return "D"
	}
}

// RobustnessGap is the strong member's adherence minus the floor member's.
//
// A positive gap means the skill leans on model capability rather than on
// explicit instruction — the strong model inferred what the text did not say.
// It is the primary input to repair, which is why the sign convention is fixed
// here rather than at each call site.
//
// Both aggregates must carry at least one run: an Agg with N==0 is a zero
// value, not a measurement of zero adherence, and a gap computed against it
// would fabricate a maximal (or minimal) gap out of an absent measurement —
// mirroring Aggregate's refusal of zero runs.
func RobustnessGap(strong, floor Agg) (float64, error) {
	if strong.N == 0 || floor.N == 0 {
		return 0, errors.New("drift.RobustnessGap: both members need at least one drift run")
	}
	return strong.Mean - floor.Mean, nil
}
