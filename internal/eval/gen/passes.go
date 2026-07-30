package gen

import (
	"context"
	"fmt"
	"strings"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// outlineRes is the outline pass's response: the section plan the body pass
// then writes to.
type outlineRes struct {
	Sections []string `json:"sections"`
}

// bodyRes is the body pass's response: the SKILL.md body markdown, without
// frontmatter.
type bodyRes struct {
	Body string `json:"body"`
}

// resourcesRes is the resources pass's response: every planned bundle file,
// in one call. Paths are untrusted model output — assemble is what enforces
// that none of them escape the bundle directory.
type resourcesRes struct {
	Files []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
}

// descriptionRes is the description pass's response: the frontmatter
// description, written to the what+when+nouns formula rather than reused
// from the spec's own (often terser) Description field.
type descriptionRes struct {
	Description string `json:"description"`
}

// runOutline asks for a short section plan for the skill body. The plan is
// threaded into the body pass so body writing has explicit structure to fill
// rather than free rein.
func runOutline(ctx context.Context, g llm.Gateway, s *spec.SkillSpec) (outlineRes, error) {
	return llm.CompleteJSON[outlineRes](ctx, g, llm.Req{
		Role:       "gen.outline",
		Prompt:     outlinePrompt(s),
		Schema:     []byte(`{"type":"object","properties":{"sections":{"type":"array","items":{"type":"string"}}},"required":["sections"]}`),
		ModelClass: llm.ClassStrong,
	})
}

func outlinePrompt(s *spec.SkillSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan the section outline for a skill's SKILL.md body.\n\n")
	fmt.Fprintf(&b, "Skill name: %s\n", s.Name)
	fmt.Fprintf(&b, "Purpose: %s\n\n", s.Purpose)
	b.WriteString("Steps the body must cover, in order:\n")
	b.WriteString(renderSteps(s.Steps))
	b.WriteString("\nRespond with JSON: {\"sections\": [\"...\", \"...\"]} — a short, " +
		"ordered list of section headings (e.g. \"Overview\", \"Steps\", " +
		"\"Failure handling\"). No prose outside the JSON.\n")
	return b.String()
}

// runBody asks for the full SKILL.md body markdown, under the robustness
// rules, written to the outline's section plan.
func runBody(ctx context.Context, g llm.Gateway, s *spec.SkillSpec, outline outlineRes) (bodyRes, error) {
	return llm.CompleteJSON[bodyRes](ctx, g, llm.Req{
		Role:       "gen.body",
		Prompt:     bodyPrompt(s, outline),
		Schema:     []byte(`{"type":"object","properties":{"body":{"type":"string"}},"required":["body"]}`),
		ModelClass: llm.ClassStrong,
	})
}

func bodyPrompt(s *spec.SkillSpec, outline outlineRes) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Write the markdown body of a skill's SKILL.md (everything after the\n")
	b.WriteString("frontmatter — no frontmatter, just the body).\n\n")
	fmt.Fprintf(&b, "Skill name: %s\n", s.Name)
	fmt.Fprintf(&b, "Purpose: %s\n", s.Purpose)
	fmt.Fprintf(&b, "Target tier: %s\n\n", s.TargetTier)

	b.WriteString("Section outline to fill:\n")
	for _, sec := range outline.Sections {
		fmt.Fprintf(&b, "- %s\n", sec)
	}
	b.WriteString("\n")

	b.WriteString("Steps the body must render as imperative numbered steps, each keeping its\n")
	b.WriteString("stated postcondition verbatim or near-verbatim:\n")
	b.WriteString(renderSteps(s.Steps))
	b.WriteString("\n")

	if plan := renderResourcePlan(s.Resources); plan != "" {
		b.WriteString("Bundled resources the steps may reference by their exact relative path:\n")
		b.WriteString(plan)
		b.WriteString("\n")
	}

	b.WriteString(robustnessRules)
	b.WriteString("\n\nDo not use hedge words — \"consider\", \"if appropriate\", \"as needed\",\n")
	b.WriteString("\"ideally\" — inside any numbered step; a step is a binding instruction, not a\n")
	b.WriteString("suggestion.\n\n")
	b.WriteString("Respond with JSON: {\"body\": \"...\"} — the complete markdown body as a single\n")
	b.WriteString("string. No prose outside the JSON.\n")
	return b.String()
}

// runResources asks for the content of every planned bundle file in one
// call, rather than one call per file: the files are small and related, and
// one call lets the model keep cross-references between them consistent.
func runResources(ctx context.Context, g llm.Gateway, s *spec.SkillSpec) (resourcesRes, error) {
	return llm.CompleteJSON[resourcesRes](ctx, g, llm.Req{
		Role:       "gen.resources",
		Prompt:     resourcesPrompt(s),
		Schema:     []byte(`{"type":"object","properties":{"files":{"type":"array"}},"required":["files"]}`),
		ModelClass: llm.ClassStrong,
	})
}

func resourcesPrompt(s *spec.SkillSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Write the content of every planned resource file for the skill %q.\n\n", s.Name)
	b.WriteString("Planned files (write exactly these paths, no others):\n")
	b.WriteString(renderResourcePlan(s.Resources))
	b.WriteString("\n")
	b.WriteString("Each path is relative to the bundle root and must stay a plain relative path\n")
	b.WriteString("under scripts/, references/, or assets/ — no leading \"/\", no \"..\" segment, no\n")
	b.WriteString("symlink. A script must be a self-contained, runnable file (e.g. a python3 or\n")
	b.WriteString("bash script with a shebang); a reference or asset should be the file's literal\n")
	b.WriteString("content, not a description of it.\n\n")
	b.WriteString("Respond with JSON: {\"files\": [{\"path\": \"...\", \"content\": \"...\"}, ...]} — one\n")
	b.WriteString("entry per planned file. No prose outside the JSON.\n")
	return b.String()
}

// runDescription asks for the frontmatter description, written to the
// what+when+nouns formula rather than reused from the spec's own Description
// field, which is written for a human reviewer and is often terser than what
// triggers reliable activation.
func runDescription(ctx context.Context, g llm.Gateway, s *spec.SkillSpec) (descriptionRes, error) {
	return llm.CompleteJSON[descriptionRes](ctx, g, llm.Req{
		Role:       "gen.description",
		Prompt:     descriptionPrompt(s),
		Schema:     []byte(`{"type":"object","properties":{"description":{"type":"string"}},"required":["description"]}`),
		ModelClass: llm.ClassStrong,
	})
}

func descriptionPrompt(s *spec.SkillSpec) string {
	var b strings.Builder
	b.WriteString("Write the frontmatter \"description\" field for a skill.\n\n")
	fmt.Fprintf(&b, "Skill name: %s\n", s.Name)
	fmt.Fprintf(&b, "Purpose: %s\n", s.Purpose)
	fmt.Fprintf(&b, "Human-written description (for reference, do not just copy it): %s\n\n", s.Description)

	b.WriteString("Example prompts that should trigger this skill:\n")
	for _, t := range s.Triggers {
		if t.Negative {
			continue
		}
		fmt.Fprintf(&b, "- %s\n", t.Text)
	}
	b.WriteString("\n")

	b.WriteString("Write exactly one sentence pair to this formula:\n")
	b.WriteString("1. What the skill does.\n")
	b.WriteString("2. When to use it, stated assertively (\"Use when ...\" or \"Use this when ...\"),\n")
	b.WriteString("   naming the concrete nouns a user's request would contain — not a vague\n")
	b.WriteString("   category. Skills under-trigger far more often than they over-trigger, so be\n")
	b.WriteString("   assertive and unambiguous about when this applies.\n\n")
	b.WriteString("The result must be at most 1024 bytes and contain no newline.\n\n")
	b.WriteString("Respond with JSON: {\"description\": \"...\"}. No prose outside the JSON.\n")
	return b.String()
}

// renderSteps renders a spec's steps as a numbered list a prompt can quote
// directly.
func renderSteps(steps []spec.Step) string {
	var b strings.Builder
	for i, st := range steps {
		fmt.Fprintf(&b, "%d. [%s] %s (postcondition: %s)", i+1, st.ID, st.Action, st.Postcondition)
		if st.Validation {
			b.WriteString(" [validation checkpoint]")
		}
		if st.Rationale != "" {
			fmt.Fprintf(&b, " — rationale: %s", st.Rationale)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderResourcePlan renders every planned resource item as a bullet the
// prompt can quote directly, in the order the model should describe them.
func renderResourcePlan(plan spec.ResourcePlan) string {
	var b strings.Builder
	for _, items := range [][]spec.ResourceItem{plan.Scripts, plan.References, plan.Assets} {
		for _, it := range items {
			fmt.Fprintf(&b, "- %s", it.Path)
			if it.Purpose != "" {
				fmt.Fprintf(&b, " (%s)", it.Purpose)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}
