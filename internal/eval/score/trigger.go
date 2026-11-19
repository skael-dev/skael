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

// Firing is one skill that fired in a session. Skill is the name the event
// carried: the name the agent invoked, or the folder of the SKILL.md it read.
// Inferred is true when the only evidence is a read of SKILL.md.
type Firing struct {
	Skill    string
	Inferred bool
}

// DetectFiring returns every skill that fired in events, in the order each
// first fired. An explicit invocation (a Skill tool call) wins over a read of
// the same skill. With several skills installed, a session can fire more than
// one, and the routing report needs every name.
func DetectFiring(events []agent.Event) []Firing {
	var out []Firing
	at := map[string]int{}
	add := func(name string, inferred bool) {
		if name == "" {
			return
		}
		if i, ok := at[name]; ok {
			if !inferred {
				out[i].Inferred = false
			}
			return
		}
		at[name] = len(out)
		out = append(out, Firing{Skill: name, Inferred: inferred})
	}
	for _, e := range events {
		switch e.Type {
		case agent.TypeToolCall:
			if e.Name == "Skill" {
				for _, n := range eventSkills(e) {
					add(n, false)
				}
			}
		case agent.TypeSkillRead:
			for _, n := range eventSkills(e) {
				add(n, true)
			}
		}
	}
	return out
}

// Fired reports whether any of names fired, and whether the evidence was only
// inferred. names holds every name the skill can appear under.
func Fired(firings []Firing, names ...string) (fired, inferred bool) {
	explicit, read := false, false
	for _, f := range firings {
		for _, n := range names {
			if f.Skill != n {
				continue
			}
			if f.Inferred {
				read = true
			} else {
				explicit = true
			}
		}
	}
	if explicit {
		return true, false
	}
	return read, read
}

// eventSkills names the skills one event refers to. Only the immediate parent
// directory of a path counts, so a folder named after a skill deeper in some
// other skill's tree is not that skill.
func eventSkills(e agent.Event) []string {
	var out []string
	for _, p := range e.Paths {
		if path.Base(p) == "SKILL.md" {
			out = append(out, path.Base(path.Dir(p)))
		}
	}
	if len(out) == 0 && e.Name != "" && e.Name != "Skill" {
		out = append(out, e.Name)
	}
	return out
}
