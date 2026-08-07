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

// stepLine matches a numbered list item. Only step text is binding: prose may
// legitimately hedge, and flagging prose trains users to ignore the linter. A
// numbered item still isn't a step when it falls under a declarative
// heading (see IsDeclarativeSection) — a numbered rule or reference entry is
// read, not executed in order.
var stepLine = regexp.MustCompile(`^\s*\d+\.\s+`)

// sectionHeading matches a level-2 or level-3 markdown heading, captured so
// its text can be checked against IsDeclarativeSection.
var sectionHeading = regexp.MustCompile(`^#{2,3}\s+(.*)$`)

// declarativeHeadingWords names section headings whose numbered lists
// enumerate items rather than instruct a sequence of actions — a list of
// rules or a glossary is not "step 1, step 2". Matched by substring against
// the lowercased heading, so "Rules and constraints" and "Failure modes and
// recovery" both match without every phrasing being listed. This is a
// heuristic on heading wording, not document structure, so a body that puts
// genuine steps under an oddly-named heading can still evade it — that's the
// accepted trade for not over-firing on prose, which is the worse error here.
var declarativeHeadingWords = []string{
	"rule", "constraint", "reference", "note", "example",
	"troubleshoot", "glossary", "failure mode", "failure handling",
	"input", "output", "artifact", "overview", "when to use",
	"recovery", "schema", "format",
}

// IsDeclarativeSection reports whether a heading names a declarative section
// — reference material rather than a procedure. Exported so gen's deterministic
// offload pass (internal/eval/gen) can pick the same candidate sections this
// linter treats as non-steps, rather than a second copy of the heuristic.
func IsDeclarativeSection(heading string) bool {
	lower := strings.ToLower(heading)
	for _, w := range declarativeHeadingWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// declarativeMask marks, per body line, whether that line falls under the
// most recent declarative heading.
func declarativeMask(lines []string) []bool {
	mask := make([]bool, len(lines))
	declarative := false
	for i, line := range lines {
		if m := sectionHeading.FindStringSubmatch(line); m != nil {
			declarative = IsDeclarativeSection(m[1])
		}
		mask[i] = declarative
	}
	return mask
}

// postconditionMarker is how a step declares its verifiable outcome.
var postconditionMarker = regexp.MustCompile(`(?i)postcondition|exits? 0|→|results? in`)

// terminalFallbackPhrase is the four literal stop-and-report phrasings this
// rule originally shipped with.
var terminalFallbackPhrase = regexp.MustCompile(`(?i)(stop and report|halt and report|report state|stop, and report)`)

// terminalFallbackMarker matches a body that labels its own fallback rule,
// e.g. "**Terminal fallback:**", rather than only the four exact phrasings.
var terminalFallbackMarker = regexp.MustCompile(`(?i)terminal fallback`)

// stopWord and reportWord are the loose form of the rule: a stop instruction
// and a report instruction stated in the same paragraph satisfy the concept
// even when neither the exact phrase nor the "terminal fallback" label is
// used.
var stopWord = regexp.MustCompile(`(?i)\bstop\b`)
var reportWord = regexp.MustCompile(`(?i)\breport\b`)

// paragraphSplit divides a body into blank-line-delimited blocks, the unit
// hasTerminalFallback checks stop/report co-occurrence within.
var paragraphSplit = regexp.MustCompile(`\n\s*\n`)

// hasTerminalFallback reports whether the body states a stop-and-report
// fallback, matching the concept rather than only four fixed phrasings.
func hasTerminalFallback(body string) bool {
	if terminalFallbackPhrase.MatchString(body) || terminalFallbackMarker.MatchString(body) {
		return true
	}
	for _, para := range paragraphSplit.Split(body, -1) {
		if stopWord.MatchString(para) && reportWord.MatchString(para) {
			return true
		}
	}
	return false
}

// triggerLanguage is what makes a description fire. Skills under-trigger far
// more often than they over-trigger, so an assertive "use when" is required.
var triggerLanguage = regexp.MustCompile(`(?i)(use when|when the user|use this when|triggers? (on|when))`)

// guardrailWord matches an imperative guardrail keyword. Guardrails belong
// next to the step they govern, not only in a preamble read once and
// forgotten by the time the model reaches step 8.
var guardrailWord = regexp.MustCompile(`\b(MUST NOT|MUST|NEVER|ALWAYS)\b`)

// MaxBodyLines is the line budget for a skill body. Exported so gen's prompts
// and revision passes state the same number this package enforces, instead of
// a separately hardcoded copy that can drift.
const MaxBodyLines = 500

// MaxBodyApproxTokens is the token budget for a skill body: roughly 5000
// tokens, measured against ApproxTokens' bytes/4 approximation rather than an
// exact tokenizer.
const MaxBodyApproxTokens = 5000

// MaxMetadataApproxTokens is the token budget for frontmatter.
const MaxMetadataApproxTokens = 100

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

	capacityCount, refCount, err := countModules(bundleDir)
	if err != nil {
		return nil, err
	}
	if capacityCount > spec.MaxModules {
		findings = append(findings, Finding{
			Rule:     "too-many-modules",
			Severity: SeverityError,
			File:     "SKILL.md",
			Message:  fmt.Sprintf("bundle has %d files across scripts/, assets/, over the %d-file cap", capacityCount, spec.MaxModules),
		})
	}
	if refCount > spec.MaxReferences {
		findings = append(findings, Finding{
			Rule:     "too-many-references",
			Severity: SeverityError,
			File:     "SKILL.md",
			Message:  fmt.Sprintf("bundle has %d files under references/, over the %d-file cap", refCount, spec.MaxReferences),
		})
	}

	if !hasTerminalFallback(body) {
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
	declarative := declarativeMask(bodyLines)

	firstStepIdx := -1
	for i, line := range bodyLines {
		if !declarative[i] && stepLine.MatchString(line) {
			firstStepIdx = i
			break
		}
	}

	var hasGuardrailBefore, hasGuardrailAtOrAfter bool

	for i, line := range bodyLines {
		fileLine := bodyStartLine + i + 1

		if !declarative[i] && stepLine.MatchString(line) {
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

	if n := strings.Count(body, "\n"); n > MaxBodyLines {
		findings = append(findings, Finding{
			Rule:     "body-too-long",
			Severity: SeverityError,
			File:     "SKILL.md",
			Message:  fmt.Sprintf("body is %d lines, over the %d-line budget", n, MaxBodyLines),
		})
	}

	if tok := ApproxTokens(body); tok > MaxBodyApproxTokens {
		findings = append(findings, Finding{
			Rule:     "body-token-budget",
			Severity: SeverityError,
			File:     "SKILL.md",
			Message:  fmt.Sprintf("body is approx %d tokens, over the approx %d-token budget", tok, MaxBodyApproxTokens),
		})
	}

	if tok := ApproxTokens(frontmatterText); tok > MaxMetadataApproxTokens {
		findings = append(findings, Finding{
			Rule:     "metadata-token-budget",
			Severity: SeverityWarn,
			File:     "SKILL.md",
			Message:  fmt.Sprintf("frontmatter is approx %d tokens, over the approx %d-token budget", tok, MaxMetadataApproxTokens),
		})
	}

	return findings
}

// countModules counts the regular files bundled under scripts/+assets/ (the
// MaxModules capacity cap) separately from references/ (the MaxReferences
// cap) — see MaxModules' doc comment for why the two are not the same cap.
func countModules(bundleDir string) (capacity, references int, err error) {
	capacity, err = countFilesUnder(bundleDir, "scripts", "assets")
	if err != nil {
		return 0, 0, err
	}
	references, err = countFilesUnder(bundleDir, "references")
	if err != nil {
		return 0, 0, err
	}
	return capacity, references, nil
}

// countFilesUnder counts regular files under each named bundle-relative
// subdirectory, skipping any that don't exist.
func countFilesUnder(bundleDir string, subs ...string) (int, error) {
	count := 0
	for _, sub := range subs {
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
