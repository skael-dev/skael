package contract

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

// bundlePathPattern recognizes a path into one of the three bundle
// directories a generated skill may ship (scripts/, assets/, references/).
// An action naming one of these is assumed to run it, so it compiles to a
// shell matcher.
//
// The match must end on a path character (a word character or a dash), not on
// a "." or a "/". An action is ordinary English — "Read references/style-guide.md."
// — and a greedy class containing "." would otherwise swallow the sentence's
// final period, which QuoteMeta then escapes into a pattern requiring a literal
// trailing dot in the observed command. No trajectory can satisfy that, and an
// inert matcher scores every run as a step failure.
var bundlePathPattern = regexp.MustCompile(`(scripts|assets|references)/[\w.\-/]*[\w\-]`)

// pathTokenPattern recognizes a bare path-like token — at least one
// "/"-or-"." separated segment following a leading word — so a write/read
// action naming a file can be turned into a file matcher.
var pathTokenPattern = regexp.MustCompile(`[\w][\w\-]*(?:[./][\w\-]+)+`)

// pathScopePattern recognizes a MUST-NOT constraint that scopes writes to (or
// out of) a named path, e.g. "outside out/", "only in tmp/", "within out/".
var pathScopePattern = regexp.MustCompile(`(?i)\b(?:outside|only in|within)\s+([\w./\-]+)`)

// networkCommandPattern matches an invocation of a common network tool. Each
// name must start a command word and end on a word boundary: unanchored, "nc"
// is a substring of "encode", "sync", "announce", "increment" and "concat", so
// a bare alternation turns routine commands into violations — and a "no
// network" constraint is typically authored at critical severity, the heaviest
// penalty there is. The leading class is the set of characters that can precede
// a command: whitespace, a shell operator, an opening quote, or a "/".
//
// "/" is in the class because a path-qualified invocation is still an
// invocation: "/usr/bin/curl" and "./nc" are what a skill writes when PATH is
// not to be trusted, and without "/" the rule was inert for exactly those. The
// cost is a file whose own name is a tool name — a write to "out/nc" reads as
// an invocation. That direction is deliberate: a false violation gets
// investigated, a missed one looks like a clean run.
const networkCommandPattern = "(?:^|[\\s;|&(<>'\"`/])(?:curl|wget|nc)\\b"

var writeKeywords = []string{"write", "save", "output to", "create"}

var readKeywords = []string{"read", "load", "open"}

var networkKeywords = []string{"no network", "must not connect"}

// Compile builds a Contract from a spec.SkillSpec.
//
// Every emitted StepMatch and ForbidMatch is checkable against a normalized
// trajectory.Event. A step or constraint that has no deterministic check is
// demoted to a SemanticRule rather than emitted as a matcher: a matcher
// nothing can ever satisfy would score every run as a step failure.
//
// A MUST-NOT constraint may become a ForbidMatch; a MUST constraint may
// become a StepMatch when its text names an observable action, reusing the
// same classification as a step (a positive obligation is otherwise just as
// deterministically checkable as a step's action) — both share the ID space
// of an emitted matcher. Every ID Compile emits (across Steps, Forbid, and
// Semantic together) must therefore be unique in the whole compiled
// Contract: a spec whose constraint ID collides with a step ID (or with
// another constraint's ID) is a compile error rather than a silent merge or
// overwrite, since a later report keyed by ID could otherwise attribute a
// violation to the wrong rule.
func Compile(s *spec.SkillSpec) (*Contract, error) {
	c := &Contract{Version: Version}
	usedIDs := make(map[string]bool)

	lastStepID := ""
	for _, step := range s.Steps {
		if step.Validation {
			c.Checkpoints = append(c.Checkpoints, step.ID)
		}
		usedIDs[step.ID] = true

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
		if usedIDs[rule.ID] {
			return nil, fmt.Errorf("contract.Compile: constraint id %q collides with an id already compiled into the contract", rule.ID)
		}
		usedIDs[rule.ID] = true

		if rule.Kind == spec.RuleMustNot {
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
			continue
		}

		// RuleMust: reuse the step classification. An observable positive
		// obligation (names a bundle path, or a write/read plus a path-like
		// token) becomes a required matcher with no ordering claim — it must
		// be observed somewhere in the trajectory, not after any particular
		// step. Anything else has no deterministic check, exactly like an
		// unmatchable step, so it is judge-scored instead.
		match, ok := classifyStep(rule.Text)
		if !ok {
			c.Semantic = append(c.Semantic, SemanticRule{ID: rule.ID, Text: rule.Text})
			continue
		}

		c.Steps = append(c.Steps, StepMatch{
			ID:       rule.ID,
			Desc:     rule.Text,
			Match:    match,
			Order:    Order{Mode: "any"},
			Required: true,
		})
	}

	if err := validatePathPatterns(c); err != nil {
		return nil, err
	}

	return c, nil
}

// probePath is an arbitrary well-formed relative path. MatchPath validates the
// pattern before it looks at the candidate, so any candidate it accepts works
// as a probe; whether it matches is irrelevant, only whether the pattern is
// well-formed.
const probePath = "probe"

// validatePathPatterns runs every emitted path glob through MatchPath, the
// only sanctioned consumer of these patterns, and turns a rejection into a
// compile error.
//
// MatchPath deliberately reports a malformed pattern loudly rather than
// silently never matching, and calls that "a defensive rejection of a compiler
// bug". Without this check the compiler can emit exactly such a pattern (a
// spec scoping writes to "../shared" or "/out" is enough), and the error
// surfaces at scoring time — far from the spec text the author could fix —
// instead of here.
func validatePathPatterns(c *Contract) error {
	check := func(kind, id, pattern string) error {
		if pattern == "" {
			return nil
		}
		if _, err := MatchPath(pattern, probePath); err != nil {
			return fmt.Errorf("contract.Compile: %s %q compiled to an unusable path pattern: %w", kind, id, err)
		}
		return nil
	}
	for _, s := range c.Steps {
		if err := check("step", s.ID, s.Match.PathGlob); err != nil {
			return err
		}
		if err := check("step", s.ID, s.Match.PathNotGlob); err != nil {
			return err
		}
	}
	for _, f := range c.Forbid {
		if err := check("constraint", f.ID, f.Match.PathGlob); err != nil {
			return err
		}
		if err := check("constraint", f.ID, f.Match.PathNotGlob); err != nil {
			return err
		}
	}
	return nil
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
		return Matcher{Type: trajectory.TypeShell, Pattern: networkCommandPattern}, true
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
