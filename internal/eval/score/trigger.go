package score

import (
	"errors"
	"path"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

// Probe is one trigger measurement: did the skill fire on this prompt, and
// was that prompt one the skill should have fired on (Positive) or a
// distractor it should have ignored?
//
// Inferred records how Fired was decided: true when it came from a read of
// SKILL.md rather than an explicit invocation event. The two are not the
// same measurement — a read cannot distinguish "consulted" from "used" —
// which is why F1Result carries the flag forward rather than silently
// mixing them, mirroring the event_source discipline in the analytics path.
//
// Unknown marks a probe whose session could not be measured (e.g. it
// errored). It is excluded from every quadrant rather than counted as a
// miss, so infrastructure failures don't masquerade as recall failures.
type Probe struct {
	Prompt   string
	Positive bool
	Fired    bool
	Inferred bool
	Unknown  bool
	Reason   string
}

// F1Result is the trigger precision/recall/F1 over a set of Probes, plus the
// confusion-matrix counts and provenance flags a report needs.
type F1Result struct {
	Precision, Recall, F1 float64
	TP, FP, FN, TN        int
	Unknown               int
	// AnyInferred is true if any counted probe's Fired was an inferred
	// detection rather than an explicit one.
	AnyInferred bool
}

// TriggerF1 computes trigger precision/recall/F1 over ps. Unknown probes are
// excluded from every quadrant. TriggerF1 errors if no measurable positive
// probe remains, since recall over zero positives is undefined and a
// measurement with nothing to fire on measures nothing.
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

// DetectFiring reports whether the named skill fired anywhere in events, and
// whether that determination was inferred (from a read of the skill's
// SKILL.md) rather than explicit (from a Skill tool-call event naming it).
//
// A distractor skill's own read or invocation never counts: DetectFiring
// only recognizes events that name skill, matched either directly (Name) or
// via a path whose directory component is the skill name — so reading a
// distractor's SKILL.md, which is correct behavior on a hard negative,
// never counts as skill firing.
func DetectFiring(skill string, caps agent.Caps, events []trajectory.Event) (fired, inferred bool) {
	explicit := false
	read := false
	for _, e := range events {
		switch e.Type {
		case trajectory.TypeToolCall:
			if e.Name == "Skill" && eventNamesSkill(e, skill) {
				explicit = true
			}
		case trajectory.TypeSkillRead:
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

// eventNamesSkill reports whether e refers to skill, either by its Name
// field or by a path whose parent directory is the skill's name (e.g.
// ".claude/skills/<skill>/SKILL.md").
func eventNamesSkill(e trajectory.Event, skill string) bool {
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
