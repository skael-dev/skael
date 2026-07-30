# Judge calibration set

`items.yaml` holds thirty labelled pairs used to calibrate the LLM judge
(`internal/eval/score.Judge`). Each item is a task prompt plus two short
transcripts — one from a session with the relevant skill installed, one
without (baseline) — and a human verdict: which one actually did the better
job, or whether they tied.

`whetstone doctor --judge` runs the judge's `Pairwise` protocol over every
item and compares its verdicts to the human labels with Cohen's κ
(`score.Kappa`). κ, not raw percent agreement, because two raters who both
answer "skill" most of the time agree most of the time by construction; κ
subtracts that expected-by-chance agreement, so it measures what the judge
actually adds.

The judge sets 20% of a skill's final effectiveness score. `score.KappaFloor`
is 0.6: below it, the judge is not agreeing with a human often enough to be
trusted as that 20%, and the eval engine demotes Uplift to the deterministic
pass-rate delta instead — recorded on the report as a different measurement
(`uplift_source: passrate-fallback`), never substituted silently.

## Provenance: these labels are author-written

`labeled_by: author` in `items.yaml` means every label was written by the
person who wrote the judge being calibrated against it, not by an
independent rater. That is a weaker claim than an independent label: the
author's mental model of "what should the judge think" and the judge's actual
behavior are correlated by construction — sharing a rubric, a set of
archetypes, and a set of blind spots. A κ against author labels can look good
for reasons that have nothing to do with the judge generalizing to unlabelled
tasks.

`score.CalResult.LabeledBy` carries this provenance through to `doctor
--judge`'s report so a reader always knows which claim they're looking at,
rather than a bare number that reads as more authoritative than it is.

The right fix is an independent second rater relabelling some or all of this
set over time, not treating the author labels as permanent ground truth.

## What's in the set

Thirty items drawn from the three golden-corpus archetypes in
`internal/eval/testdata/corpus/`: `deterministic-transform` (run a fixed
script, validate, summarize), `document-formatter` (apply a style guide via a
template), and `checkpointed-workflow` (multi-stage build/validate/package
with a hard constraint on where output may land). Roughly ten items per
archetype.

Labels are not majority-`skill`:

- **`skill`** (~17 items) — the skill's structured process (a script,
  postcondition checks, a stop-on-failure guardrail) caught something the
  baseline missed, or was simply more reliable at scale.
- **`tie`** (~7 items) — the task was small or simple enough that a careful
  baseline run reached the same validated result as the skill run. These
  exist because a judge that always answers "skill" would look good on a set
  that never contains a genuine tie.
- **`baseline`** (~6 items) — the skill's own structure worked against it:
  either its guardrail stopped it from delivering anything when a human
  baseline would have diagnosed and fixed the actual problem, or its
  template/scaffolding over-constrained a small task (a one-sentence
  announcement, a two-paragraph note) into disproportionate boilerplate. Two
  of these are the over-constraint case the calibration brief specifically
  asks for.

No label exceeds 60% of the set; `TestCalibration_ShipsThirtyUsableItems`
enforces this.

## Relabelling

Disagreeing with a label is a data edit, not a code change:

1. Open `items.yaml`, find the item by `id`.
2. Change its `label:` (and update `note:` to explain why).
3. Re-run `whetstone doctor --judge` to see the new κ.

No Go code needs to change. If you relabel a meaningful fraction of the set,
update `labeled_by` and this README to reflect who did it and how (e.g.
`labeled_by: author+reviewer` with a note on the review process), so the
provenance keeps meaning what it says.
