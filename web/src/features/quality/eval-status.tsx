import { useQuery } from "@tanstack/react-query";
import { AlertTriangle } from "lucide-react";
import { listSkillEvals } from "@/api/sdk.gen";
import type { QualitySummary } from "@/api/types.gen";
import { useRunEval } from "./use-run-eval";

const ACTIVE = new Set(["queued", "running"]);

function elapsed(since: string): string {
  const mins = Math.floor((Date.now() - new Date(since).getTime()) / 60_000);
  if (mins < 1) return "just started";
  if (mins < 60) return `${mins}m elapsed`;
  return `${Math.floor(mins / 60)}h ${mins % 60}m elapsed`;
}

export function EvalStatus({
  skillName,
  quality,
  latestVersion,
}: {
  skillName: string;
  quality?: QualitySummary | null;
  latestVersion: number;
}) {
  const { run, isPending, isError, error } = useRunEval(skillName);
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
  const label = quality == null ? "Run eval" : "Re-run eval";

  return (
    <span className="inline-flex items-center gap-2 text-[11px] text-text-secondary">
      {quality == null && <span>Not scored</span>}
      {quality != null && (
        <span>
          {stale
            ? `Scored on v${quality.version} · current v${latestVersion}`
            : `Scored on v${quality.version}`}
        </span>
      )}
      {lastFailed?.last_error && (
        <span className="text-danger">Last eval failed: {lastFailed.last_error}</span>
      )}
      <button
        onClick={run}
        disabled={isPending}
        className="text-accent hover:underline disabled:opacity-50"
      >
        {isPending ? "Queueing…" : label}
      </button>
      {isError && (
        <span className="text-xs text-danger flex items-center gap-1">
          <AlertTriangle size={12} />
          {error?.message ?? "Failed to queue eval"}
        </span>
      )}
    </span>
  );
}
