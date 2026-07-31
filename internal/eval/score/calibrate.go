package score

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math"

	"gopkg.in/yaml.v3"
)

// KappaFloor is the minimum Cohen's κ against the calibration set for the
// judge to be trusted with Uplift. Below it, the judge disagrees with a human
// often enough that its verdicts are not evidence, and Uplift falls back to
// the deterministic pass-rate delta instead.
const KappaFloor = 0.6

//go:embed calibration/items.yaml
var calibrationYAML []byte

// CalItem is one labelled calibration pair: a task prompt, a transcript from
// a session with the skill available, a transcript from a baseline session
// without it, and a human verdict.
type CalItem struct {
	ID       string `yaml:"id"`
	TaskID   string `yaml:"task_id"`
	Prompt   string `yaml:"prompt"`
	Skill    string `yaml:"skill"`
	Baseline string `yaml:"baseline"`
	Label    string `yaml:"label"`
	Note     string `yaml:"note"`
}

// CalSet is the calibration file: its items plus who labelled them and when.
// Provenance travels with the data because a κ computed against author
// labels is a weaker claim than one against independent labels.
type CalSet struct {
	LabeledBy string    `yaml:"labeled_by"`
	LabeledAt string    `yaml:"labeled_at"`
	Items     []CalItem `yaml:"items"`
}

// Calibration reads and parses the embedded calibration set.
// KnownFields(true) makes an unrecognized YAML key a parse error rather than
// a silently dropped one — that field would otherwise disappear from every
// item without anyone noticing.
func Calibration() (*CalSet, error) {
	dec := yaml.NewDecoder(bytes.NewReader(calibrationYAML))
	dec.KnownFields(true)
	var set CalSet
	if err := dec.Decode(&set); err != nil {
		return nil, fmt.Errorf("score.Calibration: %w", err)
	}
	return &set, nil
}

// CalDisagreement records one item where the judge's verdict did not match
// the human label. A κ with no examples attached is a number nobody can act
// on.
type CalDisagreement struct {
	ID       string
	Label    string
	Verdict  string
	Evidence []string
}

// CalResult is the outcome of running a judge over a calibration set.
type CalResult struct {
	Kappa         float64
	Agreement     float64
	N             int
	Disagreements []CalDisagreement
	LabeledBy     string
}

// JudgeTrusted reports whether this calibration result clears the bar to
// trust the judge with Uplift. An unrun calibration (N == 0) is not trusted:
// defaulting to trust would make the whole gate opt-in, which is exactly how
// it gets forgotten.
func (r CalResult) JudgeTrusted() bool {
	return r.N > 0 && r.Kappa >= KappaFloor
}

// Kappa is Cohen's κ between two raters over the same items.
//
// κ rather than raw agreement because two raters who both answer "skill" most
// of the time agree most of the time by construction. κ subtracts that
// expected agreement, so it measures what the judge adds over guessing the
// base rate — which is the only question worth asking of a judge that sets
// 20% of a score.
//
//	κ = (po − pe) / (1 − pe)
func Kappa(labels, verdicts []string) (float64, error) {
	if len(labels) == 0 {
		return 0, errors.New("score.Kappa: no items")
	}
	if len(labels) != len(verdicts) {
		return 0, fmt.Errorf("score.Kappa: %d labels against %d verdicts", len(labels), len(verdicts))
	}

	n := float64(len(labels))
	agree := 0.0
	labelCount, verdictCount := map[string]float64{}, map[string]float64{}
	for i := range labels {
		if labels[i] == verdicts[i] {
			agree++
		}
		labelCount[labels[i]]++
		verdictCount[verdicts[i]]++
	}

	po := agree / n
	pe := 0.0
	for cat, c := range labelCount {
		pe += (c / n) * (verdictCount[cat] / n)
	}
	if math.Abs(1-pe) < 1e-12 {
		// Both raters used one category for everything. κ is undefined, and
		// returning 0 would read as "the judge is worthless" when the truth is
		// "this set cannot distinguish anything".
		return 0, errors.New("score.Kappa: expected agreement is 1; the set has no variation to measure")
	}
	return (po - pe) / (1 - pe), nil
}

// Calibrate runs j.Pairwise over every item in set and reports the judge's
// agreement with the human labels, including every disagreement so a low κ
// is actionable rather than just alarming.
func Calibrate(ctx context.Context, j *Judge, set *CalSet) (CalResult, error) {
	labels := make([]string, 0, len(set.Items))
	verdicts := make([]string, 0, len(set.Items))
	var disagreements []CalDisagreement
	agree := 0

	for _, it := range set.Items {
		pair := Pair{
			TaskID:   it.TaskID,
			Prompt:   it.Prompt,
			Skill:    Sample{Label: "skill", Transcript: it.Skill},
			Baseline: Sample{Label: "baseline", Transcript: it.Baseline},
		}
		v, err := j.Pairwise(ctx, pair)
		if err != nil {
			return CalResult{}, fmt.Errorf("score.Calibrate: item %s: %w", it.ID, err)
		}

		labels = append(labels, it.Label)
		verdicts = append(verdicts, v.Winner)

		if v.Winner == it.Label {
			agree++
		} else {
			disagreements = append(disagreements, CalDisagreement{
				ID:       it.ID,
				Label:    it.Label,
				Verdict:  v.Winner,
				Evidence: v.Evidence,
			})
		}
	}

	n := len(set.Items)
	kappa, err := Kappa(labels, verdicts)
	if err != nil {
		return CalResult{}, fmt.Errorf("score.Calibrate: %w", err)
	}

	return CalResult{
		Kappa:         kappa,
		Agreement:     float64(agree) / float64(n),
		N:             n,
		Disagreements: disagreements,
		LabeledBy:     set.LabeledBy,
	}, nil
}
