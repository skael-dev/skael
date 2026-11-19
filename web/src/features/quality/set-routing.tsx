import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { getSkillQualitySet } from "@/api/sdk.gen";
import type { QualitySetBody } from "@/api/types.gen";

// The latest eval of this skill that installed a set beside it. The with-set
// results never move the headline, the lift or the gate; they say what the
// neighbours cost the skill.
export function useSetResult(skillName: string | undefined) {
  return useQuery({
    queryKey: ["skill-quality-set", skillName],
    queryFn: async (): Promise<QualitySetBody | null> => {
      const res = await getSkillQualitySet({ path: { name: skillName! } });
      if (!res.data) throw new Error("Failed to load the with-set result");
      return res.data.set ? res.data : null;
    },
    enabled: !!skillName,
  });
}

function pct(v: number) {
  return `${Math.round(v * 100)}%`;
}

// One line per neighbour that took a prompt, for the top of the skill page:
// the owner sees the neighbour by name.
export function SetLossLine({ skillName }: { skillName: string }) {
  const { data } = useSetResult(skillName);
  const set = data?.set;
  if (!set) return null;
  const losses = (set.shares ?? []).filter((s) => s.took > 0).sort((a, b) => b.share - a.share);
  // A tier with no trigger probe measures no share: that is not "keeps".
  const measured = (set.shares ?? []).some((s) => s.of > 0);
  return (
    <div className="text-[12px] text-text-secondary mb-4 max-w-[720px]">
      {!measured ? (
        <span>
          Share of prompts beside set <span className="font-mono">{set.name}</span> not measured: the latest eval
          with a set ran no trigger probe.
        </span>
      ) : losses.length === 0 ? (
        <span>
          Keeps its prompts beside set <span className="font-mono">{set.name}</span>.
        </span>
      ) : (
        losses.map((s) => (
          <div key={s.name}>
            Loses <span className="font-mono text-text-primary">{pct(s.share)}</span> of its prompts to{" "}
            <Link to={`/skills/${s.name}`} className="font-mono text-accent hover:underline">
              {s.name}
            </Link>
            <span className="text-text-tertiary"> · set {set.name}, v{data?.version}</span>
          </div>
        ))
      )}
    </div>
  );
}

// The full with-set result for the quality tab: who took which prompt, and
// which rules the skill keeps alone but loses with the set.
export function SetSection({ skillName }: { skillName: string }) {
  const { data, isError } = useSetResult(skillName);
  if (isError) {
    return <div className="text-sm text-danger mb-6">Could not load the with-set result.</div>;
  }
  const set = data?.set;
  if (!set) return null;
  const shares = set.shares ?? [];
  const rules = set.rules_lost ?? [];
  const probes = set.probes ?? [];

  return (
    <div className="mb-6">
      <h3 className="text-sm font-medium text-text-primary mb-1">
        With set <span className="font-mono">{set.name}</span>
      </h3>
      <p className="text-[11px] text-text-tertiary mb-3 break-words">
        v{data?.version} with {(set.members ?? []).map((m) => `${m.name}@${m.version}`).join(", ")} installed ·
        resolution <span className="font-mono">{set.resolution_id.slice(0, 12)}</span>. These results never change
        the headline, the lift or the gate.
      </p>

      {shares.length > 0 && !shares.some((s) => s.of > 0) && (
        <p className="text-[12px] text-text-secondary mb-4">
          Share of prompts not measured: this tier runs no trigger probe.
        </p>
      )}
      {shares.some((s) => s.of > 0) && (
        <div className="border border-border rounded-lg overflow-x-auto mb-4">
          <table className="w-full min-w-[420px] text-[11px]">
            <thead>
              <tr className="border-b border-border text-text-tertiary text-left">
                <th className="px-3 py-1.5 font-normal">Neighbour</th>
                <th className="px-3 py-1.5 font-normal">Took</th>
                <th className="px-3 py-1.5 font-normal">Fired beside it</th>
                <th className="px-3 py-1.5 font-normal">Share of its prompts</th>
              </tr>
            </thead>
            <tbody>
              {shares.map((s) => (
                <tr key={s.name} className="border-b border-border last:border-b-0">
                  <td className="px-3 py-1.5 font-mono text-text-secondary">{s.name}</td>
                  <td className="px-3 py-1.5 font-mono text-text-primary">
                    {s.took} of {s.of}
                  </td>
                  <td className="px-3 py-1.5 font-mono text-text-secondary">{s.shared}</td>
                  <td className={`px-3 py-1.5 font-mono ${s.took > 0 ? "text-danger" : "text-text-primary"}`}>
                    {pct(s.share)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <h4 className="text-[11px] text-text-tertiary uppercase tracking-[0.08em] mb-1.5">Rules lost with the set</h4>
      {rules.length === 0 ? (
        <p className="text-[12px] text-text-secondary mb-4">
          No expectation or check passes alone and fails with the set.
        </p>
      ) : (
        <ul className="mb-4 space-y-1.5">
          {rules.map((r) => (
            <li key={`${r.task_id}-${r.text}`} className="text-[12px] text-text-secondary pl-3 border-l border-border">
              {/* A check's text already starts with "check:". */}
              <span className="text-text-tertiary">eval {r.task_id} · {r.check ? "" : "expectation: "}</span>
              {r.text}
              <div className="text-[11px] text-text-tertiary font-mono">
                {r.alone_passes}/{r.alone_runs} alone · {r.set_passes}/{r.set_runs} with the set
              </div>
            </li>
          ))}
        </ul>
      )}

      {probes.length > 0 && (
        <>
          <h4 className="text-[11px] text-text-tertiary uppercase tracking-[0.08em] mb-1.5">Which skill took each probe</h4>
          <div className="border border-border rounded-lg overflow-x-auto">
            <table className="w-full min-w-[520px] text-[11px]">
              <thead>
                <tr className="border-b border-border text-text-tertiary text-left">
                  <th className="px-3 py-1.5 font-normal">Prompt</th>
                  <th className="px-3 py-1.5 font-normal">Written for</th>
                  <th className="px-3 py-1.5 font-normal">Fired</th>
                </tr>
              </thead>
              <tbody>
                {probes.map((p, i) => (
                  <tr key={i} className="border-b border-border last:border-b-0 align-top">
                    <td className="px-3 py-1.5 text-text-secondary">{p.prompt}</td>
                    <td className="px-3 py-1.5 font-mono text-text-tertiary whitespace-nowrap">
                      {p.source ? p.source : p.positive ? skillName : "no skill"}
                    </td>
                    <td className="px-3 py-1.5 font-mono text-text-primary">
                      {p.unknown ? (
                        <span className="text-text-tertiary">not measured</span>
                      ) : (p.fired ?? []).length > 0 ? (
                        (p.fired ?? []).join(" + ")
                      ) : (
                        <span className="text-text-tertiary">none</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </div>
  );
}
