// Package drift measures how closely an agent's actual trajectory followed the
// behavioural contract compiled from a skill's specification. It is the half of
// the quality story a pass/fail verifier cannot see: a skill can produce the
// right file having ignored every instruction that was supposed to get it there,
// and that skill will fail on the next task.
package drift

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

// StepObs is one contract step's fate in one trajectory.
type StepObs struct {
	ID       string
	Seq      int
	Matched  bool
	Required bool
	// OrderOK is whether the matching event fell where the step's Order allows.
	// A step can be matched and out of order: coverage counts it, OrderScore
	// penalises it, and conflating the two loses one of the two signals.
	OrderOK bool
}

// Violation is one forbid rule's hits.
type Violation struct {
	ID       string
	Severity spec.Severity
	Hits     int
	// Evidence quotes what triggered it. A violation nobody can check is a
	// number nobody will act on.
	Evidence []string
}

// Observation is the deterministic result of matching one trajectory against
// one contract.
type Observation struct {
	Steps       []StepObs
	Violations  []Violation
	Checkpoints map[string]bool
	// OffContract is contractable events matching no step and no forbid rule.
	OffContract int
	// Total is contractable events. Opaque events are in neither.
	Total int
	// Unevaluable counts checks that could not be performed — an absolute path
	// where a workspace-relative one was expected, a candidate MatchPath
	// refuses. These are surfaced rather than folded into "no violation":
	// a missed violation looks like a clean run, which is the failure direction
	// that never gets investigated.
	Unevaluable       int
	UnevaluableDetail []string
	// Attempted counts every (rule × event) check Observe tried, whether it
	// succeeded or was unevaluable. It exists to give Unevaluable a
	// denominator: a handful of unevaluable checks among thousands is noise,
	// while "every check failed" means the run measured nothing at all, and
	// the raw count cannot tell those apart.
	Attempted int
}

// Observe matches events against c.
func Observe(c *contract.Contract, events []trajectory.Event) (*Observation, error) {
	if c == nil {
		return nil, fmt.Errorf("drift.Observe: no contract")
	}
	scored := trajectory.Contractable(events)
	o := &Observation{Checkpoints: map[string]bool{}, Total: len(scored)}

	// Compile every regexp once. A pattern that does not compile is a compiler
	// defect and must stop scoring rather than silently never matching.
	patterns := map[string]*regexp.Regexp{}
	compile := func(id, pat string) error {
		if pat == "" {
			return nil
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return fmt.Errorf("drift.Observe: rule %s has an uncompilable pattern %q: %w", id, pat, err)
		}
		patterns[id] = re
		return nil
	}

	matchedSeq := map[string]int{}
	claimed := map[int]bool{}

	for _, sm := range c.Steps {
		if err := compile(sm.ID, sm.Match.Pattern); err != nil {
			return nil, err
		}
		obs := StepObs{ID: sm.ID, Required: sm.Required}
		for _, e := range scored {
			ok, unevaluable, detail, err := matches(sm.Match, patterns[sm.ID], e)
			if err != nil {
				return nil, err
			}
			o.Attempted++
			if unevaluable {
				o.Unevaluable++
				o.UnevaluableDetail = append(o.UnevaluableDetail, detail)
				continue
			}
			if !ok {
				continue
			}
			obs.Matched, obs.Seq = true, e.Seq
			matchedSeq[sm.ID] = e.Seq
			claimed[e.Seq] = true
			break
		}
		o.Steps = append(o.Steps, obs)
	}

	// Order is resolved after every step has a sequence number, because an
	// "after" constraint can name a step declared later in the contract.
	for i, sm := range c.Steps {
		o.Steps[i].OrderOK = orderSatisfied(sm.Order, matchedSeq, o.Steps[i])
	}

	for _, fm := range c.Forbid {
		if err := compile(fm.ID, fm.Match.Pattern); err != nil {
			return nil, err
		}
		v := Violation{ID: fm.ID, Severity: fm.Severity}
		for _, e := range scored {
			ok, unevaluable, detail, err := matches(fm.Match, patterns[fm.ID], e)
			if err != nil {
				return nil, err
			}
			o.Attempted++
			if unevaluable {
				o.Unevaluable++
				o.UnevaluableDetail = append(o.UnevaluableDetail, detail)
				continue
			}
			if !ok {
				continue
			}
			v.Hits++
			if len(v.Evidence) < maxEvidence {
				v.Evidence = append(v.Evidence, evidenceFor(e))
			}
			claimed[e.Seq] = true
		}
		if v.Hits > 0 {
			o.Violations = append(o.Violations, v)
		}
	}

	for _, id := range c.Checkpoints {
		_, ran := matchedSeq[id]
		o.Checkpoints[id] = ran
	}

	for _, e := range scored {
		if !claimed[e.Seq] {
			o.OffContract++
		}
	}
	return o, nil
}

// maxEvidence bounds how many quotes a violation carries. Enough to see the
// pattern, not enough to make a report unreadable.
const maxEvidence = 5

// evidenceFor renders a short, human-checkable quote of what triggered a
// violation.
func evidenceFor(e trajectory.Event) string {
	if len(e.Paths) > 0 {
		return fmt.Sprintf("%s %s: %v", e.Type, e.Name, e.Paths)
	}
	return fmt.Sprintf("%s %s", e.Type, e.Name)
}

// isPatternError reports whether err is a compiler defect — a malformed
// MatchPath pattern — as opposed to a rejected candidate. contract.MatchPath
// wraps every error it returns in exactly one of contract.ErrBadPattern or
// contract.ErrBadCandidate for precisely this distinction: a malformed
// pattern means the compiled contract itself cannot be trusted for any
// candidate, so Observe must stop; a rejected candidate is a defect in one
// recorded event, so Observe counts it as unevaluable and keeps scoring the
// rest of the trajectory.
func isPatternError(err error) bool {
	return errors.Is(err, contract.ErrBadPattern)
}

// orderSatisfied reports whether a step's Order constraint holds, given the
// matched sequence number of every step (including this one) and this step's
// own observation.
//
// "any" and an unmatched step are always satisfied: an absent step is already
// penalised by coverage, so also failing its order would double-count one
// defect. "after" requires this step's sequence to exceed every listed
// predecessor's matched sequence, treating an unmatched predecessor as
// satisfied for the same reason. "strict" additionally requires the
// immediately preceding claimed sequence among the listed predecessors —
// not merely any earlier one — to be the predecessor's own matched sequence.
func orderSatisfied(order contract.Order, matchedSeq map[string]int, step StepObs) bool {
	if !step.Matched {
		return true
	}
	switch order.Mode {
	case "", "any":
		return true
	case "after":
		for _, pred := range order.After {
			predSeq, ok := matchedSeq[pred]
			if !ok {
				continue
			}
			if step.Seq <= predSeq {
				return false
			}
		}
		return true
	case "strict":
		var maxPred int
		found := false
		for _, pred := range order.After {
			predSeq, ok := matchedSeq[pred]
			if !ok {
				continue
			}
			if step.Seq <= predSeq {
				return false
			}
			if !found || predSeq > maxPred {
				maxPred = predSeq
				found = true
			}
		}
		if !found {
			// No predecessor was matched: absence is already penalised by
			// coverage.
			return true
		}
		return step.Seq == maxPred+1
	default:
		return true
	}
}

// matches reports whether one event satisfies one matcher, and separately
// whether the check could not be performed at all.
//
// A PathNotGlob is satisfied — i.e. is a violation of the forbid rule that
// carries it — when the event has at least one path and no path matches the
// glob. An event of the right type carrying no paths is unevaluable rather than
// clean: something upstream failed to record where it wrote.
func matches(m contract.Matcher, re *regexp.Regexp, e trajectory.Event) (ok, unevaluable bool, detail string, err error) {
	if m.Type != "" && e.Type != m.Type {
		return false, false, "", nil
	}
	if re != nil && !re.MatchString(e.Name) {
		return false, false, "", nil
	}

	switch {
	case m.PathGlob != "":
		for _, p := range e.Paths {
			hit, mErr := contract.MatchPath(m.PathGlob, p)
			if mErr != nil {
				if isPatternError(mErr) {
					return false, false, "", fmt.Errorf("drift.Observe: %w", mErr)
				}
				return false, true, fmt.Sprintf("%s against %s: %v", p, m.PathGlob, mErr), nil
			}
			if hit {
				return true, false, "", nil
			}
		}
		if len(e.Paths) == 0 {
			return false, true, fmt.Sprintf("%s event %d has no recorded path", e.Type, e.Seq), nil
		}
		return false, false, "", nil

	case m.PathNotGlob != "":
		if len(e.Paths) == 0 {
			return false, true, fmt.Sprintf("%s event %d has no recorded path", e.Type, e.Seq), nil
		}
		for _, p := range e.Paths {
			hit, mErr := contract.MatchPath(m.PathNotGlob, p)
			if mErr != nil {
				if isPatternError(mErr) {
					return false, false, "", fmt.Errorf("drift.Observe: %w", mErr)
				}
				return false, true, fmt.Sprintf("%s against %s: %v", p, m.PathNotGlob, mErr), nil
			}
			if !hit {
				return true, false, "", nil
			}
		}
		return false, false, "", nil
	}
	return true, false, "", nil
}
