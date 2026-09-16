// Package propose turns what a candidate missed in a contest into one change
// to the skill's prose.
//
// It is given the missed expectations and the judge's evidence, and never the
// task prompts, the suite or the workspace. A proposer that cannot read a task
// cannot write a change tailored to one, which is the whole overfitting
// defence — there is no held-out split here, because one proposal validated
// once cannot compound the way an iterating loop does.
package propose

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/skael-dev/skael/internal/eval/llm"
)

// Loss is one task a candidate failed. TaskID identifies it for a reader; the
// prompt sent to the model carries the expectations and the evidence only.
type Loss struct {
	TaskID   string
	Missed   []string
	Evidence string
}

// File is one prose file from the bundle, as it stands.
type File struct {
	// Path is relative to the bundle root, e.g. "SKILL.md".
	Path string
	Body string
}

// Input is one proposal request.
type Input struct {
	Skill string
	// Files are the bundle's prose. A contest compares whole bundles, so a
	// skill often loses a task because a reference is wrong.
	Files []File
	// Losses are every recorded loss of one candidate, addressed together: a
	// skill is prose read as a whole, so five edits to five missed
	// expectations are one rewrite, not five proposals.
	Losses []Loss
	// Description is the spec's description when the skill has a stored spec.
	// Empty when it has none.
	Description string
}

// Change is one file's proposed new contents.
type Change struct {
	Path string `json:"path"`
	Body string `json:"body"`
}

// Result is what the model proposed.
type Result struct {
	Changes []Change `json:"changes"`
	// Description is the spec's new description, empty when unchanged or when
	// the skill has no spec.
	Description string `json:"description"`
	// Rationale is one short paragraph a reader weighs the diff against.
	Rationale string `json:"rationale"`
}

// ErrNoLosses reports a candidate with nothing to work from. Inventing an
// improvement with no evidence is the edit this command exists to replace.
var ErrNoLosses = fmt.Errorf("propose: this candidate recorded no loss, so there is nothing to work from")

const schema = `{
  "type": "object",
  "required": ["changes", "rationale"],
  "properties": {
    "changes": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["path", "body"],
        "properties": {
          "path": {"type": "string"},
          "body": {"type": "string"}
        }
      }
    },
    "description": {"type": "string"},
    "rationale": {"type": "string"}
  }
}`

// Run asks for one change addressing every loss.
func Run(ctx context.Context, gw llm.Gateway, in Input) (*Result, error) {
	if gw == nil {
		return nil, fmt.Errorf("propose: needs an LLM gateway")
	}
	if len(in.Losses) == 0 {
		return nil, ErrNoLosses
	}
	if len(in.Files) == 0 {
		return nil, fmt.Errorf("propose: %s has no prose to change", in.Skill)
	}

	res, _, err := llm.CompleteJSON[Result](ctx, gw, llm.Req{
		Role:       "propose.change",
		Prompt:     Prompt(in),
		Schema:     []byte(schema),
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return nil, err
	}
	res.Changes = keepKnownFiles(res.Changes, in.Files)
	if len(res.Changes) == 0 && res.Description == "" {
		return nil, fmt.Errorf("propose: the model changed nothing")
	}
	return &res, nil
}

// keepKnownFiles drops any path the bundle does not already have. A proposal
// never adds a file: that changes the bundle's shape, and a reader comparing
// two prose files is not reviewing a path they never asked for.
func keepKnownFiles(changes []Change, files []File) []Change {
	known := map[string]bool{}
	for _, f := range files {
		known[f.Path] = true
	}
	var out []Change
	for _, c := range changes {
		if known[c.Path] {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Prompt is exported so a test can assert what the model is told — above all,
// that no task prompt reaches it.
func Prompt(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, `A skill called %q was measured against another skill on the same tasks, and it
failed some of them. Rewrite its instructions so it would pass.

These are the expectations it failed, with the grader's note on each. You are
not shown the tasks themselves: write a change that fixes the shortcoming in
general, not one that answers a particular case.

`, in.Skill)

	for _, l := range in.Losses {
		for _, m := range l.Missed {
			fmt.Fprintf(&b, "- %s\n", m)
		}
		if l.Evidence != "" {
			fmt.Fprintf(&b, "  what the grader saw: %s\n", l.Evidence)
		}
	}

	b.WriteString("\nThese are the skill's files as they stand.\n")
	for _, f := range in.Files {
		fmt.Fprintf(&b, "\n=== %s ===\n%s\n", f.Path, f.Body)
	}

	b.WriteString(`
Return the full new contents of every file you change, and only files listed
above — you may not add a file, delete one, or write anything that is not
prose. Change as little as possible: an author reads this as a diff.

`)
	if in.Description != "" {
		fmt.Fprintf(&b, `The skill's stored description is %q. If your change makes it wrong, return a
new one in "description"; otherwise leave that field out.

`, in.Description)
	}
	b.WriteString(`Also return "rationale": one short paragraph saying what you changed and which
failed expectation it addresses.

Reply with JSON only:

`)
	if in.Description != "" {
		b.WriteString(`{"changes": [{"path": "...", "body": "..."}], "description": "...", "rationale": "..."}` + "\n")
	} else {
		b.WriteString(`{"changes": [{"path": "...", "body": "..."}], "rationale": "..."}` + "\n")
	}
	return b.String()
}
