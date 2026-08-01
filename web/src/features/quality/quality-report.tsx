import { useQuery } from "@tanstack/react-query";
import { getSkillQuality, getSkillQualityVersion } from "@/api/sdk.gen";
import { EvalStatus } from "./eval-status";

// The engine keeps a measurement that was never defined for a run distinct
// from a measured zero, using a nullable field, in several places
// (robustness_gap, drift_grade, judge evidence/kappa). Collapsing that
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
};

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

function KeyedTable({
  title,
  data,
}: {
  title: string;
  data: unknown;
}) {
  if (!data || typeof data !== "object") return null;
  const entries = Object.entries(data as Record<string, unknown>);
  if (entries.length === 0) return null;
  return (
    <div className="mb-6">
      <h3 className="text-sm font-medium text-text-primary mb-2">{title}</h3>
      <div className="border border-border rounded-lg overflow-hidden">
        <table className="w-full text-[11px]">
          <tbody>
            {entries.map(([member, value]) => (
              <tr key={member} className="border-b border-border last:border-b-0">
                <td className="px-3 py-1.5 text-text-secondary">{member}</td>
                <td className="px-3 py-1.5 font-mono text-text-primary">
                  {typeof value === "number" ? value : JSON.stringify(value)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
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
            {summary.headline_score}
          </span>
          {summary.headline_ci_low != null && summary.headline_ci_high != null && (
            <span className="text-xs text-text-secondary">
              CI {summary.headline_ci_low}–{summary.headline_ci_high}
            </span>
          )}
        </div>
        <div className="mt-2">
          <EvalStatus skillName={skillName} quality={summary} latestVersion={latestVersion} />
        </div>
      </div>

      <KeyedTable title="Pillar breakdown" data={summary.pillar_breakdown} />
      <KeyedTable title="Model panel matrix" data={summary.panel_matrix} />

      <div className="mb-6">
        <h3 className="text-sm font-medium text-text-primary mb-2">Robustness gap</h3>
        <div className="text-sm text-text-secondary">
          {measurement(summary.robustness_gap, (v) => String(v))}
        </div>
      </div>

      <div className="mb-6">
        <h3 className="text-sm font-medium text-text-primary mb-2">Drift</h3>
        <div className="text-sm text-text-secondary mb-2">
          Grade: <span>{summary.drift_grade ?? "not measured"}</span>
        </div>
        <KeyedTable title="Drift breakdown" data={summary.drift_breakdown} />

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

      {report == null ? (
        <div className="text-sm text-text-secondary mb-6">
          Detailed report not available for this score.
        </div>
      ) : (
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
      )}

      <div className="text-[11px] text-text-tertiary">
        Scored v{summary.version} · suite {summary.suite_ref ?? "—"}
      </div>
    </div>
  );
}
