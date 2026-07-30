package repair

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// MaxRewriteFraction bounds how much of a file one proposal may replace.
//
// A proposal that rewrites most of a file is not a repair: the score it produces
// cannot be attributed to any particular change, and nobody can review the diff
// against the failure it claims to fix. Constraining it in code rather than
// asking the model nicely is the difference between a rule and a preference.
const MaxRewriteFraction = 0.5

// ErrInadmissible is returned for a proposal the loop refuses to apply.
var ErrInadmissible = errors.New("repair: inadmissible proposal")

// Proposal is one model-authored edit to a bundle file.
//
// Before/After are literal text, not a line range or a patch format, so
// Admissible can verify Before is verbatim in the file without trusting any
// position the model reports.
type Proposal struct {
	File      string `json:"file"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Rationale string `json:"rationale"`
	Deletion  bool   `json:"deletion,omitempty"`
}

// ProposeInput carries everything one repair iteration's gateway call needs.
type ProposeInput struct {
	Spec          *spec.SkillSpec
	BundleDir     string
	Clusters      []FailureCluster
	RobustnessGap float64
	AllowDeletion bool
}

type proposeResponse struct {
	Proposals []Proposal `json:"proposals"`
}

const proposeSchema = `{
  "type": "object",
  "properties": {
    "proposals": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "file": {"type": "string"},
          "before": {"type": "string"},
          "after": {"type": "string"},
          "rationale": {"type": "string"},
          "deletion": {"type": "boolean"}
        },
        "required": ["file", "before", "after", "rationale"]
      }
    }
  },
  "required": ["proposals"]
}`

// Propose asks the gateway for minimal-diff edits that address the given
// failing clusters, then filters the response through Admissible. Only the
// spec, the failing clusters, the robustness gap, and the contents of files
// the clusters implicate are sent — never the whole bundle's worth of
// unrelated context, and never a request framed as "rewrite this file".
func Propose(ctx context.Context, g llm.Gateway, in ProposeInput) ([]Proposal, error) {
	prompt, err := buildPrompt(in)
	if err != nil {
		return nil, fmt.Errorf("repair: propose: %w", err)
	}

	resp, err := llm.CompleteJSON[proposeResponse](ctx, g, llm.Req{
		Role:       "repair.propose",
		Prompt:     prompt,
		Schema:     json.RawMessage(proposeSchema),
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return nil, fmt.Errorf("repair: propose: %w", err)
	}

	out := make([]Proposal, 0, len(resp.Proposals))
	for _, p := range resp.Proposals {
		if p.Deletion && !in.AllowDeletion {
			log.Printf("repair: dropping deletion proposal for %s: deletions are not enabled for this iteration", p.File)
			continue
		}
		if err := Admissible(in.BundleDir, p, in.Clusters); err != nil {
			log.Printf("repair: dropping inadmissible proposal for %s: %v", p.File, err)
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func buildPrompt(in ProposeInput) (string, error) {
	var b strings.Builder

	fmt.Fprintf(&b, "You are repairing an agent skill bundle. Propose the smallest possible edits that address the failing checks below.\n\n")
	if in.Spec != nil {
		fmt.Fprintf(&b, "Skill: %s\nPurpose: %s\n\n", in.Spec.Name, in.Spec.Purpose)
	}

	fmt.Fprintf(&b, "Failing clusters:\n")
	for _, c := range in.Clusters {
		fmt.Fprintf(&b, "- [%s/%s] failed %d times across tasks %v. examples: %v\n", c.Kind, c.ID, c.Count, c.Tasks, c.Examples)
	}
	fmt.Fprintf(&b, "\n")

	if in.RobustnessGap != 0 {
		fmt.Fprintf(&b, "Robustness gap: %.1f. A positive gap means a weaker model needed the skill's "+
			"instructions to succeed where a stronger model did not — the instructions are carrying less "+
			"weight than the model is, which calls for making a step more explicit rather than declaring the "+
			"step itself wrong.\n\n", in.RobustnessGap)
	}

	files, err := implicatedFiles(in)
	if err != nil {
		return "", err
	}
	for _, rel := range files {
		content, err := os.ReadFile(filepath.Join(in.BundleDir, rel))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "--- %s ---\n%s\n\n", rel, string(content))
	}

	fmt.Fprintf(&b, "Rules:\n")
	fmt.Fprintf(&b, "- Every proposal must give a Before string that appears verbatim and exactly once in "+
		"the named file, and an After string to replace it with.\n")
	fmt.Fprintf(&b, "- Do not rewrite a whole file. Propose the smallest edit that fixes the failure.\n")
	if in.AllowDeletion {
		fmt.Fprintf(&b, "- You may propose a deletion (set \"deletion\": true) to remove a rule or step "+
			"entirely rather than rewording it, when the failures suggest over-constraint. A deletion must "+
			"only remove text: After must not add anything not already in Before.\n")
	}
	fmt.Fprintf(&b, "\nReply with JSON matching {\"proposals\": [...]} — no prose, no code fence.\n")

	return b.String(), nil
}

// implicatedFiles returns the bundle-relative files a repair prompt should
// carry the contents of. SKILL.md is always included: every cluster kind
// this package produces — contract, verifier, lint — traces back to the
// instructions or steps it describes.
func implicatedFiles(in ProposeInput) ([]string, error) {
	return []string{"SKILL.md"}, nil
}

// Admissible reports whether a proposal may be applied.
//
// Six checks, each with a failure it prevents:
//
//  1. The file resolves inside the bundle. A model-authored path is untrusted
//     input, and "../" in it is a write outside the skill.
//  2. Before appears verbatim. Fuzzy matching would land an edit somewhere
//     nobody proposed it.
//  3. Before is unique in the file. An anchor matching twice edits whichever
//     occurrence the implementation happens to reach first.
//  4. The replacement is under MaxRewriteFraction of the file.
//  5. Frontmatter is only touched when a cluster concerns triggering. Widening a
//     description raises recall at precision's expense — improving the number by
//     breaking what it measures.
//  6. A deletion only deletes: After must be a subsequence of Before with
//     nothing added. A "deletion" that substitutes text defeats the
//     over-constraint probe it exists to serve.
func Admissible(bundleDir string, p Proposal, clusters []FailureCluster) error {
	if p.File == "" {
		return fmt.Errorf("%w: empty file path", ErrInadmissible)
	}
	target, err := safeJoin(bundleDir, p.File)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInadmissible, err)
	}

	raw, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("%w: reading %s: %v", ErrInadmissible, p.File, err)
	}
	text := string(raw)

	count := strings.Count(text, p.Before)
	if count == 0 {
		return fmt.Errorf("%w: Before in %s must appear verbatim in the file; it was not found", ErrInadmissible, p.File)
	}
	if count > 1 {
		return fmt.Errorf("%w: Before in %s is ambiguous — it matches %d times", ErrInadmissible, p.File, count)
	}

	if len(text) > 0 {
		fraction := float64(len(p.Before)) / float64(len(text))
		if fraction > MaxRewriteFraction {
			return fmt.Errorf("%w: proposal replaces %.0f%% of %s, exceeding MaxRewriteFraction (%.0f%%)",
				ErrInadmissible, fraction*100, p.File, MaxRewriteFraction*100)
		}
	}

	if inFrontmatter(text, p.Before) && !clustersConcernTriggering(clusters) {
		return fmt.Errorf("%w: frontmatter edit in %s is not motivated by any cluster that concerns triggering", ErrInadmissible, p.File)
	}

	if p.Deletion && !isSubsequence(p.After, p.Before) {
		return fmt.Errorf("%w: deletion in %s carries text not present in Before", ErrInadmissible, p.File)
	}

	return nil
}

// inFrontmatter reports whether before falls within the YAML frontmatter
// block delimited by the first two "---" lines.
func inFrontmatter(text, before string) bool {
	parts := strings.SplitN(text, "---", 3)
	if len(parts) < 3 {
		return false
	}
	return strings.Contains(parts[1], before)
}

// clustersConcernTriggering reports whether any cluster's kind or id names a
// triggering concern, which is the only thing that motivates a frontmatter
// edit — the description drives when the skill fires, not what it does.
func clustersConcernTriggering(clusters []FailureCluster) bool {
	for _, c := range clusters {
		if strings.Contains(strings.ToLower(c.Kind+" "+c.ID), "trigger") {
			return true
		}
	}
	return false
}

// isSubsequence reports whether every rune of sub appears in s in order,
// possibly with gaps — the character-level definition of "sub removes text
// from s and adds nothing".
func isSubsequence(sub, s string) bool {
	subR := []rune(sub)
	if len(subR) == 0 {
		return true
	}
	i := 0
	for _, r := range s {
		if subR[i] == r {
			i++
			if i == len(subR) {
				return true
			}
		}
	}
	return false
}

// Apply applies every proposal to the bundle on disk. All proposals are
// staged in memory first and only written once every one of them has been
// validated and applied to its staged content; if any proposal cannot be
// applied, no file is written, so a batch with one bad member leaves the
// bundle untouched rather than half-edited.
func Apply(bundleDir string, ps []Proposal) error {
	staged := make(map[string]string)
	targets := make(map[string]string)

	for _, p := range ps {
		target, err := safeJoin(bundleDir, p.File)
		if err != nil {
			return fmt.Errorf("repair: apply: %w", err)
		}
		targets[p.File] = target

		content, ok := staged[p.File]
		if !ok {
			raw, err := os.ReadFile(target)
			if err != nil {
				return fmt.Errorf("repair: apply: reading %s: %w", p.File, err)
			}
			content = string(raw)
		}

		count := strings.Count(content, p.Before)
		if count != 1 {
			return fmt.Errorf("repair: apply: Before in %s must match exactly once, matched %d times", p.File, count)
		}
		staged[p.File] = strings.Replace(content, p.Before, p.After, 1)
	}

	for file, content := range staged {
		if err := os.WriteFile(targets[file], []byte(content), 0o644); err != nil {
			return fmt.Errorf("repair: apply: writing %s: %w", file, err)
		}
	}
	return nil
}

// safeJoin resolves rel inside dir, refusing anything that escapes. A
// proposal's file path comes from the model, so it is untrusted input: an
// absolute path or a traversal must be an error rather than something
// quietly cleaned up.
func safeJoin(dir, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty file path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute file path %q", rel)
	}
	target := filepath.Join(dir, rel)
	within, err := filepath.Rel(dir, target)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", rel, err)
	}
	if within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file path %q escapes the bundle", rel)
	}
	return target, nil
}
