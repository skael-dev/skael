package spec

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/skael-dev/skael/internal/eval/llm"
)

// maxBundleBytes bounds how much of a bundle reaches the prompt. Every byte
// of skill content competes with the instructions for attention, and a
// reference-heavy bundle can run to hundreds of kilobytes.
const maxBundleBytes = 60_000

// maxFileBytes bounds any single file's contribution, so one long reference
// cannot consume the whole budget and hide the scripts behind it.
const maxFileBytes = 12_000

const recoverPrompt = `Below is an agent skill that already exists. Produce the
specification that DESCRIBES it.

You are not designing a skill. You are recovering the intent of one that was
already written, so that it can be evaluated. Requirements:

- Every step must correspond to something the skill actually instructs. Do not
  add steps it would benefit from; a step it does not describe makes the
  evaluation measure the wrong thing.
- Every step still needs a mechanically verifiable postcondition. Where the
  skill states an outcome, use it. Where it does not, state the closest
  observable result of what it does instruct.
- Constraints must be MUST or MUST-NOT statements the skill's own text makes.
  If it states none, return no constraints rather than inventing plausible ones.
- Triggers: at least eight positive prompts drawn from what the skill says it
  is for, and at least eight hard negatives that are adjacent-domain
  near-misses. An obviously irrelevant negative tests nothing, and too few of
  either leaves the trigger measurement with nothing to measure.
- Deps must be limited to tools the skill actually invokes.

The skill:

%s`

const recoverRepairPrompt = `Here is a draft specification you produced for an
existing skill:

%s

It failed validation. Fix each problem below without inventing behaviour the
skill does not describe:

%s
Return the corrected specification.`

// Recover reconstructs a SkillSpec from a published bundle. It exists because
// a published bundle never carries spec.yaml — lint.Excluded strips it as
// authoring scaffolding — so a skill that did not come through whetstone has
// no IR for the suite generator or contract.Compile to work from.
//
// One gateway call, plus a repair call only when the draft fails validation.
// Interview's unconditional second call is a design critique, which is the
// wrong objective here: fidelity to an existing skill is what matters.
func Recover(ctx context.Context, g llm.Gateway, skillName, bundleDir string) (*SkillSpec, error) {
	rendered, err := renderBundle(bundleDir)
	if err != nil {
		return nil, err
	}

	draft, err := llm.CompleteJSON[SkillSpec](ctx, g, llm.Req{
		Role:       "spec.recover",
		Prompt:     fmt.Sprintf(recoverPrompt, rendered),
		Schema:     []byte(specSchema),
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return nil, err
	}
	draft.Name = skillName

	errs := draft.Validate()
	if len(errs) == 0 {
		return &draft, nil
	}

	var problems strings.Builder
	for _, e := range errs {
		fmt.Fprintf(&problems, "- %s\n", e)
	}
	prior, err := renderYAML(&draft)
	if err != nil {
		return nil, err
	}

	final, err := llm.CompleteJSON[SkillSpec](ctx, g, llm.Req{
		Role:       "spec.recover.repair",
		Prompt:     fmt.Sprintf(recoverRepairPrompt, prior, problems.String()),
		Schema:     []byte(specSchema),
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return nil, err
	}
	final.Name = skillName
	if errs := final.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("spec.Recover: specification still invalid after repair: %v", errs)
	}
	return &final, nil
}

// renderBundle reads SKILL.md plus scripts/ and references/ into one prompt
// section, in a stable order so the same bundle always produces the same
// prompt. assets/ is skipped: it is data the skill operates on, not behaviour
// that describes it.
func renderBundle(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return "", fmt.Errorf("spec.Recover: read SKILL.md: %w", err)
	}

	var b strings.Builder
	b.WriteString("--- SKILL.md ---\n")
	b.Write(truncate(raw))
	b.WriteString("\n")

	var paths []string
	for _, sub := range []string{"scripts", "references"} {
		root := filepath.Join(dir, sub)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return fs.SkipDir
				}
				return err
			}
			if d.Type().IsRegular() {
				paths = append(paths, p)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("spec.Recover: walk %s: %w", sub, err)
		}
	}
	sort.Strings(paths)

	for _, p := range paths {
		if b.Len() >= maxBundleBytes {
			b.WriteString("\n--- (remaining files omitted: bundle exceeds the prompt budget) ---\n")
			break
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("spec.Recover: read %s: %w", p, err)
		}
		rel, _ := filepath.Rel(dir, p)
		fmt.Fprintf(&b, "\n--- %s ---\n", filepath.ToSlash(rel))
		b.Write(truncate(content))
		b.WriteString("\n")
	}
	return b.String(), nil
}

func truncate(b []byte) []byte {
	if len(b) <= maxFileBytes {
		return b
	}
	return append(b[:maxFileBytes:maxFileBytes], []byte("\n… (truncated)")...)
}
