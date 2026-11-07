import { useQuery } from "@tanstack/react-query";
import { analyticsBreakdown } from "@/api/sdk.gen";
import type { BreakdownRow } from "@/api/types.gen";
import { throwOnApiError } from "@/lib/api-error";

function Rows({ title, rows, empty }: { title: string; rows: BreakdownRow[]; empty: string }) {
  const max = Math.max(1, ...rows.map((r) => r.activations));
  return (
    <div className="bg-bg-secondary border border-border rounded-lg p-4 flex flex-col gap-2 min-w-0">
      <span className="text-[11px] text-text-tertiary uppercase tracking-wider">{title}</span>
      {rows.length === 0 && <span className="text-xs text-text-tertiary">{empty}</span>}
      {rows.slice(0, 8).map((r) => (
        <div key={r.key || r.label} className="flex flex-col gap-1">
          <div className="flex items-baseline justify-between gap-2 text-xs">
            <span className="text-text-primary font-mono truncate">{r.label}</span>
            <span className="text-text-secondary tabular-nums shrink-0">
              {r.activations} · {r.people} {r.people === 1 ? "person" : "people"}
            </span>
          </div>
          <div className="h-1 bg-bg-tertiary rounded">
            <div className="h-1 bg-accent rounded" style={{ width: `${(r.activations / max) * 100}%` }} />
          </div>
        </div>
      ))}
    </div>
  );
}

// UsageBreakdown shows activations by model, harness and person, as far as the
// caller may see. skill narrows it to one skill.
export function UsageBreakdown({ days, skill }: { days: number; skill?: string }) {
  const { data } = useQuery({
    queryKey: ["analytics-breakdown", days, skill ?? ""],
    queryFn: async () => {
      const res = await analyticsBreakdown({ query: { days, ...(skill ? { skill } : {}) } });
      throwOnApiError("load the usage breakdown", res);
      return res.data!;
    },
  });
  if (!data) return null;
  const people = data.by_person;
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-base font-medium text-text-primary m-0 tracking-tight">Who, on which model and harness</h2>
        <span className="text-xs text-text-secondary">You ran {data.mine} {data.mine === 1 ? "activation" : "activations"} in {data.days} days</span>
      </div>
      <div className={`grid grid-cols-1 gap-3 ${people ? "md:grid-cols-3" : "md:grid-cols-2"}`}>
        <Rows title="Model" rows={data.by_model ?? []} empty="No model reported yet. Run skael hook install again to send it." />
        <Rows title="Harness" rows={data.by_harness ?? []} empty="No activations yet." />
        {people && (
          <Rows
            title="Person"
            rows={people}
            empty={data.identity_recorded ? "No activations yet." : "Identity is not recorded. Instance settings → Analytics turns it on."}
          />
        )}
      </div>
      {data.hidden > 0 && (
        <p className="text-[11px] text-text-tertiary m-0">
          {data.hidden} {data.hidden === 1 ? "group" : "groups"} used by fewer than 5 people {data.hidden === 1 ? "is" : "are"} hidden.
        </p>
      )}
    </div>
  );
}
