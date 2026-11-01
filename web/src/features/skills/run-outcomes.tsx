import { useQuery } from "@tanstack/react-query";
import { getSkillOutcomes } from "@/api/sdk.gen";
import type { VersionOutcomes } from "@/api/types.gen";
import { EmptyState } from "@/components/ui/empty-state";
import { RowsSkeleton } from "@/components/ui/rows-skeleton";

function Cell({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="flex flex-col gap-0.5 min-w-0">
      <span className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary">{label}</span>
      <span className="text-[13px] text-text-primary tabular-nums">{value}</span>
    </div>
  );
}

function direction(change: number, unit: string, digits: number): string {
  const size = Math.abs(change).toFixed(digits);
  if (Number(size) === 0) return "unchanged";
  return `${size}${unit} ${change > 0 ? "higher" : "lower"}`;
}

function countsLine(v: VersionOutcomes): string {
  return [
    `${v.succeeded} succeeded`,
    `${v.failed} failed`,
    `${v.cancelled} cancelled`,
    `${v.no_outcome} no outcome`,
  ].join(" · ");
}

// The wording states a difference only. These runs are uncontrolled, so no line
// here may say that a version caused the change.
function comparisonLine(v: VersionOutcomes, minRuns: number): string {
  if (v.comparison) {
    const c = v.comparison;
    const rate = direction(c.success_rate_change * 100, " points", 1);
    const repairs = direction(c.mean_repairs_change, "", 2);
    return `Against v${c.previous_version}: success rate ${rate}, mean repairs ${repairs}. This is a difference, not a cause.`;
  }
  if (v.sample_too_small) {
    return `Sample too small to compare. A comparison needs ${minRuns} runs, and this version has ${v.runs}.`;
  }
  return `No comparison. A comparison needs ${minRuns} runs on this version and on the previous released version.`;
}

function VersionRow({ v, minRuns }: { v: VersionOutcomes; minRuns: number }) {
  return (
    <div className="px-3.5 py-3 border-b border-border last:border-b-0">
      <div className="grid grid-cols-[28px_0.7fr_1.1fr_1.1fr] gap-3 items-end">
        <span className="font-mono text-[13px] text-text-primary">v{v.version}</span>
        <Cell label="Sample" value={v.runs} />
        {v.success_rate != null && v.mean_repairs != null && (
          <>
            <Cell label="Success rate" value={`${(v.success_rate * 100).toFixed(1)}%`} />
            <Cell label="Mean repairs" value={v.mean_repairs.toFixed(2)} />
          </>
        )}
      </div>
      <div className="text-[11px] text-text-secondary mt-2 tabular-nums">{countsLine(v)}</div>
      <div className="text-[11px] text-text-tertiary mt-1 leading-relaxed">
        {comparisonLine(v, minRuns)}
      </div>
    </div>
  );
}

export function RunOutcomes({ skillName }: { skillName: string }) {
  const { data, isPending, isError, refetch } = useQuery({
    queryKey: ["skill-outcomes", skillName],
    queryFn: async () => {
      const res = await getSkillOutcomes({ path: { name: skillName } });
      if (res.error) throw res.error;
      return res.data;
    },
  });

  const consumers = data?.consumers ?? [];

  return (
    <section aria-labelledby="run-outcomes-heading" className="min-w-0">
      <h2 id="run-outcomes-heading" className="text-sm font-medium text-text-primary mt-0 mb-1">
        Run outcomes
      </h2>
      <p className="text-[11px] text-text-tertiary mt-0 mb-3 leading-relaxed">
        Consumers report these outcomes about their own runs. Outcomes do not affect the score and
        do not release a held version. The sample counts runs that succeeded or failed.
      </p>

      {isPending ? (
        <div className="border border-border rounded-lg overflow-hidden">
          <RowsSkeleton rows={3} />
        </div>
      ) : isError ? (
        <div className="border border-border rounded-lg">
          <EmptyState
            message="Couldn't load run outcomes. Check that the server is reachable, then retry."
            action={
              <button onClick={() => refetch()} className="text-accent text-[13px] hover:underline">
                Retry
              </button>
            }
          />
        </div>
      ) : consumers.length === 0 ? (
        <div className="border border-border rounded-lg">
          <EmptyState message="No runs loaded this skill. Report them from a service account with POST /api/events/batch and POST /api/outcomes." />
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          {consumers.map((c) => (
            <div key={c.consumer} className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
              <div className="px-3.5 py-2 border-b border-border font-mono text-[13px] text-text-primary break-all">
                {c.consumer}
              </div>
              {(c.versions ?? []).map((v) => (
                <VersionRow key={v.version} v={v} minRuns={data?.min_comparable_runs ?? 20} />
              ))}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
