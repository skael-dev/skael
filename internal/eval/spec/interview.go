package spec

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/skael-dev/skael/internal/eval/llm"
)

// ErrEmptyIntent is returned when there is nothing to interview about.
var ErrEmptyIntent = errors.New("spec: intent is empty")

// specSchema constrains the model's output. It is a trimmed JSON Schema rather
// than the full type: the gateway includes it in the prompt, and every byte of
// schema competes with the actual instructions for attention.
const specSchema = `{
  "type": "object",
  "required": ["name", "purpose", "description", "triggers", "steps", "target_tier"],
  "properties": {
    "name":        {"type": "string", "pattern": "^[a-z0-9]([a-z0-9-]*[a-z0-9])?$", "maxLength": 64},
    "purpose":     {"type": "string"},
    "description": {"type": "string", "maxLength": 1024},
    "triggers":    {"type": "array", "items": {"type": "object",
                     "properties": {"text": {"type": "string"}, "negative": {"type": "boolean"}},
                     "required": ["text"]}},
    "steps":       {"type": "array", "items": {"type": "object",
                     "properties": {"id": {"type": "string"}, "action": {"type": "string"},
                                    "postcondition": {"type": "string"}, "validation": {"type": "boolean"},
                                    "rationale": {"type": "string"}},
                     "required": ["id", "action", "postcondition"]}},
    "constraints": {"type": "array", "items": {"type": "object",
                     "properties": {"id": {"type": "string"}, "text": {"type": "string"},
                                    "kind": {"enum": ["must", "must_not"]},
                                    "severity": {"enum": ["critical", "major", "minor"]}},
                     "required": ["id", "text", "kind", "severity"]}},
    "resources":   {"type": "object"},
    "deps":        {"type": "object"},
    "target_tier": {"enum": ["floor", "mid", "strong"]}
  }
}`

const draftPrompt = `You are designing an agent skill from a user's description.

Produce a skill specification. Requirements:

- Every step states a verifiable postcondition — something a script could check.
  "Parse the file" is not a postcondition; "out/parsed.json exists and is valid JSON" is.
- Mark steps that are validations with "validation": true.
- Triggers must include concrete positive example prompts AND hard negatives:
  adjacent-domain near-misses that must NOT fire this skill. An obviously
  irrelevant negative tests nothing.
- The description states what the skill does AND when to use it, naming concrete
  trigger nouns. Be assertive about when to use it — skills under-fire far more
  often than they over-fire.
- Plan at most 3 resource modules. Focused skills outperform exhaustive bundles.
- Anything mechanical and repeatable belongs in scripts/, because models execute
  scripts more reliably than they follow prose.

The user's description:

%s`

const critiquePrompt = `Here is a draft skill specification you produced:

%s

Critique and repair it. Check specifically for:

- Steps whose postcondition is not mechanically verifiable.
- Vague trigger phrases, or negatives that are too obviously irrelevant to be
  discriminating.
- Constraints that cannot be checked against an observed action.
- A description that says what the skill does but not when to use it.
- Missing validation checkpoints between steps that can fail silently.
%s
Return the corrected specification.`

// Interview turns a natural-language intent into a validated SkillSpec using two
// gateway calls: a structured draft, then a self-critique pass that is shown the
// draft together with its own validation errors.
//
// The second call is a repair, not a re-roll — it is told exactly what was wrong.
// Re-asking the same question without that feedback tends to reproduce the same
// defect and spends a session to do it.
func Interview(ctx context.Context, g llm.Gateway, intent string) (*SkillSpec, error) {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return nil, ErrEmptyIntent
	}

	draft, err := llm.CompleteJSON[SkillSpec](ctx, g, llm.Req{
		Role:       "interview.draft",
		Prompt:     fmt.Sprintf(draftPrompt, intent),
		Schema:     []byte(specSchema),
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return nil, err
	}

	var buf strings.Builder
	if errs := draft.Validate(); len(errs) > 0 {
		buf.WriteString("\nValidation of your draft reported these problems — fix each one:\n")
		for _, e := range errs {
			fmt.Fprintf(&buf, "- %s\n", e)
		}
	}

	rendered, err := renderYAML(&draft)
	if err != nil {
		return nil, err
	}

	final, err := llm.CompleteJSON[SkillSpec](ctx, g, llm.Req{
		Role:       "interview.critique",
		Prompt:     fmt.Sprintf(critiquePrompt, rendered, buf.String()),
		Schema:     []byte(specSchema),
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return nil, err
	}

	if errs := final.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("spec.Interview: specification still invalid after critique: %v", errs)
	}
	return &final, nil
}

// renderYAML is the draft's representation inside the critique prompt. YAML
// rather than JSON: it is what the human sees at the approval gate, so the model
// critiques the same artifact the reviewer will read.
func renderYAML(s *SkillSpec) (string, error) {
	var b strings.Builder
	if err := s.Save(&b); err != nil {
		return "", err
	}
	return b.String(), nil
}
