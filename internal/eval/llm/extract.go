package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

const maxQuoted = 400
const quoteHead = 200
const quoteTail = 200

// ExtractJSON pulls a JSON value out of a model response, tolerating code
// fences and surrounding prose. Fails loudly with the raw text quoted so a
// refusal, rate-limit notice, and truncated response stay distinguishable.
func ExtractJSON(raw string) (json.RawMessage, error) {
	s := strings.TrimSpace(raw)

	if fenced, ok := stripFence(s); ok {
		s = fenced
	}

	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return nil, fmt.Errorf("llm.ExtractJSON: no JSON value in response: %s", quote(raw))
	}

	end, ok := matchBalanced(s[start:])
	if !ok {
		return nil, fmt.Errorf("llm.ExtractJSON: unbalanced JSON in response: %s", quote(raw))
	}

	candidate := json.RawMessage(s[start : start+end])
	if !json.Valid(candidate) {
		return nil, fmt.Errorf("llm.ExtractJSON: invalid JSON in response: %s", quote(raw))
	}
	return candidate, nil
}

func stripFence(s string) (string, bool) {
	if !strings.HasPrefix(s, "```") {
		return s, false
	}
	rest := s[3:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.LastIndex(rest, "```"); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest), true
}

// matchBalanced returns the length of the first balanced JSON value at s.
// Brace counting is string- and escape-aware.
func matchBalanced(s string) (int, bool) {
	var depth int
	var inStr, escaped bool

	for i, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inStr:
			escaped = true
		case r == '"':
			inStr = !inStr
		case inStr:
			// ignore
		case r == '{' || r == '[':
			depth++
		case r == '}' || r == ']':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

func quote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxQuoted {
		s = s[:quoteHead] + " …[truncated]… " + s[len(s)-quoteTail:]
	}
	return fmt.Sprintf("%q", s)
}
