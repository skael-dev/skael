import { useQuery } from "@tanstack/react-query";
import { ShieldCheck } from "lucide-react";
import { getReviewQueue } from "@/api/sdk.gen";
import { useAuth } from "@/app/auth-provider";
import { HeldVersion } from "./held-version";

// Approve/reject apply to the version as a whole. Any authenticated member
// can read the queue; only owner/admin may decide — enforced server-side,
// mirrored here so a member sees the hold and the disabled reason rather
// than nothing at all.
const DECISION_ROLES = new Set(["owner", "admin"]);

export function ReviewQueue() {
  const { user } = useAuth();
  const canDecide = user != null && DECISION_ROLES.has(user.role);

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

      {query.isLoading && <div className="text-sm text-text-secondary">Loading…</div>}

      {!query.isLoading && sorted.length === 0 && (
        <div className="flex flex-col items-center justify-center py-16 gap-3">
          <ShieldCheck size={28} className="text-accent" />
          <div className="text-sm text-text-secondary">Nothing awaiting review.</div>
        </div>
      )}

      {sorted.map((h) => (
        <HeldVersion
          key={`${h.skill_name}-${h.version}`}
          held={h}
          canDecide={canDecide}
          decideDisabledReason={canDecide ? undefined : "See the reason above — only an owner or an admin can decide"}
        />
      ))}
    </div>
  );
}
