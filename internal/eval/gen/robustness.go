package gen

// robustnessRules is injected into the body pass and mirrored by the quality
// lints, so the generator writes under the same rules the linter enforces.
// Divergence between the two would produce bundles that fail their own lint.
const robustnessRules = `Write the skill body under these rules:

- Imperative, numbered steps. One action per step.
- Every step states a verifiable postcondition — something a script could check.
- Embed validation checkpoints as executable commands: "run scripts/validate.py;
  exit != 0 -> fix before continuing".
- Place guardrails locally, adjacent to the step they govern. A guardrail only in
  a global preamble is not followed reliably.
- Assume zero conversation context: explicit relative paths, explicit tool names,
  self-contained input-to-output examples.
- Use templates rather than prose descriptions of output formats.
- Body under 500 lines and roughly 5000 tokens. Metadata under ~100 tokens.
- End with a terminal fallback: "if a checkpoint cannot be satisfied after one
  retry, stop and report state".
- Never write "consider", "if appropriate", "as needed", or "ideally" inside a
  step. Steps are binding instructions, not suggestions.
- Give a step a one-line rationale ("do X - otherwise Y") where it aids
  compliance. Reasoning-annotated instructions are followed more reliably than
  bare ALWAYS/NEVER directives.`

// RobustnessRules returns the rule text the body pass is written under.
func RobustnessRules() string { return robustnessRules }
