import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle } from "lucide-react";
import { listSkillEvals } from "@/api/sdk.gen";
import type { QualitySummary } from "@/api/types.gen";
import { useAuth } from "@/app/auth-provider";
import { useRunEval } from "./use-run-eval";
import { TriggerReview } from "./trigger-review";

const ACTIVE = new Set(["queued", "running"]);

// Enqueuing an eval is owner/admin only server-side (rerun-eval is gated on
// u.IsPrivileged() in internal/evalqueue/routes.go) — mirrored here so a
// member sees why the button is disabled rather than a 403 after clicking.
const RUN_EVAL_ROLES = new Set(["owner", "admin"]);
const RUN_EVAL_DISABLED_REASON = "Only an owner or admin can queue an evaluation.";
const REVIEW_SUITE_DISABLED_REASON = "Only an owner or admin can review an eval set.";

// The models the panel may be pinned to. These are the Claude Code CLI's own
// aliases, which is what the panel runs — the same vocabulary
// runner.DefaultPanel() uses, not API model ids. A worker pointed at a gateway
// that namespaces its identifiers serves none of them, and says so: every
// panel member fails its health probe and the job fails with the model named.
// Empty value means "whatever the worker's default panel is", which is the
// only choice that stays comparable with a skill's existing score history.
const PANEL_MODELS = ["opus", "sonnet", "haiku"];

// A held-for-review score that came from a machine-derived suite (no
// authored SKILL.md suite existed) cannot clear a scan hold — see
// internal/skill/release.go. A reviewer looking at a high score needs to
// know that up front, not discover it after approving.
function DerivedSuiteBadge() {
  return (
    <span
      className="inline-flex items-center gap-1 text-[11px] px-1.5 py-0.5 rounded-full border border-accent/40 text-accent bg-accent/10"
      title="This skill had no evaluation suite, so one was generated from its own SKILL.md. Treat the score as a quality signal, not a review approval."
    >
      Derived suite
    </span>
  );
}

// last_error is one column carrying two things when evalqueue.Explain
// recognises the failure (routes.go): a plain-language lead sentence, a
// blank line, then the raw Go error chain. Rendered inline that reads as one
// run-on line, so the raw chain sits behind a <details> toggle instead —
// same disclosure pattern as quality-trend.tsx's "not comparable" scores. An
// unrecognised failure has no "\n\n" (Explain's contract: passed through
// unchanged) and renders as-is, no toggle.
function LastFailedError({ lastError }: { lastError: string }) {
  const sep = lastError.indexOf("\n\n");
  if (sep === -1) {
    return <span className="text-danger">Last eval failed: {lastError}</span>;
  }
  const lead = lastError.slice(0, sep);
  const raw = lastError.slice(sep + 2);
  return (
    <details className="text-danger">
      <summary className="inline cursor-pointer">Last eval failed: {lead}</summary>
      <p className="mt-1 whitespace-pre-wrap text-text-tertiary">{raw}</p>
    </details>
  );
}

function elapsed(since: string): string {
  const mins = Math.floor((Date.now() - new Date(since).getTime()) / 60_000);
  if (mins < 1) return "just started";
  if (mins < 60) return `${mins}m elapsed`;
  return `${Math.floor(mins / 60)}h ${mins % 60}m elapsed`;
}

// QualitySummary itself carries no suite_derived field, but every caller in
// this codebase actually passes a RecordOutput (a superset) through this
// prop — get-skill-quality's wire response is RecordOutput, not
// QualitySummary. Widened here rather than changing the prop's declared
// type wholesale, since QualitySummary is also used by call sites that
// genuinely don't have it.
type QualityWithSuiteDerived = QualitySummary & { suite_derived?: boolean };

export function EvalStatus({
  skillName,
  quality,
  latestVersion,
  hideDerivedBadge,
  suiteRef,
}: {
  skillName: string;
  quality?: QualityWithSuiteDerived | null;
  latestVersion: number;
  // The parent (quality-report.tsx) already renders its own DerivedSuiteBadge
  // next to the headline score. Without this, a suite_derived score shows
  // the pill twice in the same viewport — here again, next to "Scored on
  // vN". Defaults to false/undefined so a standalone EvalStatus (no such
  // parent badge) still shows it.
  hideDerivedBadge?: boolean;
  // The suite ref that produced a derived score. Most EvalStatus callers
  // have no derived suite to review, so this stays optional. A
  // suite_derived score can arrive before its ref is set. That state must
  // not offer a review button with nothing to fetch.
  suiteRef?: string;
}) {
  const { user } = useAuth();
  const canRun = user != null && RUN_EVAL_ROLES.has(user.role);
  const { run, isPending, isError, error } = useRunEval(skillName);
  const [model, setModel] = useState("");
  const [reviewing, setReviewing] = useState(false);
  const models = model ? [model] : undefined;
  const evalsQuery = useQuery({
    queryKey: ["skill-evals", skillName],
    queryFn: async () => (await listSkillEvals({ path: { name: skillName } })).data,
    // Poll only while something is in flight. An eval runs for 45-90 minutes;
    // polling a finished queue forever is pure noise.
    refetchInterval: (q) => {
      const jobs = q.state.data?.jobs ?? [];
      return jobs.some((j) => ACTIVE.has(j.status)) ? 15_000 : false;
    },
  });

  const jobs = evalsQuery.data?.jobs ?? [];
  const active = jobs.find((j) => ACTIVE.has(j.status));

  if (active?.status === "queued") {
    // The wire value queue_position is 0-indexed (0 = front of the queue) —
    // that's the correct wire contract and stays as-is. The DISPLAY is
    // 1-indexed because a human in a queue is first, not zeroth. Do not
    // "fix" this back to the raw value.
    return (
      <span className="text-[11px] text-text-secondary">
        Queued · position {active.queue_position + 1}
      </span>
    );
  }
  if (active?.status === "running") {
    return (
      <span className="text-[11px] text-text-secondary">
        Running · {active.started_at ? elapsed(active.started_at) : "starting"}
      </span>
    );
  }

  const lastFailed = jobs.find((j) => j.status === "failed");
  const stale = quality != null && quality.version < latestVersion;
  const rerun = quality != null;

  return (
    <>
      <span className="inline-flex items-center gap-2 text-[11px] text-text-secondary">
        {quality == null && <span>Not scored</span>}
        {quality != null && (
          <span>
            {stale
              ? `Scored on v${quality.version} · current v${latestVersion}`
              : `Scored on v${quality.version}`}
          </span>
        )}
        {quality?.suite_derived && !hideDerivedBadge && <DerivedSuiteBadge />}
        {/* review-eval-suite is owner/admin only server-side, the same gate
            the run buttons carry. Shown disabled rather than hidden, so a
            member reads why instead of clicking into a 403. */}
        {quality?.suite_derived && suiteRef && (
          <button
            onClick={() => setReviewing((v) => !v)}
            disabled={!canRun}
            title={!canRun ? REVIEW_SUITE_DISABLED_REASON : undefined}
            className="text-accent hover:underline disabled:opacity-50"
          >
            {reviewing ? "Close" : "Review the eval set"}
          </button>
        )}
        {lastFailed?.last_error && <LastFailedError lastError={lastFailed.last_error} />}
        <span className="inline-flex items-center gap-1.5">
          <select
            aria-label="Panel model"
            title="Which model attempts the tasks. A score against a different model is charted as its own series, not appended to the existing trend."
            value={model}
            onChange={(e) => setModel(e.target.value)}
            disabled={!canRun || isPending}
            className="bg-transparent border border-border rounded px-1 py-0.5 text-[11px] disabled:opacity-50"
          >
            <option value="">Default model</option>
            {PANEL_MODELS.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
          <button
            onClick={() => run({ tier: "smoke", models })}
            disabled={!canRun || isPending}
            title={!canRun ? RUN_EVAL_DISABLED_REASON : "5 tasks on one model"}
            className="text-accent hover:underline disabled:opacity-50"
          >
            {isPending ? "Queueing…" : rerun ? "Re-run quick check (smoke)" : "Quick check (smoke)"}
          </button>
          <span className="text-text-tertiary">·</span>
          <button
            onClick={() => run({ models })}
            disabled={!canRun || isPending}
            title={!canRun ? RUN_EVAL_DISABLED_REASON : "the releasable score"}
            className="text-accent hover:underline disabled:opacity-50"
          >
            {isPending ? "Queueing…" : rerun ? "Re-run full evaluation" : "Full evaluation"}
          </button>
        </span>
        {!canRun && <span className="text-text-tertiary">{RUN_EVAL_DISABLED_REASON}</span>}
        {isError && (
          <span className="text-xs text-danger flex items-center gap-1">
            <AlertTriangle size={12} />
            {error?.message ?? "Failed to queue eval"}
          </span>
        )}
      </span>
      {reviewing && canRun && suiteRef && <TriggerReview suiteRef={suiteRef} skillName={skillName} />}
    </>
  );
}
