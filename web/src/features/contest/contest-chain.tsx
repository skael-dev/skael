import { useQuery } from "@tanstack/react-query";
import { listSkillContests } from "@/api/sdk.gen";
import { ContestLink, Split, VerdictLine } from "./contest-verdict";

// The chain answers "is this skill improving": a sequence of verdicts between
// consecutive versions, each measured on one day with both bundles present. A
// panel change does not split it, because a link is a comparison and not a
// level.
export function ContestChain({ skillName }: { skillName: string }) {
  const { data, isLoading } = useQuery({
    queryKey: ["skill-contests", skillName],
    queryFn: async () => {
      const res = await listSkillContests({ path: { name: skillName } });
      return res.data?.contests ?? [];
    },
  });

  if (isLoading) return null;

  const contests = data ?? [];
  if (contests.length === 0) {
    return (
      <p className="text-[11px] text-text-tertiary">
        No contest yet. A contest runs candidates side by side and says which won each task.
      </p>
    );
  }

  const chain = contests.filter((c) => c.chain_link);
  const against = contests.filter((c) => !c.chain_link);

  return (
    <div className="space-y-4">
      {chain.length > 0 && (
        <div>
          <h4 className="text-[11px] text-text-tertiary uppercase tracking-[0.08em] mb-1.5">
            Improvement over time
          </h4>
          {chain.map((c) => (
            <Row key={c.id} contest={c} />
          ))}
        </div>
      )}
      {against.length > 0 && (
        <div>
          <h4 className="text-[11px] text-text-tertiary uppercase tracking-[0.08em] mb-1.5">
            Against other skills
          </h4>
          {against.map((c) => (
            <Row key={c.id} contest={c} />
          ))}
        </div>
      )}
    </div>
  );
}

function Row({ contest }: { contest: Parameters<typeof ContestLink>[0]["contest"] }) {
  const labels = (contest.candidates ?? []).map((c) => c.label).join(" vs ");
  const pairing = (contest.pairings ?? [])[0];
  return (
    <ContestLink contest={contest}>
      <div className="flex items-baseline justify-between gap-3 py-1.5 border-b border-border last:border-0 flex-wrap">
        <span className="font-mono text-[12px] text-text-secondary">{labels}</span>
        <span className="flex items-baseline gap-3">
          <span className="text-[12px]">
            <VerdictLine contest={contest} compact />
          </span>
          {pairing && <Split pairing={pairing} />}
        </span>
      </div>
    </ContestLink>
  );
}
