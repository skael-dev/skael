import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ShieldOff, Users } from "lucide-react";
import { getSkillOwners } from "@/api/sdk.gen";

// Read-only projection of GET /api/skills/{name}/owners (Task 11) onto the
// skill detail page. Management (creating/editing rules) lives on the
// Settings → Ownership page (Task 18) — this card only answers "who owns
// this skill, and why", with an affordance straight to that page when the
// answer is "no one".
export function OwnersCard({ skillName }: { skillName: string }) {
  const query = useQuery({
    queryKey: ["skill-owners", skillName],
    queryFn: async () => {
      const res = await getSkillOwners({ path: { name: skillName } });
      if (res.error) throw res.error;
      return res.data;
    },
    enabled: !!skillName,
  });

  const owners = query.data?.owners ?? [];
  const unowned = query.data?.unowned ?? false;
  const rulePattern = query.data?.rule_pattern;

  return (
    <div className="mt-6 max-w-[640px]">
      <div className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary mb-4">
        Owners
      </div>

      <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden px-3.5 py-3">
        {query.isLoading ? (
          <div className="text-[13px] text-text-tertiary">Loading…</div>
        ) : query.isError ? (
          <div className="text-[13px] text-danger">Couldn't load owners</div>
        ) : unowned ? (
          <div className="flex items-center justify-between gap-3 flex-wrap">
            <div className="flex items-center gap-2 text-[13px] text-text-secondary">
              <ShieldOff className="size-4 text-text-tertiary shrink-0" />
              No owners
            </div>
            <Link to="/settings/ownership" className="text-xs text-accent hover:underline shrink-0">
              Assign an owner
            </Link>
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            <div className="flex flex-wrap gap-2">
              {owners.length === 0 ? (
                <span className="text-[13px] text-text-tertiary italic">
                  Owned, but membership couldn't be resolved
                </span>
              ) : (
                owners.map((o) => (
                  <span
                    key={o.id}
                    title={o.email}
                    className="flex items-center gap-1.5 text-[13px] text-text-primary bg-bg-tertiary px-2 py-1 rounded"
                  >
                    <Users className="size-3 text-text-tertiary shrink-0" />
                    {o.name}
                  </span>
                ))
              )}
            </div>
            {rulePattern && (
              <div className="text-[11px] text-text-tertiary">
                via{" "}
                <span className="font-mono text-text-secondary">{rulePattern}</span>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
