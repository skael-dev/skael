import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { ArrowLeft, Pencil, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { deleteSet, getSet, resolveSet } from "@/api/sdk.gen";
import type { ExcludedEntry, Resolution, SetCaller, SetEntry } from "@/api/types.gen";
import { isPrivileged, useAuth } from "@/app/auth-provider";
import { Button } from "@/components/ui/button";
import { CopyButton, copyWithToast } from "@/components/ui/copy-button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { RowsSkeleton } from "@/components/ui/rows-skeleton";
import { useListNavigation } from "@/features/keyboard/use-list-navigation";
import { apiErrorMessage, throwOnApiError } from "@/lib/api-error";
import { relativeTime } from "@/lib/relative-time";
import { SetEditor } from "./set-editor";

const EXCLUDED_REASONS: Record<string, string> = {
  skill_not_found: "This skill does not exist. Remove the entry or publish the skill.",
  no_released_version: "This skill has no released version. Clear its hold in the review queue.",
  version_not_found: "This version does not exist. Pin another version.",
  held: "This version is held. Clear the hold in the review queue, or pin another version.",
  rejected: "This version was rejected. Pin another version.",
};

const rowClass =
  "flex flex-wrap items-center gap-x-4 gap-y-1 px-3.5 py-2.5 border-b border-border last:border-b-0";

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <h2 className="text-[10px] font-mono uppercase tracking-[0.08em] text-text-tertiary mb-2">
        {title}
      </h2>
      <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">{children}</div>
    </section>
  );
}

function skillPath(name: string) {
  return `/skills/${name}`;
}

function ResolveSummary({
  resolution,
  refreshing,
  onRefresh,
}: {
  resolution: Resolution;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const excluded = resolution.excluded?.length ?? 0;
  return (
    <div className="flex flex-col gap-2 px-3.5 py-3 border-b border-border">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-[13px] font-medium">
          {resolution.complete ? (
            <span className="text-text-primary">Complete</span>
          ) : (
            <span className="text-warning">
              Incomplete: {excluded} {excluded === 1 ? "entry" : "entries"} excluded
            </span>
          )}
        </span>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            className="border-border text-text-secondary"
            disabled={refreshing}
            onClick={onRefresh}
          >
            <RefreshCw className="size-3" />
            Resolve again
          </Button>
          <CopyButton text={resolution.id} what="resolution id" />
        </div>
      </div>
      <code className="font-mono text-[11px] text-text-tertiary break-all">{resolution.id}</code>
    </div>
  );
}

export function SetDetailPage() {
  const { name = "" } = useParams();
  const { user } = useAuth();
  const canEdit = isPrivileged(user);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const editing = canEdit && params.get("edit") === "1";
  const [confirmDelete, setConfirmDelete] = useState(false);

  const setQuery = useQuery({
    queryKey: ["sets", name],
    queryFn: async () => {
      const res = await getSet({ path: { name } });
      if (res.error !== undefined) {
        throw new Error(
          apiErrorMessage("load the set", res, { 404: "Open the list of sets and pick another." }),
        );
      }
      return res.data!;
    },
  });

  const resolveQuery = useQuery({
    queryKey: ["sets", name, "resolve"],
    staleTime: 0,
    queryFn: async () => {
      const res = await resolveSet({ path: { name }, query: { include_excluded: true } });
      if (res.error !== undefined) throw new Error(apiErrorMessage("resolve the set", res));
      return res.data!;
    },
  });

  const remove = useMutation({
    mutationFn: async () => {
      throwOnApiError("delete the set", await deleteSet({ path: { name } }));
    },
    onSuccess: () => {
      toast.success(`Deleted set ${name}`);
      queryClient.removeQueries({ queryKey: ["sets", name] });
      queryClient.invalidateQueries({ queryKey: ["sets"] });
      navigate("/sets");
    },
    onError: (err) => toast.error(err.message),
  });

  const skillRow = useListNavigation<string>((skill) => navigate(skillPath(skill)));
  const callerRow = useListNavigation<SetCaller>((c) =>
    copyWithToast(c.resolution_id, "resolution id"),
  );

  const refresh = async () => {
    const result = await resolveQuery.refetch();
    if (result.isError) toast.error(result.error.message);
    else toast.success("Resolved the set again");
  };

  if (setQuery.isLoading) {
    return (
      <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto">
        <div className="bg-bg-secondary border border-border rounded-lg">
          <RowsSkeleton rows={6} />
        </div>
      </div>
    );
  }
  if (setQuery.isError || !setQuery.data) {
    return (
      <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto">
        <EmptyState
          message={setQuery.error?.message ?? "Could not load the set."}
          action={
            <Link to="/sets" className="text-[13px] text-accent hover:underline">
              Open sets
            </Link>
          }
        />
      </div>
    );
  }

  const set = setQuery.data;
  const entries: SetEntry[] = set.entries ?? [];
  const callers = set.callers ?? [];
  const resolution = resolveQuery.data;
  const excluded: ExcludedEntry[] = resolution?.excluded ?? [];

  return (
    <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto flex flex-col gap-5">
      <Link
        to="/sets"
        className="inline-flex items-center gap-1.5 text-xs text-text-tertiary hover:text-text-primary transition-colors self-start"
      >
        <ArrowLeft className="size-3" />
        Sets
      </Link>

      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-60">
          <h1 className="font-mono text-lg font-medium text-text-primary break-all">{set.name}</h1>
          <p className="text-xs text-text-tertiary mt-1">
            {set.description || "No description."}
          </p>
        </div>
        {canEdit && !editing && (
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              className="border-border text-text-secondary"
              onClick={() => setParams({ edit: "1" })}
            >
              <Pencil className="size-3" />
              Edit
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="border-border text-text-secondary hover:text-destructive"
              onClick={() => setConfirmDelete(true)}
            >
              <Trash2 className="size-3" />
              Delete
            </Button>
          </div>
        )}
      </div>

      {editing ? (
        <SetEditor
          initial={set}
          onSaved={() => setParams({})}
          onCancel={() => setParams({})}
        />
      ) : (
        <Section title="Entries">
          {entries.length === 0 ? (
            <EmptyState
              message={
                canEdit
                  ? "No entries yet. Edit the set to add a skill."
                  : "No entries yet. Ask an owner or an admin to add a skill."
              }
            />
          ) : (
            entries.map((entry) => (
              <div key={entry.name} {...skillRow(entry.name)} className={rowClass}>
                <Link
                  to={skillPath(entry.name)}
                  className="flex-1 min-w-0 truncate font-mono text-[13px] text-text-primary hover:text-accent transition-colors"
                >
                  {entry.name}
                </Link>
                <span className="font-mono text-[11px] text-text-secondary">
                  {entry.version ? `pinned v${entry.version}` : "latest"}
                </span>
              </div>
            ))
          )}
        </Section>
      )}

      <Section title="Resolves to">
        {resolveQuery.isLoading ? (
          <RowsSkeleton rows={3} />
        ) : resolveQuery.isError || !resolution ? (
          <EmptyState
            message={resolveQuery.error?.message ?? "Could not resolve the set."}
            action={
              <button
                onClick={refresh}
                className="text-[13px] text-accent hover:underline cursor-pointer"
              >
                Retry
              </button>
            }
          />
        ) : (
          <>
            <ResolveSummary
              resolution={resolution}
              refreshing={resolveQuery.isFetching}
              onRefresh={refresh}
            />
            {(resolution.entries ?? []).map((entry) => (
              <div key={entry.name} {...skillRow(entry.name)} className={rowClass}>
                <span className="flex-1 basis-full sm:basis-0 min-w-0 truncate font-mono text-[13px] text-text-primary">
                  {entry.name}
                </span>
                <span className="font-mono text-[11px] text-text-secondary">v{entry.version}</span>
                <span className="font-mono text-[11px] text-text-tertiary" title={entry.sha256}>
                  {entry.sha256.slice(0, 12)}
                </span>
                <span className="font-mono text-[11px] text-text-secondary">
                  scan {entry.scan_verdict}
                </span>
                <span className="font-mono text-[11px] text-text-secondary">
                  {entry.approval_state.replace("_", " ")}
                </span>
              </div>
            ))}
            {excluded.map((entry) => (
              <div
                key={entry.name}
                {...skillRow(entry.name)}
                data-testid="excluded-entry"
                className={rowClass}
              >
                <span className="flex-1 basis-full sm:basis-0 min-w-0 truncate font-mono text-[13px] text-warning">
                  {entry.name}
                </span>
                <span className="font-mono text-[11px] text-text-secondary">
                  {/^\d+$/.test(entry.requested) ? `pinned v${entry.requested}` : entry.requested}
                </span>
                <span className="basis-full text-xs text-text-secondary">
                  {EXCLUDED_REASONS[entry.reason] ?? entry.reason}{" "}
                  {(entry.reason === "held" || entry.reason === "no_released_version") && (
                    <Link to="/review" className="text-accent hover:underline">
                      Open review queue
                    </Link>
                  )}
                </span>
              </div>
            ))}
          </>
        )}
      </Section>

      <Section title="Callers">
        {callers.length === 0 ? (
          <EmptyState message="No callers yet. Give a service account a key with sets:read." />
        ) : (
          callers.map((c) => (
            <div key={c.name} {...callerRow(c)} className={rowClass}>
              <span className="flex-1 min-w-0 truncate font-mono text-[13px] text-text-primary">
                {c.name}
              </span>
              <span className="font-mono text-[11px] text-text-tertiary" title={c.resolution_id}>
                {c.resolution_id.slice(0, 12)}
              </span>
              <span className="text-[11px] text-text-tertiary">{relativeTime(c.last_seen_at)}</span>
            </div>
          ))
        )}
      </Section>

      <Dialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
          <DialogHeader className="text-left">
            <DialogTitle className="text-text-primary">Delete this set?</DialogTitle>
            <DialogDescription className="text-text-secondary">
              A key limited to <span className="font-mono text-text-primary">{set.name}</span> gets
              403 after this, and any consumer that resolves it stops working.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              className="border-border text-text-secondary"
              onClick={() => setConfirmDelete(false)}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              disabled={remove.isPending}
              onClick={() => remove.mutate()}
            >
              {remove.isPending ? "Deleting..." : "Delete set"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
