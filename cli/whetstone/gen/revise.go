package gen

import (
	"context"
	"fmt"
	"strings"

	"github.com/skael-dev/skael/internal/eval/lint"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// maxRevisionAttempts caps the lint-and-revise loop. A revision that hasn't
// cleared lint in two tries is not going to on a third; the CLI's own lint
// gate (new.go, gen.go) is the backstop, so this loop only ever improves the
// bundle rather than deciding whether generation succeeded.
const maxRevisionAttempts = 2

// bodyOwnedRules and frontmatterOwnedRules route a lint finding to the pass
// that can act on it. A rule in neither map — too-many-modules, injection,
// file-level conformance — cannot be fixed by rewriting body or description
// text, so a finding there never triggers a revision call.
var bodyOwnedRules = map[string]bool{
	"body-token-budget":          true,
	"body-too-long":              true,
	"hedge-word":                 true,
	"step-without-postcondition": true,
	"no-terminal-fallback":       true,
	"global-only-guardrail":      true,
	// Resources come from the approved spec, so gen cannot add the missing
	// file — dropping or correcting the link is the body's job.
	"broken-link": true,
}

var frontmatterOwnedRules = map[string]bool{
	"metadata-token-budget":  true,
	"description-no-trigger": true,
	"description-missing":    true,
	"description-too-long":   true,
}

// reviseUntilClean lints the assembled bundle and, while a SeverityError
// finding remains that a revision pass can act on, asks the pass that owns
// the offending output to rewrite it and re-assembles. It always returns the
// latest bundle with a nil error — see maxRevisionAttempts.
//
// A body-budget finding runs the deterministic offload (offload.go) first,
// ahead of the model loop below and outside maxRevisionAttempts: offloading
// costs nothing and is guaranteed to shrink the body, where a model asked to
// cut it converges slowly, if at all, because it cannot count its own
// tokens.
func reviseUntilClean(ctx context.Context, g llm.Gateway, s *spec.SkillSpec, outDir string, body, description *string, resources resourcesRes) (*Bundle, error) {
	b, err := assemble(s, outDir, *body, *description, resources)
	if err != nil {
		return nil, fmt.Errorf("gen: assembling bundle: %w", err)
	}

	b, resources, err = offloadIfOverBudget(s, outDir, body, *description, resources, b)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < maxRevisionAttempts; attempt++ {
		res, err := lint.Run(b.Dir)
		if err != nil {
			return nil, fmt.Errorf("gen: linting generated bundle: %w", err)
		}
		if !res.HasErrors() {
			break
		}

		bodyFindings, descFindings, fixable := routeFindings(res.Findings)
		if !fixable {
			break
		}

		if len(bodyFindings) > 0 {
			rev, err := runBodyRevision(ctx, g, s, *body, bodyFindings)
			if err != nil {
				return nil, fmt.Errorf("gen: body revision pass: %w", err)
			}
			*body = rev.Body
		}
		if len(descFindings) > 0 {
			rev, err := runDescriptionRevision(ctx, g, s, *description, descFindings)
			if err != nil {
				return nil, fmt.Errorf("gen: description revision pass: %w", err)
			}
			*description = rev.Description
		}

		b, err = assemble(s, outDir, *body, *description, resources)
		if err != nil {
			return nil, fmt.Errorf("gen: assembling bundle: %w", err)
		}
	}

	return b, nil
}

// offloadIfOverBudget lints the just-assembled bundle and, if it trips
// body-token-budget or body-too-long, runs offloadOverBudgetSections and
// re-assembles with the produced reference files appended to resources so
// they're actually written and appear in Bundle.Files. Returns b and
// resources unchanged if there was nothing to offload.
func offloadIfOverBudget(s *spec.SkillSpec, outDir string, body *string, description string, resources resourcesRes, b *Bundle) (*Bundle, resourcesRes, error) {
	res, err := lint.Run(b.Dir)
	if err != nil {
		return nil, resourcesRes{}, fmt.Errorf("gen: linting generated bundle: %w", err)
	}
	if !hasBudgetFinding(res.Findings) {
		return b, resources, nil
	}

	newBody, refs := offloadOverBudgetSections(*body, resources.Files)
	if len(refs) == 0 {
		return b, resources, nil
	}
	*body = newBody
	resources.Files = append(resources.Files, refs...)

	b, err = assemble(s, outDir, *body, description, resources)
	if err != nil {
		return nil, resourcesRes{}, fmt.Errorf("gen: assembling bundle: %w", err)
	}
	return b, resources, nil
}

// hasBudgetFinding reports whether findings include a body length or token
// budget error — the two rules offload can act on.
func hasBudgetFinding(findings []lint.Finding) bool {
	for _, f := range findings {
		if f.Rule == "body-token-budget" || f.Rule == "body-too-long" {
			return true
		}
	}
	return false
}

// routeFindings splits every finding into the body-owned and
// frontmatter-owned buckets a revision call can act on — including
// warn-severity findings in the same category, since a revision call already
// rewriting the body may as well clear them too. fixable reports whether any
// SeverityError finding landed in either bucket; if every error is one
// neither pass can touch, the caller stops rather than spending a call that
// cannot help.
func routeFindings(findings []lint.Finding) (body, frontmatter []lint.Finding, fixable bool) {
	for _, f := range findings {
		switch {
		case bodyOwnedRules[f.Rule]:
			body = append(body, f)
			fixable = fixable || f.Severity == lint.SeverityError
		case frontmatterOwnedRules[f.Rule]:
			frontmatter = append(frontmatter, f)
			fixable = fixable || f.Severity == lint.SeverityError
		}
	}
	return body, frontmatter, fixable
}

// runBodyRevision asks for a rewritten body that clears the given findings.
// Role "gen.revise.body" (distinct from "gen.body") is what makes the
// progress decorator and the cache treat it as its own call.
func runBodyRevision(ctx context.Context, g llm.Gateway, s *spec.SkillSpec, body string, findings []lint.Finding) (bodyRes, error) {
	res, _, err := llm.CompleteJSON[bodyRes](ctx, g, llm.Req{
		Role:       "gen.revise.body",
		Prompt:     bodyRevisionPrompt(s, body, findings),
		Schema:     []byte(`{"type":"object","properties":{"body":{"type":"string"}},"required":["body"]}`),
		ModelClass: llm.ClassStrong,
	})
	return res, err
}

func bodyRevisionPrompt(s *spec.SkillSpec, body string, findings []lint.Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Revise the markdown body of the skill %q to fix the lint findings below.\n", s.Name)
	b.WriteString("Keep every step, guardrail, and postcondition the findings do not mention —\n")
	b.WriteString("this is a targeted fix, not a rewrite.\n\n")
	b.WriteString("Current body:\n---\n")
	b.WriteString(body)
	b.WriteString("\n---\n\n")
	b.WriteString("Findings to fix:\n")
	b.WriteString(renderFindings(findings))
	b.WriteString("\n")
	if hasBudgetFinding(findings) {
		b.WriteString(bodyBudgetInstruction(body))
	}
	b.WriteString(RobustnessRules())
	b.WriteString("\n\nRespond with JSON: {\"body\": \"...\"} — the complete revised markdown body\n")
	b.WriteString("as a single string. No prose outside the JSON.\n")
	return b.String()
}

// bodyBudgetInstruction states the required cut in bytes, against the offload
// pass's target of ~85% of the token budget rather than the limit itself —
// tokens are a unit the model cannot perceive, but a byte count and a
// concrete "remove at least N" is something it can act on, and landing
// exactly on the limit is how the next pass re-trips the same finding.
// offloadOverBudgetSections already ran ahead of this call and moved what it
// could into references/; whatever's left is body content the model has to
// actually cut or restructure.
func bodyBudgetInstruction(body string) string {
	current := len(body)
	cut := current - offloadTargetBytes
	if cut < 0 {
		cut = 0
	}
	return fmt.Sprintf(
		"Your body is %d bytes; to clear the budget with headroom it must be at most "+
			"%d bytes — remove or move to references/ at least %d bytes. Cut declarative "+
			"content (rules, notes, examples, troubleshooting) rather than steps; if a\n"+
			"section is reference material, replace it in the body with a short pointer\n"+
			"and put the detail in a references/ file instead of deleting it outright.\n\n",
		current, offloadTargetBytes, cut)
}

// runDescriptionRevision asks for a rewritten description that clears the
// given findings.
func runDescriptionRevision(ctx context.Context, g llm.Gateway, s *spec.SkillSpec, description string, findings []lint.Finding) (descriptionRes, error) {
	res, _, err := llm.CompleteJSON[descriptionRes](ctx, g, llm.Req{
		Role:       "gen.revise.description",
		Prompt:     descriptionRevisionPrompt(s, description, findings),
		Schema:     []byte(`{"type":"object","properties":{"description":{"type":"string"}},"required":["description"]}`),
		ModelClass: llm.ClassStrong,
	})
	return res, err
}

func descriptionRevisionPrompt(s *spec.SkillSpec, description string, findings []lint.Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Revise the frontmatter \"description\" field for the skill %q to fix the\n", s.Name)
	b.WriteString("lint findings below, keeping the what+when+nouns formula.\n\n")
	fmt.Fprintf(&b, "Current description: %s\n\n", description)
	b.WriteString("Findings to fix:\n")
	b.WriteString(renderFindings(findings))
	fmt.Fprintf(&b, "\nThe result must be at most %d bytes and contain no newline.\n\n", descriptionByteBudget(s))
	b.WriteString("Respond with JSON: {\"description\": \"...\"}. No prose outside the JSON.\n")
	return b.String()
}

// renderFindings renders findings as bullets a prompt can quote directly.
func renderFindings(findings []lint.Finding) string {
	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", f.Rule, f.Severity, f.Message)
	}
	return b.String()
}
