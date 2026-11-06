import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ExternalLink, GitBranch, GitPullRequest } from "lucide-react";
import { toast } from "sonner";
import { detachGitSkill, getSkillGit, listSkillChanges } from "@/api/sdk.gen";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { throwOnApiError } from "@/lib/api-error";

export const skillGitKey = (name: string) => ["skill-git", name];

/** Where a git-bound skill lives. A native skill renders nothing. */
export function GitBindingStrip({ name }: { name: string }) {
  const queryClient = useQueryClient();
  const [detaching, setDetaching] = useState(false);
  const query = useQuery({
    queryKey: skillGitKey(name),
    queryFn: async () => {
      const res = await getSkillGit({ path: { name } });
      throwOnApiError("load the skill's git source", res);
      return res.data!;
    },
  });
  const changes = useQuery({
    queryKey: [...skillGitKey(name), "changes"],
    enabled: !!query.data?.bound,
    refetchInterval: 30_000,
    queryFn: async () => {
      const res = await listSkillChanges({ path: { name } });
      throwOnApiError("load the skill's pull requests", res);
      return res.data!.changes ?? [];
    },
  });
  const detach = useMutation({
    mutationFn: async () => throwOnApiError("detach the skill", await detachGitSkill({ path: { name } })),
    onSuccess: () => {
      toast.success(`${name} is native now. Its connection no longer syncs it.`);
      setDetaching(false);
      queryClient.invalidateQueries({ queryKey: skillGitKey(name) });
    },
    onError: (err) => toast.error(err.message),
  });

  const data = query.data;
  if (!data?.bound || !data.binding || !data.connection) return null;
  const { binding, connection } = data;

  return (
    <div className="mb-4 flex flex-col gap-2 max-w-[720px]">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[12px] text-text-secondary">
        <GitBranch className="size-3.5 text-accent shrink-0" />
        <span>Git-bound to</span>
        <Link
          to={`/git/connections/${connection.id}`}
          className="font-mono text-text-primary hover:underline [overflow-wrap:anywhere]"
        >
          {connection.repo_owner}/{connection.repo_name}/{binding.path}
        </Link>
        <span className="font-mono text-text-tertiary">
          {connection.branch}
          {binding.last_commit ? ` @ ${binding.last_commit.slice(0, 7)}` : ""}
        </span>
        <button
          type="button"
          onClick={() => setDetaching(true)}
          className="text-[12px] text-text-tertiary hover:text-text-primary underline cursor-pointer bg-transparent border-none p-0"
        >
          Detach
        </button>
      </div>
      {(changes.data ?? []).slice(0, 5).map((c) => (
        <div key={c.id} className="flex flex-wrap items-center gap-x-2.5 gap-y-1 text-[12px] text-text-secondary">
          <GitPullRequest className="size-3.5 text-text-tertiary shrink-0" />
          <a href={c.pr_url} target="_blank" rel="noreferrer" className="text-text-primary hover:underline inline-flex items-center gap-1">
            PR #{c.pr_number}
            <ExternalLink className="size-3" />
          </a>
          <span
            className={
              c.state === "open" ? "text-accent" : c.state === "merged" ? "text-text-primary" : "text-text-tertiary"
            }
          >
            {c.state === "merged" && c.version ? `merged as v${c.version}` : c.state === "merged" ? "merged, publishing" : c.state}
          </span>
          <span className="text-text-tertiary">requested by {c.requested_by}</span>
        </div>
      ))}
      {binding.removed_from_git_at && (
        <p className="text-[12px] text-warning m-0">
          This skill's folder left the repo. Skael still serves its last version. Delete the skill, or detach it to
          keep it as a native skill.
        </p>
      )}

      <Dialog open={detaching} onOpenChange={setDetaching}>
        <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
          <DialogHeader className="text-left">
            <DialogTitle className="text-text-primary">Detach {name}?</DialogTitle>
            <DialogDescription className="text-text-secondary">
              The skill becomes native and keeps every version. Its connection stops syncing it and does not adopt it
              again. Changes in the repo no longer reach it.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" size="sm" className="border-border text-text-secondary" onClick={() => setDetaching(false)}>
              Cancel
            </Button>
            <Button size="sm" disabled={detach.isPending} onClick={() => detach.mutate()}>
              {detach.isPending ? "Detaching..." : "Detach"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
