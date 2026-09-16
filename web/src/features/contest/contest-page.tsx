import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { getContest } from "@/api/sdk.gen";
import type { ContestOutput, Pairing } from "@/api/types.gen";
import { Split, SuiteNote, VerdictLine } from "./contest-verdict";

function rate(v: number) {
  return `${Math.round(v * 100)}%`;
}

function PairingTable({ pairing }: { pairing: Pairing }) {
  const tasks = pairing.tasks ?? [];
  return (
    <div className="mb-8">
      <div className="flex items-baseline justify-between mb-2 flex-wrap gap-2">
        <h3 className="text-sm font-medium text-text-primary font-mono">
          {pairing.a} vs {pairing.b}
        </h3>
        <Split pairing={pairing} />
      </div>
      {/* Scrolls rather than truncates: four columns of labels do not fit a
          phone, and a clipped table hides the column that says who won. */}
      <div className="border border-border rounded-lg overflow-x-auto">
        <table className="w-full min-w-[520px] text-[12px]">
          <thead>
            <tr className="text-[10px] text-text-tertiary uppercase tracking-[0.08em] border-b border-border">
              <th className="text-left font-normal px-3 py-2">Task</th>
              <th className="text-right font-normal px-3 py-2 font-mono">{pairing.a}</th>
              <th className="text-right font-normal px-3 py-2 font-mono">{pairing.b}</th>
              <th className="text-right font-normal px-3 py-2">Won by</th>
            </tr>
          </thead>
          <tbody>
            {tasks.map((t) => (
              <tr key={t.task_id} className="border-b border-border last:border-0">
                <td className="px-3 py-2 font-mono text-text-secondary whitespace-nowrap">{t.task_id}</td>
                <td className="px-3 py-2 text-right font-mono">{rate(t.a_rate)}</td>
                <td className="px-3 py-2 text-right font-mono">{rate(t.b_rate)}</td>
                <td className="px-3 py-2 text-right">
                  {t.winner ? (
                    <span className="font-mono text-accent">{t.winner}</span>
                  ) : (
                    <span className="text-text-tertiary">tie</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {tasks.length === 0 && (
        <p className="text-[11px] text-text-tertiary mt-2">No task was run for this pair.</p>
      )}
    </div>
  );
}

function Losses({ contest }: { contest: ContestOutput }) {
  const losses = contest.losses ?? [];
  if (losses.length === 0) return null;

  const byLabel = new Map<string, typeof losses>();
  for (const l of losses) {
    byLabel.set(l.label, [...(byLabel.get(l.label) ?? []), l]);
  }

  return (
    <div className="mb-8">
      <h3 className="text-sm font-medium text-text-primary mb-1">What each candidate missed</h3>
      <p className="text-[11px] text-text-tertiary mb-3">
        The expectations a candidate failed, with the judge's own words. This is what to fix.
      </p>
      {[...byLabel.entries()].map(([label, items]) => (
        <div key={label} className="mb-4">
          <div className="font-mono text-[12px] text-text-primary mb-1">{label}</div>
          {items.map((l) => (
            <div key={`${label}-${l.task_id}`} className="pl-3 border-l border-border mb-2">
              <div className="text-[11px] text-text-tertiary">task {l.task_id}</div>
              <ul className="text-[12px] text-text-secondary">
                {(l.missed ?? []).map((m) => (
                  <li key={m}>— {m}</li>
                ))}
              </ul>
              {l.evidence && (
                <div className="text-[11px] text-text-tertiary mt-0.5 italic">{l.evidence}</div>
              )}
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

export function ContestPage() {
  const { id = "" } = useParams();
  const query = useQuery({
    queryKey: ["contest", id],
    queryFn: async () => {
      const res = await getContest({ path: { id } });
      if (!res.data) throw new Error("Failed to load the contest");
      return res.data;
    },
    // A running contest finishes on its own; poll until it does.
    refetchInterval: (q) => {
      const s = q.state.data?.status;
      return s === "done" || s === "failed" ? false : 5000;
    },
  });

  if (query.isLoading) {
    return <div className="p-8 text-sm text-text-secondary">Loading the contest…</div>;
  }
  if (query.isError || !query.data) {
    return <div className="p-8 text-sm text-danger">Could not load the contest.</div>;
  }

  const contest = query.data;
  const candidates = contest.candidates ?? [];

  return (
    <div className="px-8 py-8 max-w-[1000px]">
      <Link
        to={candidates[0] ? `/skills/${candidates[0].skill}` : "/"}
        className="inline-flex items-center gap-1.5 text-[12px] text-text-secondary hover:text-text-primary mb-6"
      >
        <ArrowLeft className="size-3.5" />
        {candidates[0]?.skill ?? "Skills"}
      </Link>

      <div className="text-[10px] text-text-tertiary uppercase tracking-[0.08em] mb-2">
        {contest.chain_link ? "Chain link" : "Contest"}
      </div>
      <h1 className="text-2xl font-mono text-text-primary mb-2">
        {candidates.map((c) => c.label).join("  vs  ")}
      </h1>
      <div className="text-sm mb-1">
        <VerdictLine contest={contest} />
      </div>
      <SuiteNote contest={contest} />
      <div className="text-[11px] text-text-tertiary mb-8">
        {contest.attempts} attempts per task · {contest.tier} tier
        {contest.requested_by ? ` · requested by ${contest.requested_by}` : ""}
      </div>

      {(contest.pairings ?? []).map((p) => (
        <PairingTable key={`${p.a}-${p.b}`} pairing={p} />
      ))}

      <Losses contest={contest} />

      <p className="text-[11px] text-text-tertiary">
        A contest advises. It releases nothing and holds nothing.
      </p>
    </div>
  );
}
