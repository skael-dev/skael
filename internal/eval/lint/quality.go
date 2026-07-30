package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/skill"
)

// Package-level rule data, so the rule set is readable as data rather than
// buried in control flow.

// hedges are words and phrases that make an instruction non-binding. Prose
// may legitimately use them; a numbered step must not, since an agent
// following a step needs an unambiguous action.
var hedges = []string{"consider", "if appropriate", "as needed", "ideally", "you may want to"}

// hedgeRule pairs a hedge phrase with its compiled word-boundary matcher.
type hedgeRule struct {
	word    string
	pattern *regexp.Regexp
}

var hedgeRules = buildHedgeRules()

func buildHedgeRules() []hedgeRule {
	rules := make([]hedgeRule, len(hedges))
	for i, h := range hedges {
		rules[i] = hedgeRule{word: h, pattern: regexp.MustCompile(`\b` + regexp.QuoteMeta(h) + `\b`)}
	}
	return rules
}

// stepLine matches a numbered step. Only step text is binding: prose may
// legitimately hedge, and flagging prose trains users to ignore the linter.
var stepLine = regexp.MustCompile(`^\s*\d+\.\s+`)

// postconditionMarker is how a step declares its verifiable outcome.
var postconditionMarker = regexp.MustCompile(`(?i)postcondition|exits? 0|→|results? in`)

// terminalFallback is the required stop-and-report rule.
var terminalFallback = regexp.MustCompile(`(?i)(stop and report|halt and report|report state|stop, and report)`)

// triggerLanguage is what makes a description fire. Skills under-trigger far
// more often than they over-trigger, so an assertive "use when" is required.
var triggerLanguage = regexp.MustCompile(`(?i)(use when|when the user|use this when|triggers? (on|when))`)

// guardrailWord matches an imperative guardrail keyword. Guardrails belong
// next to the step they govern, not only in a preamble read once and
// forgotten by the time the model reaches step 8.
var guardrailWord = regexp.MustCompile(`\b(MUST NOT|MUST|NEVER|ALWAYS)\b`)

// maxBodyLines is the line budget for a skill body.
const maxBodyLines = 500

// maxBodyApproxTokens is the token budget for a skill body. The design target
// is roughly 5000 tokens, but ApproxTokens (bytes/4) is a coarse estimate
// that undercounts short, densely-punctuated instruction lines relative to
// natural prose; the enforced budget is set below the nominal target so the
// check remains reachable for genuinely over-long bodies rather than only
// ones padded with long words.
const maxBodyApproxTokens = 3000

// maxMetadataApproxTokens is the token budget for frontmatter.
const maxMetadataApproxTokens = 100

// ApproxTokens estimates a token count as bytes/4. This is deliberately an
// approximation — adding a tokenizer dependency to check a soft budget is not
// worth it — so every message derived from it says so.
func ApproxTokens(s string) int { return len(s) / 4 }

// Quality checks whether a bundle reads the way skills that actually work are
// written: hedge-free steps with postconditions, a terminal fallback rule, a
// description with assertive trigger language, guardrails placed near the
// steps they govern, and body/metadata within their token and line budgets.
func Quality(bundleDir string) ([]Finding, error) {
	skillPath := filepath.Join(bundleDir, "SKILL.md")

	raw, err := os.ReadFile(skillPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Conformance already reports a missing SKILL.md; nothing more to
			// check here.
			return nil, nil
		}
		return nil, fmt.Errorf("lint.Quality: reading SKILL.md: %w", err)
	}
	content := string(raw)

	fm, body, fmErr := skill.ParseFrontmatter(content)
	frontmatterText := ""
	if fmErr == nil {
		frontmatterText = content[:len(content)-len(body)]
	} else {
		// Conformance already reports invalid frontmatter; fall back to
		// treating the whole file as body so the step-level checks still run.
		body = content
	}

	var findings []Finding

	findings = append(findings, checkSteps(body, frontmatterText)...)
	findings = append(findings, checkBudgets(body, frontmatterText)...)

	modCount, err := countModules(bundleDir)
	if err != nil {
		return nil, err
	}
	if modCount > spec.MaxModules {
		findings = append(findings, Finding{
			Rule:     "too-many-modules",
			Severity: SeverityError,
			File:     "SKILL.md",
			Message:  fmt.Sprintf("bundle has %d files across scripts/, references/, assets/, over the %d-file cap", modCount, spec.MaxModules),
		})
	}

	if !terminalFallback.MatchString(body) {
		findings = append(findings, Finding{
			Rule:     "no-terminal-fallback",
			Severity: SeverityWarn,
			File:     "SKILL.md",
			Message:  "body never states a stop-and-report fallback for a step that cannot be satisfied",
		})
	}

	if desc, ok := fm["description"].(string); ok && desc != "" && !triggerLanguage.MatchString(desc) {
		findings = append(findings, Finding{
			Rule:     "description-no-trigger",
			Severity: SeverityWarn,
			File:     "SKILL.md",
			Message:  "description lacks assertive trigger language (\"use when\", \"when the user\")",
		})
	}

	return findings, nil
}

// checkSteps walks the body line by line, flagging hedge words and missing
// postconditions inside numbered steps, and a MUST/NEVER/ALWAYS guardrail
// that appears only before the first numbered step rather than next to the
// step it governs.
func checkSteps(body, frontmatterText string) []Finding {
	var findings []Finding

	bodyStartLine := strings.Count(frontmatterText, "\n")
	bodyLines := strings.Split(body, "\n")

	firstStepIdx := -1
	for i, line := range bodyLines {
		if stepLine.MatchString(line) {
			firstStepIdx = i
			break
		}
	}

	var hasGuardrailBefore, hasGuardrailAtOrAfter bool

	for i, line := range bodyLines {
		fileLine := bodyStartLine + i + 1

		if stepLine.MatchString(line) {
			lowered := strings.ToLower(line)
			for _, h := range hedgeRules {
				if h.pattern.MatchString(lowered) {
					findings = append(findings, Finding{
						Rule:     "hedge-word",
						Severity: SeverityError,
						File:     "SKILL.md",
						Line:     fileLine,
						Message:  fmt.Sprintf("step uses hedge word %q; a step must be an unambiguous action", h.word),
					})
				}
			}
			if !postconditionMarker.MatchString(line) {
				findings = append(findings, Finding{
					Rule:     "step-without-postcondition",
					Severity: SeverityWarn,
					File:     "SKILL.md",
					Line:     fileLine,
					Message:  "numbered step has no verifiable postcondition",
				})
			}
		}

		if guardrailWord.MatchString(line) {
			if firstStepIdx >= 0 && i >= firstStepIdx {
				hasGuardrailAtOrAfter = true
			} else {
				hasGuardrailBefore = true
			}
		}
	}

	if firstStepIdx >= 0 && hasGuardrailBefore && !hasGuardrailAtOrAfter {
		findings = append(findings, Finding{
			Rule:     "global-only-guardrail",
			Severity: SeverityWarn,
			File:     "SKILL.md",
			Message:  "a MUST/NEVER/ALWAYS guardrail appears only in the preamble, not next to the step it governs",
		})
	}

	return findings
}

// checkBudgets enforces the body's line and token budgets and the
// frontmatter's token budget.
func checkBudgets(body, frontmatterText string) []Finding {
	var findings []Finding

	if n := strings.Count(body, "\n"); n > maxBodyLines {
		findings = append(findings, Finding{
			Rule:     "body-too-long",
			Severity: SeverityError,
			File:     "SKILL.md",
			Message:  fmt.Sprintf("body is %d lines, over the %d-line budget", n, maxBodyLines),
		})
	}

	if tok := ApproxTokens(body); tok > maxBodyApproxTokens {
		findings = append(findings, Finding{
			Rule:     "body-token-budget",
			Severity: SeverityError,
			File:     "SKILL.md",
			Message:  fmt.Sprintf("body is approx %d tokens, over the approx %d-token budget", tok, maxBodyApproxTokens),
		})
	}

	if tok := ApproxTokens(frontmatterText); tok > maxMetadataApproxTokens {
		findings = append(findings, Finding{
			Rule:     "metadata-token-budget",
			Severity: SeverityWarn,
			File:     "SKILL.md",
			Message:  fmt.Sprintf("frontmatter is approx %d tokens, over the approx %d-token budget", tok, maxMetadataApproxTokens),
		})
	}

	return findings
}

// countModules counts the regular files bundled under scripts/, references/,
// and assets/ — the module cap applies across all three, not per directory.
func countModules(bundleDir string) (int, error) {
	count := 0
	for _, sub := range []string{"scripts", "references", "assets"} {
		dir := filepath.Join(bundleDir, sub)
		info, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		if !info.IsDir() {
			continue
		}
		err = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !fi.IsDir() {
				count++
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return count, nil
}
