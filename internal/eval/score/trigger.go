package score

import (
	"errors"
	"path"

	"github.com/skael-dev/skael/internal/eval/agent"
)

// Probe is one trigger measurement. Inferred is true when Fired came from a
// read of SKILL.md rather than an explicit invocation. Unknown probes are
// excluded from every quadrant.
type Probe struct {
	Prompt   string
	Positive bool
	Fired    bool
	Inferred bool
	Unknown  bool
	Reason   string
}

// F1Result is the trigger confusion matrix and F1.
type F1Result struct {
	Precision, Recall, F1 float64
	TP, FP, FN, TN        int
	Unknown               int
	// AnyInferred is true if any counted probe's Fired was an inferred
	// detection rather than an explicit one.
	AnyInferred bool
}

// TriggerF1 computes trigger precision/recall/F1 over ps. Errors if no
// measurable positive probe remains.
func TriggerF1(ps []Probe) (F1Result, error) {
	var r F1Result
	positives := 0
	for _, p := range ps {
		if p.Unknown {
			r.Unknown++
			continue
		}
		if p.Positive {
			positives++
			if p.Fired {
				r.TP++
			} else {
				r.FN++
			}
		} else {
			if p.Fired {
				r.FP++
			} else {
				r.TN++
			}
		}
		if p.Fired && p.Inferred {
			r.AnyInferred = true
		}
	}
	if positives == 0 {
		return F1Result{}, errors.New("score: TriggerF1 needs at least one measurable positive probe")
	}

	if r.TP+r.FP > 0 {
		r.Precision = float64(r.TP) / float64(r.TP+r.FP)
	}
	if r.TP+r.FN > 0 {
		r.Recall = float64(r.TP) / float64(r.TP+r.FN)
	}
	if r.Precision+r.Recall > 0 {
		r.F1 = 2 * r.Precision * r.Recall / (r.Precision + r.Recall)
	}
	return r, nil
}

// DetectFiring reports whether skill fired in events, and whether the
// detection was inferred (a SKILL.md read) rather than explicit (a Skill
// tool call).
func DetectFiring(skill string, caps agent.Caps, events []agent.Event) (fired, inferred bool) {
	explicit := false
	read := false
	for _, e := range events {
		switch e.Type {
		case agent.TypeToolCall:
			if e.Name == "Skill" && eventNamesSkill(e, skill) {
				explicit = true
			}
		case agent.TypeSkillRead:
			if eventNamesSkill(e, skill) {
				read = true
			}
		}
	}
	if explicit {
		return true, false
	}
	if read {
		return true, true
	}
	return false, false
}

// eventNamesSkill reports whether e refers to skill by name or path.
func eventNamesSkill(e agent.Event, skill string) bool {
	if e.Name == skill {
		return true
	}
	for _, p := range e.Paths {
		if path.Base(path.Dir(p)) == skill {
			return true
		}
	}
	return false
}
