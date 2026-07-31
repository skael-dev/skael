package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// maxQuoted bounds how much raw text an extraction error repeats, split
// between the head and the tail (see quote).
const maxQuoted = 400
const quoteHead = 200
const quoteTail = 200

// ExtractJSON pulls a JSON value out of a model response. Models wrap JSON in
// code fences and prose regardless of instructions, so extraction is tolerant
// by design — but it fails loudly with the raw text quoted, because a refusal,
// a rate-limit notice, and a truncated response are three different problems
// that a bare "no JSON found" would make indistinguishable.
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

// stripFence removes a surrounding ``` or ```json fence.
func stripFence(s string) (string, bool) {
	if !strings.HasPrefix(s, "```") {
		return s, false
	}
	rest := s[3:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:] // drop an optional language tag
	}
	if i := strings.LastIndex(rest, "```"); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest), true
}

// matchBalanced returns the length of the first balanced JSON value at the
// start of s. Brace counting is string- and escape-aware: a brace inside a JSON
// string is not structural, and naive counting truncates any response whose
// content mentions one.
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
			// structural characters inside a string are literal
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

// quote bounds how much raw text an extraction error repeats. It keeps both
// the beginning and the end of long text, joined by an elision marker: a
// refusal or a rate-limit notice is short and front-loaded, so the head is
// enough, but a genuinely truncated response is only diagnosable from where
// generation stopped — its tail — which a head-only quote would discard
// entirely. The result stays bounded regardless of input length, since these
// strings end up in logs.
func quote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxQuoted {
		s = s[:quoteHead] + " …[truncated]… " + s[len(s)-quoteTail:]
	}
	return fmt.Sprintf("%q", s)
}
