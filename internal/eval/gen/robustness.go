package gen

import (
	"fmt"

	"github.com/skael-dev/skael/internal/eval/lint"
)

// RobustnessRules returns the rule text the body pass is written under. The
// budget line is built from lint's own exported constants rather than
// restated as prose, so the generator and the linter cannot state two
// different numbers.
func RobustnessRules() string {
	return fmt.Sprintf(`Write the skill body under these rules:

- Imperative, numbered steps. One action per step.
- Every step states a verifiable postcondition — something a script could check.
- Embed validation checkpoints as executable commands: "run scripts/validate.py;
  exit != 0 -> fix before continuing".
- Place guardrails locally, adjacent to the step they govern. A guardrail only in
  a global preamble is not followed reliably.
- Assume zero conversation context: explicit relative paths, explicit tool names,
  self-contained input-to-output examples.
- Use templates rather than prose descriptions of output formats.
- Body under %d lines and roughly %d tokens. Metadata under ~%d tokens. Keep
  declarative material — rules, notes, examples, troubleshooting, a
  glossary — in a clearly headed section (e.g. "## Rules and constraints");
  if the body is running long, that detail belongs in references/, not
  crammed into the step-by-step procedure.
- End with a terminal fallback: "if a checkpoint cannot be satisfied after one
  retry, stop and report state".
- Never write "consider", "if appropriate", "as needed", or "ideally" inside a
  step. Steps are binding instructions, not suggestions.
- Give a step a one-line rationale ("do X - otherwise Y") where it aids
  compliance. Reasoning-annotated instructions are followed more reliably than
  bare ALWAYS/NEVER directives.`, lint.MaxBodyLines, lint.MaxBodyApproxTokens, lint.MaxMetadataApproxTokens)
}
