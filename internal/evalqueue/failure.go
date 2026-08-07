package evalqueue

import "strings"

// explanation maps a substring of a worker's error chain to a sentence a
// reader can act on. Ordered: the first match wins, so the more specific
// cause is listed before the stage that contains it.
var explanations = []struct {
	match string
	say   string
}{
	{"response truncated at max_tokens",
		"The model's reply was too long to complete. This usually clears on a re-run; if it repeats, the skill's description may be too broad to evaluate."},
	{"too thin to evaluate",
		"This skill's evaluation suite had too few usable tasks to score. See the suite's checks for which tasks were void and why."},
	{"outlining suite",
		"Could not generate an evaluation suite for this skill."},
	{"generate suite",
		"Could not generate an evaluation suite for this skill."},
	{"recover spec",
		"Could not work out what this skill does from its SKILL.md, so no evaluation suite could be built."},
	{"panel health",
		"The evaluation panel could not start. Check the worker's model configuration."},
}

// Explain reduces a worker's error chain to one sentence. The raw chain is
// what the worker reports and what an operator needs; this is what a reader
// sees first. An unrecognised failure passes through unchanged rather than
// becoming a vague placeholder that hides it.
func Explain(raw string) string {
	for _, e := range explanations {
		if strings.Contains(raw, e.match) {
			return e.say
		}
	}
	return raw
}
