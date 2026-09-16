import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { Swords } from "lucide-react";
import { createContest, listSkills } from "@/api/sdk.gen";
import type { ContestCandidateInput } from "@/api/types.gen";
import { Button } from "@/components/ui/button";

// The two ways a contest starts. Nothing here fires on its own: a contest
// costs a run per candidate, and an automatic one would start an argument with
// the previous version on every publish.
export function ContestActions({
  skillName,
  version,
  releasedVersion,
}: {
  skillName: string;
  version: number;
  releasedVersion: number;
}) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [picking, setPicking] = useState(false);

  const start = useMutation({
    mutationFn: async (candidates: ContestCandidateInput[]) => {
      const res = await createContest({ body: { candidates } });
      if (!res.data) throw new Error("Could not start the contest");
      return res.data;
    },
    onSuccess: (contest) => {
      queryClient.invalidateQueries({ queryKey: ["skill-contests", skillName] });
      navigate(`/contests/${contest.id}`);
    },
  });

  // Only offered on a version that is not the released one: comparing the
  // released version with itself measures nothing.
  const canCompareWithReleased = releasedVersion > 0 && version !== releasedVersion;

  return (
    <div className="flex items-center gap-2 flex-wrap">
      <Button
        variant="secondary"
        onClick={() => setPicking((v) => !v)}
        disabled={start.isPending}
        className="gap-1.5"
      >
        <Swords className="size-3.5" />
        Compare with…
      </Button>

      {canCompareWithReleased && (
        <Button
          variant="secondary"
          disabled={start.isPending}
          onClick={() =>
            start.mutate([
              { skill: skillName, version },
              { skill: skillName, version: releasedVersion },
            ])
          }
        >
          {start.isPending ? "Starting…" : `Compare with v${releasedVersion}`}
        </Button>
      )}

      {picking && (
        <SkillPicker
          exclude={skillName}
          onPick={(other) => {
            setPicking(false);
            start.mutate([
              { skill: skillName, version },
              { skill: other, version: 0 },
            ]);
          }}
        />
      )}

      {start.isError && (
        <span className="text-[11px] text-danger">{(start.error as Error).message}</span>
      )}
    </div>
  );
}

// A contest between two skills is the normal company case: two teams rarely
// converge on one name before they disagree.
function SkillPicker({
  exclude,
  onPick,
}: {
  exclude: string;
  onPick: (skill: string) => void;
}) {
  const [query, setQuery] = useState("");
  const { data } = useQuery({
    queryKey: ["skills-for-contest"],
    queryFn: async () => {
      const res = await listSkills({ query: { limit: 100 } });
      return res.data?.skills ?? [];
    },
  });

  const matches = (data ?? [])
    .filter((s) => s.name !== exclude)
    .filter((s) => s.name.toLowerCase().includes(query.toLowerCase()))
    .slice(0, 8);

  return (
    <div className="w-full mt-2 border border-border rounded-lg bg-bg-secondary p-2">
      <input
        autoFocus
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder="Which skill should it run against?"
        aria-label="Skill to compare with"
        className="w-full bg-transparent border-none outline-none text-[13px] text-text-primary px-1 py-1 placeholder:text-text-tertiary"
      />
      {matches.map((s) => (
        <button
          key={s.name}
          onClick={() => onPick(s.name)}
          className="block w-full text-left px-2 py-1.5 text-[13px] font-mono text-text-secondary hover:bg-bg-tertiary hover:text-text-primary rounded cursor-pointer"
        >
          {s.name}
          <span className="text-text-tertiary"> v{s.latest_version}</span>
        </button>
      ))}
      {matches.length === 0 && (
        <div className="px-2 py-1.5 text-[12px] text-text-tertiary">No other skill matches.</div>
      )}
    </div>
  );
}
