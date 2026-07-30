package contract

import (
	"regexp"
	"strings"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

// bundlePathPattern recognizes a path into one of the three bundle
// directories a generated skill may ship (scripts/, assets/, references/).
// An action naming one of these is assumed to run it, so it compiles to a
// shell matcher.
var bundlePathPattern = regexp.MustCompile(`(scripts|assets|references)/[\w.\-/]+`)

// pathTokenPattern recognizes a bare path-like token — at least one
// "/"-or-"." separated segment following a leading word — so a write/read
// action naming a file can be turned into a file matcher.
var pathTokenPattern = regexp.MustCompile(`[\w][\w\-]*(?:[./][\w\-]+)+`)

// pathScopePattern recognizes a MUST-NOT constraint that scopes writes to (or
// out of) a named path, e.g. "outside out/", "only in tmp/", "within out/".
var pathScopePattern = regexp.MustCompile(`(?i)\b(?:outside|only in|within)\s+([\w./\-]+)`)

var writeKeywords = []string{"write", "save", "output to", "create"}

var readKeywords = []string{"read", "load", "open"}

var networkKeywords = []string{"no network", "must not connect"}

// Compile builds a Contract from a spec.SkillSpec.
//
// Every emitted StepMatch and ForbidMatch is checkable against a normalized
// trajectory.Event. A step or constraint that has no deterministic check is
// demoted to a SemanticRule rather than emitted as a matcher: a matcher
// nothing can ever satisfy would score every run as a step failure.
func Compile(s *spec.SkillSpec) (*Contract, error) {
	c := &Contract{Version: Version}

	lastStepID := ""
	for _, step := range s.Steps {
		if step.Validation {
			c.Checkpoints = append(c.Checkpoints, step.ID)
		}

		match, ok := classifyStep(step.Action)
		if !ok {
			c.Semantic = append(c.Semantic, SemanticRule{ID: step.ID, Text: step.Action})
			continue
		}

		order := Order{Mode: "any"}
		if lastStepID != "" {
			order = Order{Mode: "after", After: []string{lastStepID}}
		}

		c.Steps = append(c.Steps, StepMatch{
			ID:       step.ID,
			Desc:     step.Action,
			Match:    match,
			Order:    order,
			Required: true,
		})
		lastStepID = step.ID
	}

	for _, rule := range s.Constraints {
		if rule.Kind != spec.RuleMustNot {
			// A MUST constraint has no single event whose presence proves it
			// was honored, so it is always judge-scored rather than matched.
			c.Semantic = append(c.Semantic, SemanticRule{ID: rule.ID, Text: rule.Text})
			continue
		}

		match, ok := classifyForbid(rule.Text)
		if !ok {
			c.Semantic = append(c.Semantic, SemanticRule{ID: rule.ID, Text: rule.Text})
			continue
		}

		c.Forbid = append(c.Forbid, ForbidMatch{
			ID:       rule.ID,
			Desc:     rule.Text,
			Match:    match,
			Severity: rule.Severity,
		})
	}

	return c, nil
}

// classifyStep decides whether a step's action compiles to a deterministic
// matcher: a bundle-path action becomes a shell matcher, and a write/read
// action naming a path-like token becomes a file matcher. Anything else
// cannot be checked against a normalized event, so it reports !ok.
func classifyStep(action string) (Matcher, bool) {
	if path := bundlePathPattern.FindString(action); path != "" {
		// QuoteMeta is essential: an unescaped "." in "extract.py" would
		// match any character, so the pattern would also accept "extractXpy".
		return Matcher{Type: trajectory.TypeShell, Pattern: regexp.QuoteMeta(path)}, true
	}

	path := pathTokenPattern.FindString(action)
	if path == "" {
		return Matcher{}, false
	}

	lower := strings.ToLower(action)
	if containsAny(lower, writeKeywords) {
		return Matcher{Type: trajectory.TypeFileWrite, PathGlob: path}, true
	}
	if containsAny(lower, readKeywords) {
		return Matcher{Type: trajectory.TypeFileRead, PathGlob: path}, true
	}
	return Matcher{}, false
}

// classifyForbid decides whether a MUST-NOT constraint's text compiles to a
// deterministic forbid matcher: a path-scope constraint becomes a file-write
// matcher scoped outside the named path, and a network constraint becomes a
// shell matcher for common network tools. Anything else cannot be checked
// against a normalized event, so it reports !ok.
func classifyForbid(text string) (Matcher, bool) {
	if m := pathScopePattern.FindStringSubmatch(text); m != nil {
		path := strings.TrimRight(m[1], "./")
		if path != "" {
			return Matcher{Type: trajectory.TypeFileWrite, PathNotGlob: path + "/**"}, true
		}
	}

	lower := strings.ToLower(text)
	if containsAny(lower, networkKeywords) {
		return Matcher{Type: trajectory.TypeShell, Pattern: `curl|wget|nc`}, true
	}

	return Matcher{}, false
}

func containsAny(s string, substrs []string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
