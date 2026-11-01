import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { getReviewQueue } from "@/api/sdk.gen";
import { useAuth } from "@/app/auth-provider";
import { EmptyState } from "@/components/ui/empty-state";
import { RowsSkeleton } from "@/components/ui/rows-skeleton";
import { useListNavigation } from "@/features/keyboard/use-list-navigation";
import { HeldVersion } from "./held-version";

// Approve/reject apply to the version as a whole. Any authenticated member
// can read the queue; only owner/admin may decide — enforced server-side,
// mirrored here so a member sees the hold and the disabled reason rather
// than nothing at all.
const DECISION_ROLES = new Set(["owner", "admin"]);

export function ReviewQueue() {
  const { user } = useAuth();
  const canDecide = user != null && DECISION_ROLES.has(user.role);
  const navigate = useNavigate();
  const listRow = useListNavigation<string>((skillName) => navigate(`/skills/${skillName}`));

  const query = useQuery({
    queryKey: ["review-queue"],
    queryFn: async () => (await getReviewQueue()).data,
  });

  const held = query.data?.held ?? [];
  const sorted = [...held].sort(
    (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  );

  return (
    <div className="p-6 max-w-4xl mx-auto flex flex-col gap-4">
      <div>
        <h1 className="text-lg font-medium text-text-primary">Review queue</h1>
        <p className="text-xs text-text-tertiary mt-1">
          Versions held by the publish gate, awaiting a verified evaluation or an
          owner/admin decision.
        </p>
      </div>

      {query.isLoading && (
        <div className="border border-border rounded-lg bg-bg-secondary">
          <RowsSkeleton rows={3} />
        </div>
      )}

      {!query.isLoading && sorted.length === 0 && (
        <EmptyState
          className="py-16"
          message="Nothing awaiting review."
          action={
            <Link to="/" className="text-[13px] text-accent hover:underline">
              Open skills
            </Link>
          }
        />
      )}

      {sorted.map((h) => (
        <div key={`${h.skill_name}-${h.version}`} {...listRow(h.skill_name)} className="rounded-lg">
          <HeldVersion
            held={h}
            canDecide={canDecide}
            decideDisabledReason={canDecide ? undefined : "See the reason above — only an owner or an admin can decide"}
          />
        </div>
      ))}
    </div>
  );
}
