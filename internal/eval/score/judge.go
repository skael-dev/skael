// Package score implements the LLM judge protocol: position-swapped pairwise
// comparisons and per-rule semantic scoring, both anchored on a worked rubric
// and both requiring a verbatim evidence quote before a verdict counts.
//
// Agent CLIs expose no temperature, so determinism here is bought with
// protocol rather than sampling parameters: swap the candidates and treat
// disagreement as a tie (position bias is the largest known failure mode of
// pairwise LLM judging), require a quote that actually appears in the
// transcript (a verdict that cannot be checked is not evidence), and spend a
// third call only when the first two are close enough that one sample of a
// nondeterministic process is not enough to trust.
package score

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/skael-dev/skael/internal/eval/contract"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/spec"
)

// DefaultBorderlineMargin is the |margin| threshold below which a third vote
// is spent. Below it the judge is nearly indifferent, which is exactly where
// a single sample is worth least; above it, a third call re-confirms a
// decision already made and spends a session that could measure something
// else.
const DefaultBorderlineMargin = 0.15

// ErrNoEvidence is returned when a verdict carries no evidence quote, or a
// quote that does not appear in either transcript. A verdict without a
// checkable quote is not weak evidence — it is no evidence, and this judge
// sets 20% of the final score.
var ErrNoEvidence = errors.New("score: verdict has no evidence in the transcript")

// Sample is one candidate's run on a task: what it did (Transcript) and what
// it produced (Outputs).
type Sample struct {
	Label      string
	Transcript string
	Outputs    string
}

// Pair is one task run under both conditions: with the skill available, and
// without (baseline).
type Pair struct {
	TaskID   string
	Prompt   string
	Skill    Sample
	Baseline Sample
}

// Verdict is the outcome of a pairwise comparison. Winner is "skill",
// "baseline", or "tie".
type Verdict struct {
	Winner   string
	Margin   float64
	Evidence []string
	Votes    int
	Swapped  bool
	Tie      bool
}

// JudgeOptions configures a Judge.
type JudgeOptions struct {
	Gateway llm.Gateway
	Spec    *spec.SkillSpec
	// BorderlineMargin defaults to DefaultBorderlineMargin when zero.
	BorderlineMargin float64
	// MaxVotes caps how many pairwise calls a single Pairwise verdict may spend.
	// Defaults to 3 when zero. The protocol only ever wants a third call when
	// the first two are agreeing but borderline; setting MaxVotes to 2
	// disables that third call entirely, capping every verdict at the swap.
	MaxVotes int
}

// Judge runs the pairwise and semantic judging protocol against one skill
// spec's rubric.
type Judge struct {
	gateway          llm.Gateway
	spec             *spec.SkillSpec
	borderlineMargin float64
	maxVotes         int
}

// NewJudge validates options and returns a Judge. A Gateway and a Spec are
// both required: the rubric is derived from the spec's purpose, and there is
// nothing to call without a gateway.
func NewJudge(o JudgeOptions) (*Judge, error) {
	if o.Gateway == nil {
		return nil, errors.New("score.NewJudge: Gateway is required")
	}
	if o.Spec == nil {
		return nil, errors.New("score.NewJudge: Spec is required")
	}
	margin := o.BorderlineMargin
	if margin == 0 {
		margin = DefaultBorderlineMargin
	}
	maxVotes := o.MaxVotes
	if maxVotes == 0 {
		maxVotes = 3
	}
	return &Judge{
		gateway:          o.Gateway,
		spec:             o.Spec,
		borderlineMargin: margin,
		maxVotes:         maxVotes,
	}, nil
}

// pairwiseResponse is the model's answer to one pairwisePrompt call, in the
// A/B frame of that call's ordering.
type pairwiseResponse struct {
	Winner   string   `json:"winner"`
	Margin   float64  `json:"margin"`
	Evidence []string `json:"evidence"`
}

// semanticResponse is the model's answer to one semanticPrompt call.
type semanticResponse struct {
	Satisfied  bool     `json:"satisfied"`
	Confidence float64  `json:"confidence"`
	Evidence   []string `json:"evidence"`
}

// resolveWinner maps a call's "A"/"B"/"tie" answer back to "skill"/"baseline"/
// "tie", given which sample was presented as A in that call. This is where a
// swap silently becomes a bug if the mapping is inlined instead of named.
//
// A response that already names a side directly ("skill"/"baseline") rather
// than a position ("A"/"B") is passed through unchanged rather than treated
// as unrecognized — it is an absolute answer, not a position-relative one, so
// there is nothing to resolve. Anything else — including a genuinely
// unrecognized answer — falls back to "tie" rather than being trusted as a
// fabricated winner.
func resolveWinner(letter string, aWas Sample) string {
	other := "baseline"
	if aWas.Label == "baseline" {
		other = "skill"
	}
	switch strings.ToLower(letter) {
	case "a":
		return aWas.Label
	case "b":
		return other
	case "skill", "baseline":
		return strings.ToLower(letter)
	default:
		return "tie"
	}
}

// quoteAppears reports whether quote appears verbatim in transcript, after
// normalizing both sides by collapsing whitespace and lowercasing. Models
// reflow quotes across line breaks and change case; rejecting a genuine quote
// over that would discard sound verdicts.
func quoteAppears(quote, transcript string) bool {
	norm := func(s string) string {
		return strings.ToLower(strings.Join(strings.Fields(s), " "))
	}
	q := norm(quote)
	if q == "" {
		return false
	}
	return strings.Contains(norm(transcript), q)
}

// validateEvidence requires at least one quote, and every quote to appear in
// at least one of the given transcripts.
func validateEvidence(evidence []string, transcripts ...string) error {
	if len(evidence) == 0 {
		return ErrNoEvidence
	}
	for _, q := range evidence {
		found := false
		for _, t := range transcripts {
			if quoteAppears(q, t) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: quote %q not found in transcript", ErrNoEvidence, q)
		}
	}
	return nil
}

// callPairwise runs one pairwise call with the given ordering and validates
// its evidence against both transcripts in the pair (evidence must come from
// one of the two candidates, regardless of which was presented as A).
func (j *Judge) callPairwise(ctx context.Context, p Pair, first, second Sample) (pairwiseResponse, error) {
	prompt := pairwisePrompt(j.spec, p, first, second)
	resp, err := llm.CompleteJSON[pairwiseResponse](ctx, j.gateway, llm.Req{
		Role:       "score.pairwise",
		Prompt:     prompt,
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return pairwiseResponse{}, err
	}
	if err := validateEvidence(resp.Evidence, p.Skill.Transcript, p.Baseline.Transcript); err != nil {
		return pairwiseResponse{}, err
	}
	return resp, nil
}

// Pairwise compares a skill run against a baseline run of the same task,
// following the position-swap-and-borderline-revote protocol described on
// the package doc.
func (j *Judge) Pairwise(ctx context.Context, p Pair) (Verdict, error) {
	r1, err := j.callPairwise(ctx, p, p.Skill, p.Baseline)
	if err != nil {
		return Verdict{}, err
	}
	w1 := resolveWinner(r1.Winner, p.Skill)

	r2, err := j.callPairwise(ctx, p, p.Baseline, p.Skill)
	if err != nil {
		return Verdict{}, err
	}
	w2 := resolveWinner(r2.Winner, p.Baseline)

	evidence := append(append([]string{}, r1.Evidence...), r2.Evidence...)

	if w1 != w2 {
		// The two orderings disagreed: position bias, not a real preference.
		// A coin flip here would silently reward whichever call happened to
		// have the swap in its favor, so it is a tie instead.
		return Verdict{
			Winner:   "tie",
			Evidence: evidence,
			Votes:    2,
			Swapped:  true,
			Tie:      true,
		}, nil
	}

	meanMargin := (r1.Margin + r2.Margin) / 2
	winner := w1
	votes := 2

	if math.Abs(meanMargin) < j.borderlineMargin && j.maxVotes >= 3 {
		r3, err := j.callPairwise(ctx, p, p.Skill, p.Baseline)
		if err != nil {
			return Verdict{}, err
		}
		w3 := resolveWinner(r3.Winner, p.Skill)
		evidence = append(evidence, r3.Evidence...)
		votes = 3
		meanMargin = (r1.Margin + r2.Margin + r3.Margin) / 3

		counts := make(map[string]int)
		counts[w1] += 2
		counts[w3]++
		winner = w1
		for candidate, n := range counts {
			if n > counts[winner] {
				winner = candidate
			}
		}
	}

	return Verdict{
		Winner:   winner,
		Margin:   meanMargin,
		Evidence: evidence,
		Votes:    votes,
		Tie:      winner == "tie",
	}, nil
}

// Semantic scores one sample against one semantic rule: 1.0 if satisfied and
// citable, 0.0 otherwise. An unsupported "yes" — satisfied with no quote that
// actually appears in the transcript — is not evidence of adherence, so it
// scores 0 rather than 1.
func (j *Judge) Semantic(ctx context.Context, r contract.SemanticRule, s Sample) (float64, []string, error) {
	prompt := semanticPrompt(j.spec, r, s)
	resp, err := llm.CompleteJSON[semanticResponse](ctx, j.gateway, llm.Req{
		Role:       "score.semantic",
		Prompt:     prompt,
		ModelClass: llm.ClassStrong,
	})
	if err != nil {
		return 0, nil, err
	}

	if !resp.Satisfied {
		return 0, resp.Evidence, nil
	}
	if err := validateEvidence(resp.Evidence, s.Transcript); err != nil {
		return 0, resp.Evidence, nil
	}
	return 1, resp.Evidence, nil
}
