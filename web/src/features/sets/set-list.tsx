import { useQueries, useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { Plus } from "lucide-react";
import { getSet, listSets } from "@/api/sdk.gen";
import type { Set as SkillSet, SetCaller } from "@/api/types.gen";
import { isPrivileged, useAuth } from "@/app/auth-provider";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { RowsSkeleton } from "@/components/ui/rows-skeleton";
import { useListNavigation, type ListRowProps } from "@/features/keyboard/use-list-navigation";
import { apiErrorMessage } from "@/lib/api-error";
import { SetEditor } from "./set-editor";

export function setPath(name: string): string {
  return `/sets/${encodeURIComponent(name)}`;
}

function consumerLabel(callers: SetCaller[] | undefined): string {
  if (!callers) return "";
  if (callers.length === 0) return "no consumers yet";
  return callers.map((c) => c.name).join(", ");
}

function SetRow({
  set,
  callers,
  rowProps,
}: {
  set: SkillSet;
  callers: SetCaller[] | undefined;
  rowProps: ListRowProps;
}) {
  const count = set.entries?.length ?? 0;
  return (
    <Link
      to={setPath(set.name)}
      {...rowProps}
      tabIndex={0}
      className="flex flex-wrap items-center gap-x-4 gap-y-1 px-3.5 py-3 border-b border-border last:border-b-0 hover:bg-bg-tertiary transition-colors"
    >
      <div className="flex-1 basis-full md:basis-0 min-w-0">
        <div className="font-mono text-[13px] font-medium text-text-primary truncate">{set.name}</div>
        {set.description && (
          <div className="text-xs text-text-tertiary truncate mt-0.5">{set.description}</div>
        )}
      </div>
      <span className="font-mono text-[11px] text-text-secondary whitespace-nowrap">
        {count} {count === 1 ? "entry" : "entries"}
      </span>
      <span className="font-mono text-[11px] text-text-tertiary truncate min-w-0 md:max-w-[240px]">
        {consumerLabel(callers)}
      </span>
    </Link>
  );
}

export function SetList() {
  const { user } = useAuth();
  const canEdit = isPrivileged(user);
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const creating = canEdit && params.get("new") === "1";
  const listRow = useListNavigation<string>((name) => navigate(setPath(name)));

  const query = useQuery({
    queryKey: ["sets"],
    queryFn: async () => {
      const res = await listSets();
      if (res.error !== undefined) throw new Error(apiErrorMessage("load sets", res));
      return res.data?.sets ?? [];
    },
  });
  const sets = query.data ?? [];

  // The list route has no callers, and consumers are what make a set safe to
  // change. A failed lookup shows nothing for that row rather than "no consumers".
  const details = useQueries({
    queries: sets.map((s) => ({
      queryKey: ["sets", s.name],
      queryFn: async () => {
        const res = await getSet({ path: { name: s.name } });
        if (res.error !== undefined) throw new Error(apiErrorMessage("load the set", res));
        return res.data!;
      },
    })),
  });

  return (
    <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-60">
          <h1 className="text-lg font-medium text-text-primary">Sets</h1>
          <p className="text-xs text-text-tertiary mt-1">
            A set is a named list of skills. A machine resolves it to exact versions and digests.
          </p>
        </div>
        {canEdit && !creating && (
          <Button
            variant="outline"
            size="sm"
            className="border-border text-text-secondary"
            onClick={() => setParams({ new: "1" })}
          >
            <Plus className="size-3" />
            New set
          </Button>
        )}
      </div>

      {creating && (
        <SetEditor
          onSaved={(name) => navigate(setPath(name))}
          onCancel={() => setParams({})}
        />
      )}

      <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
        {query.isLoading ? (
          <RowsSkeleton />
        ) : query.isError ? (
          <EmptyState
            message={query.error.message}
            action={
              <button
                onClick={() => query.refetch()}
                className="text-[13px] text-accent hover:underline cursor-pointer"
              >
                Retry
              </button>
            }
          />
        ) : sets.length === 0 ? (
          canEdit ? (
            <EmptyState
              message="No sets yet."
              action={
                !creating && (
                  <button
                    onClick={() => setParams({ new: "1" })}
                    className="text-[13px] text-accent hover:underline cursor-pointer"
                  >
                    Create the first set
                  </button>
                )
              }
            />
          ) : (
            <EmptyState message="No sets yet. Ask an owner or an admin to create one." />
          )
        ) : (
          sets.map((set, i) => (
            <SetRow
              key={set.id}
              set={set}
              callers={details[i]?.data?.callers ?? undefined}
              rowProps={listRow(set.name)}
            />
          ))
        )}
      </div>
    </div>
  );
}
