import { useQuery } from "@tanstack/react-query";
import { getSkillQuality, getSkillQualityVersion } from "@/api/sdk.gen";
import { EvalStatus } from "./eval-status";
import { QualityTrend } from "./quality-trend";

// The engine keeps a measurement that was never defined for a run distinct
// from a measured zero, using a nullable field, in several places
// (robustness_gap, judge evidence/kappa). Collapsing that
// distinction here would say "we checked and it was fine" about something
// we never checked at all.
function measurement(
  value: number | null | undefined,
  render: (v: number) => string,
): string {
  return value == null ? "not measured" : render(value);
}

const SEVERITY_ORDER: Record<string, number> = {
  critical: 0,
  major: 1,
  minor: 2,
};

type JudgeEntry = {
  model?: string;
  winner?: string;
  margin?: number;
  evidence?: string[];
  votes?: number;
};

type DriftViolation = {
  rule_id?: string;
  severity?: string;
  message?: string;
};

type TaskEntry = {
  task_id?: string;
  judge?: JudgeEntry[];
  drift?: { violations?: DriftViolation[] }[];
};

type ReportShape = {
  tasks?: TaskEntry[];
  void_tasks?: unknown[];
  // judge_kappa is a *float64 in Go (report.go:135): nil means no judge was
  // calibrated for this run, a different fact from a judge calibrated at
  // κ=0. judge_labeled_by (report.go:136) is the provenance of the labels
  // that κ was computed against.
  judge_kappa?: number | null;
  judge_labeled_by?: string;
};

// Cohen's κ (score.Kappa, internal/eval/score/calibrate.go) is in [-1, 1]
// and can legitimately be negative — worse than chance agreement is a real,
// meaningful outcome, not a clamping bug. Neither formatRate ([0,1] as a
// percentage) nor formatDriftScale (0-100) is right for this: it gets its
// own formatter rather than reusing one because it's nearby, which is
// exactly the mistake that caused the round 1 regression.
function formatKappa(v: number): string {
  return v.toFixed(2);
}

function sortedViolations(report: ReportShape | null | undefined): {
  shown: (DriftViolation & { taskId?: string })[];
  extra: number;
} {
  if (!report?.tasks) return { shown: [], extra: 0 };
  const all: (DriftViolation & { taskId?: string })[] = [];
  for (const task of report.tasks) {
    for (const drift of task.drift ?? []) {
      for (const v of drift.violations ?? []) {
        all.push({ ...v, taskId: task.task_id });
      }
    }
  }
  all.sort(
    (a, b) =>
      (SEVERITY_ORDER[a.severity ?? ""] ?? 99) -
      (SEVERITY_ORDER[b.severity ?? ""] ?? 99),
  );
  return { shown: all.slice(0, 10), extra: Math.max(0, all.length - 10) };
}

// ── panel_matrix: a JSON ARRAY of MemberReport, not an object keyed by
// member (internal/quality/ingest.go marshals `members` directly). ─────────
type PanelMember = {
  agent?: string;
  model?: string;
  class?: string;
};

type PanelMatrixEntry = {
  member?: PanelMember;
  pillars?: Record<string, number>;
  effectiveness?: number;
  drift?: unknown;
  healthy?: boolean;
  detail?: string;
};

function memberLabel(member: PanelMember | undefined, fallback: string): string {
  if (!member) return fallback;
  const parts = [member.agent, member.model].filter(Boolean);
  return parts.length > 0 ? parts.join("/") : fallback;
}

function isPanelMatrixArray(data: unknown): data is PanelMatrixEntry[] {
  return Array.isArray(data);
}

function PanelMatrixTable({ data }: { data: unknown }) {
  if (!isPanelMatrixArray(data)) {
    // Defensive fallback for a shape that doesn't match the real payload —
    // this is the exception path, not the one the server actually sends.
    if (data == null) return null;
    return (
      <div className="mb-6">
        <h3 className="text-sm font-medium text-text-primary mb-2">Model panel matrix</h3>
        <div className="text-[11px] text-text-tertiary">Unexpected panel matrix shape.</div>
      </div>
    );
  }
  if (data.length === 0) return null;
  return (
    <div className="mb-6">
      <h3 className="text-sm font-medium text-text-primary mb-2">Model panel matrix</h3>
      <div className="border border-border rounded-lg overflow-hidden">
        <table className="w-full text-[11px]">
          <thead>
            <tr className="border-b border-border text-text-tertiary text-left">
              <th className="px-3 py-1.5 font-normal">Member</th>
              <th className="px-3 py-1.5 font-normal">Effectiveness</th>
              <th className="px-3 py-1.5 font-normal">Status</th>
            </tr>
          </thead>
          <tbody>
            {data.map((entry, i) => {
              const label = memberLabel(entry.member, `member ${i}`);
              // An unhealthy member contributed nothing to the headline —
              // that is a different fact from a measured low/zero score,
              // and must never be rendered as one.
              const unhealthy = entry.healthy === false;
              return (
                <tr key={label + i} className="border-b border-border last:border-b-0">
                  <td className="px-3 py-1.5 text-text-secondary">{label}</td>
                  <td className="px-3 py-1.5 font-mono text-text-primary">
                    {unhealthy
                      ? "—"
                      : measurement(entry.effectiveness, formatDriftScale)}
                  </td>
                  <td className="px-3 py-1.5 text-text-secondary">
                    {unhealthy ? (
                      <span className="text-danger">
                        Unhealthy{entry.detail ? ` — ${entry.detail}` : ""}
                      </span>
                    ) : (
                      "Healthy"
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// ── pillar_breakdown: object keyed "agent/model" (memberKey, ingest.go:189),
// values are score.Pillars — no JSON tags, so keys are capitalised Go field
// names. These are rates in [0, 1] (score.Pillars.Validate). ──────────────
const PILLAR_LABELS: Record<string, string> = {
  TriggerF1: "Trigger F1",
  Reliability: "Reliability",
  Uplift: "Uplift",
  Efficiency: "Efficiency",
};
const PILLAR_KEYS = ["TriggerF1", "Reliability", "Uplift", "Efficiency"] as const;

function formatRate(v: number): string {
  return `${Math.round(v * 100)}%`;
}

function isPillarsShape(value: unknown): value is Record<string, number> {
  return (
    !!value &&
    typeof value === "object" &&
    PILLAR_KEYS.some((k) => typeof (value as Record<string, unknown>)[k] === "number")
  );
}

function PillarBreakdownTable({ data }: { data: unknown }) {
  if (!data || typeof data !== "object") return null;
  const entries = Object.entries(data as Record<string, unknown>);
  if (entries.length === 0) return null;
  const malformed = entries.some(([, v]) => !isPillarsShape(v));
  return (
    <div className="mb-6">
      <h3 className="text-sm font-medium text-text-primary mb-2">Pillar breakdown</h3>
      {malformed ? (
        <div className="text-[11px] text-text-tertiary">Unexpected pillar breakdown shape.</div>
      ) : (
        <div className="border border-border rounded-lg overflow-hidden">
          <table className="w-full text-[11px]">
            <thead>
              <tr className="border-b border-border text-text-tertiary text-left">
                <th className="px-3 py-1.5 font-normal">Member</th>
                {PILLAR_KEYS.map((k) => (
                  <th key={k} className="px-3 py-1.5 font-normal">
                    {PILLAR_LABELS[k]}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {entries.map(([member, value]) => {
                const pillars = value as Record<string, number>;
                return (
                  <tr key={member} className="border-b border-border last:border-b-0">
                    <td className="px-3 py-1.5 text-text-secondary">{member}</td>
                    {PILLAR_KEYS.map((k) => (
                      <td key={k} className="px-3 py-1.5 font-mono text-text-primary">
                        {measurement(pillars[k], formatRate)}
                      </td>
                    ))}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ── drift_breakdown: object keyed the same way as pillar_breakdown, values
// are drift.Agg — also untagged, so capitalised Go field names. Trap: these
// values are on a 0-100 SCALE, NOT [0,1] — this field has already been run
// through a [0,1] percentage formatter once and shown 87.5 as "8750.0%".
// Never share a formatter with the pillar rates above. ─────────────────────
const DRIFT_KEYS = ["Mean", "Worst", "Sigma", "N"] as const;

function formatDriftScale(v: number): string {
  return v.toFixed(1);
}

// N is a count of runs, not a 0-100 measurement — render it as an integer
// rather than running it through formatDriftScale like Mean/Worst/Sigma.
function formatDriftCount(v: number): string {
  return String(Math.round(v));
}

function isDriftAggShape(value: unknown): value is Record<string, number> {
  return (
    !!value &&
    typeof value === "object" &&
    DRIFT_KEYS.some((k) => typeof (value as Record<string, unknown>)[k] === "number")
  );
}

function DriftBreakdownTable({ data }: { data: unknown }) {
  if (!data || typeof data !== "object") return null;
  const entries = Object.entries(data as Record<string, unknown>);
  if (entries.length === 0) return null;
  const malformed = entries.some(([, v]) => !isDriftAggShape(v));
  return (
    <div className="mb-6">
      <h3 className="text-sm font-medium text-text-primary mb-2">Drift breakdown</h3>
      {malformed ? (
        <div className="text-[11px] text-text-tertiary">Unexpected drift breakdown shape.</div>
      ) : (
        <div className="border border-border rounded-lg overflow-hidden">
          <table className="w-full text-[11px]">
            <thead>
              <tr className="border-b border-border text-text-tertiary text-left">
                <th className="px-3 py-1.5 font-normal">Member</th>
                {DRIFT_KEYS.map((k) => (
                  <th key={k} className="px-3 py-1.5 font-normal">
                    {k}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {entries.map(([member, value]) => {
                const agg = value as Record<string, number>;
                return (
                  <tr key={member} className="border-b border-border last:border-b-0">
                    <td className="px-3 py-1.5 text-text-secondary">{member}</td>
                    {DRIFT_KEYS.map((k) => (
                      <td key={k} className="px-3 py-1.5 font-mono text-text-primary">
                        {measurement(agg[k], k === "N" ? formatDriftCount : formatDriftScale)}
                      </td>
                    ))}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

export function QualityReport({
  skillName,
  latestVersion,
}: {
  skillName: string;
  latestVersion: number;
}) {
  const summaryQuery = useQuery({
    queryKey: ["skill-quality", skillName],
    queryFn: async () => {
      const res = await getSkillQuality({ path: { name: skillName } });
      if (!res.response) throw new Error("Failed to load quality");
      if (res.response.status === 404) {
        const err = new Error("not_scored") as Error & { status: number };
        err.status = 404;
        throw err;
      }
      if (!res.data) throw new Error("Failed to load quality");
      return res.data;
    },
    retry: false,
  });

  const summary = summaryQuery.data;

  const versionQuery = useQuery({
    queryKey: ["skill-quality-version", skillName, summary?.version],
    queryFn: async () => {
      const res = await getSkillQualityVersion({
        path: { name: skillName, version: summary!.version },
      });
      if (!res.data) throw new Error("Failed to load quality version");
      return res.data;
    },
    enabled: summary != null,
  });

  // A 404 here means "never scored", a state — not an error. This is the
  // common case for most skills; it must not be rendered as a broken page.
  const notScored =
    summaryQuery.isError &&
    (summaryQuery.error as Error & { status?: number })?.status === 404;

  if (notScored) {
    return (
      <div className="text-sm text-text-secondary">
        <EvalStatus skillName={skillName} quality={null} latestVersion={latestVersion} />
      </div>
    );
  }

  if (summaryQuery.isLoading) {
    return <div className="text-sm text-text-secondary">Loading quality report…</div>;
  }

  if (summaryQuery.isError || !summary) {
    return (
      <div className="text-sm text-danger">Could not load the quality report.</div>
    );
  }

  const record = versionQuery.data;
  const report = (record?.report ?? null) as ReportShape | null;
  const { shown: violations, extra: extraViolations } = sortedViolations(report);

  return (
    <div>
      <div className="mb-6">
        <div className="flex items-baseline gap-3 flex-wrap">
          <span className="text-3xl font-mono text-text-primary">
            {Math.round(summary.headline_score)}
          </span>
          {/* The headline's confidence interval was removed: it bootstrapped
              the mean of member effectiveness while the headline is the
              minimum, and at the shipped two-member panel it could only ever
              reproduce [min, max]. Historical rows still carry the stored
              values, but rendering them for old versions and nothing for new
              ones would read as a measurement that had gone missing. */}
        </div>
        <div className="mt-2">
          <EvalStatus skillName={skillName} quality={summary} latestVersion={latestVersion} />
        </div>
      </div>

      <QualityTrend skillName={skillName} />

      <PillarBreakdownTable data={summary.pillar_breakdown} />
      <PanelMatrixTable data={summary.panel_matrix} />

      <div className="mb-6">
        <h3 className="text-sm font-medium text-text-primary mb-2">Robustness gap</h3>
        <div className="text-sm text-text-secondary">
          {measurement(summary.robustness_gap, formatDriftScale)}
        </div>
      </div>

      <div className="mb-6">
        <h3 className="text-sm font-medium text-text-primary mb-2">Contract adherence</h3>
        <DriftBreakdownTable data={summary.drift_breakdown} />

        {violations.length > 0 && (
          <div className="border border-border rounded-lg overflow-hidden">
            <table className="w-full text-[11px]">
              <tbody>
                {violations.map((v, i) => (
                  <tr key={i} className="border-b border-border last:border-b-0">
                    <td className="px-3 py-1.5 text-text-secondary">{v.severity ?? "—"}</td>
                    <td className="px-3 py-1.5 text-text-primary">{v.message ?? v.rule_id ?? "—"}</td>
                    <td className="px-3 py-1.5 text-text-tertiary">{v.taskId}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {extraViolations > 0 && (
          <div className="text-[11px] text-text-tertiary mt-1">
            +{extraViolations} more violation{extraViolations === 1 ? "" : "s"} not shown
          </div>
        )}
      </div>

      {versionQuery.isError ? (
        // A failed fetch is not the same fact as a legitimate `report:
        // null` — the version row exists and simply has no report attached
        // is different from "we couldn't ask the server at all".
        <div className="text-sm text-danger mb-6">
          Could not load the detailed report.
        </div>
      ) : report == null ? (
        <div className="text-sm text-text-secondary mb-6">
          Detailed report not available for this score.
        </div>
      ) : (
        <>
          <div className="mb-6">
            <h3 className="text-sm font-medium text-text-primary mb-2">Judge calibration</h3>
            <div className="text-sm text-text-secondary">
              κ = {measurement(report.judge_kappa, formatKappa)}
              {report.judge_labeled_by && (
                <span className="text-text-tertiary"> · labeled by {report.judge_labeled_by}</span>
              )}
            </div>
          </div>

          <div className="mb-6">
            <h3 className="text-sm font-medium text-text-primary mb-2">Judge evidence</h3>
            {(report.tasks ?? []).flatMap((task) =>
              (task.judge ?? []).flatMap((j, ji) =>
                (j.evidence ?? []).map((quote, qi) => (
                  <div
                    key={`${task.task_id}-${ji}-${qi}`}
                    className="text-[11px] text-text-secondary bg-bg-tertiary border border-border rounded px-3 py-2 mb-2"
                  >
                    {quote}
                  </div>
                )),
              ),
            )}
          </div>
        </>
      )}

      <div className="text-[11px] text-text-tertiary">
        Scored v{summary.version} · suite {summary.suite_ref ?? "—"}
      </div>
    </div>
  );
}
