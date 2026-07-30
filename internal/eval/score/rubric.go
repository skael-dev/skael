package score

import (
	"fmt"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// anchoredScale is shared by every rubric prompt this package builds. It gives
// the 1-5 axis fixed meaning across calls, tasks, and skills — an unanchored
// "rate this 1-5" question is a different question every time it is asked.
const anchoredScale = `  5 — completed the task, verified its own output, and left the workspace as the
      task described.
  4 — completed the task correctly, with no verification step.
  3 — partially completed: the right shape of output with a material error.
  2 — attempted the task and produced nothing usable.
  1 — did not attempt the task, or destroyed inputs.`

// pairwisePrompt asks for a comparison of two transcripts on the same task.
//
// Every element here is a determinism mitigation, not decoration. Agent CLIs
// expose no temperature, so an unanchored "which is better" question is a
// sample from a distribution whose shape nothing controls. The anchors give the
// scale fixed meaning; naming the skill's purpose stops the judge inventing its
// own criteria; and demanding a verbatim quote is what makes a verdict
// checkable — a judge that cannot cite the run did not read it.
func pairwisePrompt(sp *spec.SkillSpec, p Pair, first, second Sample) string {
	return fmt.Sprintf(`You are grading two attempts at the same task. One had a skill available; the
other did not. You are not told which is which.

Skill purpose: %s
Task: %s

Rate each attempt 1-5 against this anchored scale:
%s

Candidate A:
%s

Candidate B:
%s

Reply with JSON only:
{"winner": "A"|"B"|"tie", "margin": <0..1>, "evidence": ["<verbatim quote from
the winning candidate>"]}

"margin" is (winner's rating - loser's rating) / 4. Every quote in "evidence"
must appear verbatim in one of the candidates above; a verdict without one is
discarded.`, sp.Purpose, p.Prompt, anchoredScale, first.Transcript, second.Transcript)
}

// semanticPrompt asks whether a single sample satisfies one semantic rule —
// a requirement with no deterministic check. The same evidence discipline
// applies: an unsupported "yes" is not evidence of adherence.
func semanticPrompt(sp *spec.SkillSpec, r contract.SemanticRule, s Sample) string {
	return fmt.Sprintf(`You are grading whether a single run of a skill satisfied one requirement
that cannot be checked mechanically.

Skill purpose: %s
Requirement: %s

Transcript:
%s

Reply with JSON only:
{"satisfied": true|false, "confidence": <0..1>, "evidence": ["<verbatim quote
from the transcript that supports your answer>"]}

Every quote in "evidence" must appear verbatim in the transcript above; a
verdict without one is discarded.`, sp.Purpose, r.Text, s.Transcript)
}
