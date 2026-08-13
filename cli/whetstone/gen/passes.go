package gen

import (
	"context"
	"fmt"
	"strings"

	"github.com/skael-dev/skael/internal/eval/lint"
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

// resourceFile is one bundle file's path (from the approved spec) and
// content (from the model).
type resourceFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// resourcesRes is the resources pass's aggregate result: every planned
// bundle file, assembled from one gateway call per file (see runResources).
// Paths come from the spec, not the model — assemble's traversal checks stay
// in place regardless, as defense in depth.
type resourcesRes struct {
	Files []resourceFile `json:"files"`
}

// resourceItemRes is a single planned file's content — the resources pass's
// per-call response.
type resourceItemRes struct {
	Content string `json:"content"`
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
	res, _, err := llm.CompleteJSON[outlineRes](ctx, g, llm.Req{
		Role:       "gen.outline",
		Prompt:     outlinePrompt(s),
		Schema:     []byte(`{"type":"object","properties":{"sections":{"type":"array","items":{"type":"string"}}},"required":["sections"]}`),
		ModelClass: llm.ClassStrong,
	})
	return res, err
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
	res, _, err := llm.CompleteJSON[bodyRes](ctx, g, llm.Req{
		Role:       "gen.body",
		Prompt:     bodyPrompt(s, outline),
		Schema:     []byte(`{"type":"object","properties":{"body":{"type":"string"}},"required":["body"]}`),
		ModelClass: llm.ClassStrong,
	})
	return res, err
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

	if rules := renderConstraints(s.Constraints); rules != "" {
		b.WriteString("Constraints the skill is scored against. State each one in the body as a\n")
		b.WriteString("guardrail placed next to the step it governs, not only in a preamble:\n")
		b.WriteString(rules)
		b.WriteString("\n")
	}

	if plan := renderResourcePlan(s.Resources); plan != "" {
		b.WriteString("Bundled resources the steps may reference by their exact relative path:\n")
		b.WriteString(plan)
		b.WriteString("\n")
	}

	b.WriteString(RobustnessRules())
	b.WriteString("\n\nDo not use hedge words — \"consider\", \"if appropriate\", \"as needed\",\n")
	b.WriteString("\"ideally\" — inside any numbered step; a step is a binding instruction, not a\n")
	b.WriteString("suggestion.\n\n")
	b.WriteString("Respond with JSON: {\"body\": \"...\"} — the complete markdown body as a single\n")
	b.WriteString("string. No prose outside the JSON.\n")
	return b.String()
}

// runResources asks for one file per call, not all of them in one: a spec
// planning several substantial scripts pushes a single response past the
// gateway's timeout. Each call still gets the whole plan as context, so
// cross-file references stay consistent. Sequential on purpose — the token
// cost is the same, and concurrent sessions would scramble progress output.
func runResources(ctx context.Context, g llm.Gateway, s *spec.SkillSpec) (resourcesRes, error) {
	var res resourcesRes
	for _, items := range [][]spec.ResourceItem{s.Resources.Scripts, s.Resources.References, s.Resources.Assets} {
		for _, item := range items {
			r, _, err := llm.CompleteJSON[resourceItemRes](ctx, g, llm.Req{
				Role:       "gen.resources:" + item.Path,
				Prompt:     resourceItemPrompt(s, item),
				Schema:     []byte(`{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}`),
				ModelClass: llm.ClassStrong,
			})
			if err != nil {
				return resourcesRes{}, err
			}
			res.Files = append(res.Files, resourceFile{Path: item.Path, Content: r.Content})
		}
	}
	return res, nil
}

func resourceItemPrompt(s *spec.SkillSpec, item spec.ResourceItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Write the content of one planned resource file for the skill %q.\n\n", s.Name)
	b.WriteString("Full resource plan, for cross-file consistency — write only the one file\n")
	b.WriteString("named below; the rest are written in separate calls:\n")
	b.WriteString(renderResourcePlan(s.Resources))
	b.WriteString("\n")
	fmt.Fprintf(&b, "Write: %s", item.Path)
	if item.Purpose != "" {
		fmt.Fprintf(&b, " (%s)", item.Purpose)
	}
	b.WriteString("\n\n")
	b.WriteString("A script must be a self-contained, runnable file (e.g. a python3 or bash\n")
	b.WriteString("script with a shebang); a reference or asset should be the file's literal\n")
	b.WriteString("content, not a description of it.\n\n")
	b.WriteString("Respond with JSON: {\"content\": \"...\"}. No prose outside the JSON.\n")
	return b.String()
}

// runDescription asks for the frontmatter description, written to the
// what+when+nouns formula rather than reused from the spec's own Description
// field, which is written for a human reviewer and is often terser than what
// triggers reliable activation.
func runDescription(ctx context.Context, g llm.Gateway, s *spec.SkillSpec) (descriptionRes, error) {
	res, _, err := llm.CompleteJSON[descriptionRes](ctx, g, llm.Req{
		Role:       "gen.description",
		Prompt:     descriptionPrompt(s),
		Schema:     []byte(`{"type":"object","properties":{"description":{"type":"string"}},"required":["description"]}`),
		ModelClass: llm.ClassStrong,
	})
	return res, err
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
	fmt.Fprintf(&b, "The result must be at most %d bytes and contain no newline.\n\n", descriptionByteBudget(s))
	b.WriteString("Respond with JSON: {\"description\": \"...\"}. No prose outside the JSON.\n")
	return b.String()
}

// descriptionByteBudget is what's left of lint.MaxMetadataApproxTokens (a
// token count, converted to bytes at the same bytes/4 approximation
// lint.ApproxTokens uses) after the frontmatter's fixed overhead — the "---"
// fences, the "name: <name>" line, and the "description: " key. Computed per
// skill because the name's length is part of that overhead.
func descriptionByteBudget(s *spec.SkillSpec) int {
	overhead := len("---\n") + len("name: "+s.DirName()+"\n") + len("description: \n") + len("---\n")
	budget := lint.MaxMetadataApproxTokens*4 - overhead
	if budget < 0 {
		budget = 0
	}
	return budget
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

// renderConstraints renders a spec's constraints as bullets the prompt can
// quote directly, each tagged with its kind and severity.
//
// The generator has to see these: contract.Compile turns every MUST-NOT into a
// violation weighted by the same severity, and the eval suite's task prompts
// render them too. Writing the body without them means generating a skill
// against rules it is never told about, and satisfying the guardrail-placement
// rule the body prompt states only by accident.
func renderConstraints(rules []spec.Rule) string {
	var b strings.Builder
	for _, r := range rules {
		kind := "MUST"
		if r.Kind == spec.RuleMustNot {
			kind = "MUST NOT"
		}
		fmt.Fprintf(&b, "- [%s] %s", kind, r.Text)
		if r.Severity != "" {
			fmt.Fprintf(&b, " (severity: %s)", r.Severity)
		}
		b.WriteString("\n")
	}
	return b.String()
}
