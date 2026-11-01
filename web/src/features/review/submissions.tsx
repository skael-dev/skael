import { useRef } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { getSkill, listSubmissions } from "@/api/sdk.gen";
import type { Submission } from "@/api/types.gen";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { RowsSkeleton } from "@/components/ui/rows-skeleton";
import { useListNavigation, type ListRowProps } from "@/features/keyboard/use-list-navigation";
import type { ScanReport } from "@/features/security/scan-findings";
import { apiErrorMessage } from "@/lib/api-error";
import { relativeTime } from "@/lib/relative-time";
import { cn } from "@/lib/utils";
import { SubmissionDetail } from "./submission-detail";

const DOCS_URL = "https://skael.dev/docs/cli/";
export const LIST_LIMIT = 500;

const STATES = [
  { id: "pending", label: "Pending", empty: "No submissions wait for review." },
  { id: "approved", label: "Approved", empty: "No submission is approved." },
  { id: "rejected", label: "Rejected", empty: "No submission is rejected." },
  { id: "all", label: "All", empty: "No agent has sent a submission." },
] as const;

export function useSubmissionList(state: "pending" | "approved" | "rejected" | "all") {
  return useQuery({
    queryKey: ["submissions", state],
    queryFn: async () => {
      const res = await listSubmissions({
        query: { limit: LIST_LIMIT, ...(state === "all" ? {} : { state }) },
      });
      if (res.error !== undefined) throw new Error(apiErrorMessage("load submissions", res));
      return res.data?.submissions ?? [];
    },
  });
}

// An approved submission knows its version. Otherwise the skill decides: a
// skill with no served version has nothing for the submission to change.
function useServedSkills(names: string[]) {
  const unique = [...new Set(names)];
  const results = useQueries({
    queries: unique.map((name) => ({
      queryKey: ["submission-skill", name],
      queryFn: async () => {
        const res = await getSkill({ path: { name } });
        if (res.response?.status === 404) return false;
        if (res.error !== undefined) throw new Error(apiErrorMessage("load the skill", res));
        return (res.data?.latest_version ?? 0) > 0;
      },
      retry: false,
    })),
  });
  return new Map(unique.map((name, i) => [name, results[i]?.data]));
}

function submissionKind(sub: Submission, served: boolean | undefined): string {
  if (sub.version != null) return sub.version === 1 ? "New skill" : "Change";
  if (served === undefined) return "";
  return served ? "Change" : "New skill";
}

function ScanStatus({ report }: { report: ScanReport | null }) {
  const count = report?.findings?.length ?? 0;
  if (count === 0) {
    return <span className="font-mono text-[11px] text-text-secondary">scan clean</span>;
  }
  return (
    <span
      className={cn(
        "font-mono text-[11px]",
        report?.status === "critical" && "text-danger",
        report?.status === "warn" && "text-warning",
        report?.status === "info" && "text-text-secondary",
      )}
    >
      {count} {count === 1 ? "finding" : "findings"}
    </span>
  );
}

function SubmissionRow({
  sub,
  kind,
  to,
  rowProps,
  focusOnMount,
}: {
  sub: Submission;
  kind: string;
  to: string;
  rowProps: ListRowProps;
  focusOnMount: boolean;
}) {
  return (
    <Link
      to={to}
      {...rowProps}
      ref={(el) => {
        rowProps.ref(el);
        if (el && focusOnMount) el.focus();
      }}
      tabIndex={0}
      className="flex flex-wrap items-center gap-x-4 gap-y-1 px-3.5 py-3 border-b border-border last:border-b-0 hover:bg-bg-tertiary transition-colors"
    >
      <div className="flex-1 basis-full md:basis-0 min-w-0">
        <div className="font-mono text-[13px] font-medium text-text-primary truncate">{sub.skill_name}</div>
        <div className="text-xs text-text-tertiary truncate mt-0.5">
          {[kind, sub.consumer].filter(Boolean).join(" · ")}
        </div>
      </div>
      {sub.state !== "pending" && (
        <span className="font-mono text-[11px] text-text-secondary">{sub.state}</span>
      )}
      <ScanStatus report={(sub.scan_result ?? null) as ScanReport | null} />
      <span className="font-mono text-[11px] text-text-tertiary whitespace-nowrap">
        {relativeTime(sub.created_at)}
      </span>
    </Link>
  );
}

export function Submissions() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const raw = params.get("state");
  const state = STATES.find((s) => s.id === raw) ?? STATES[0];
  const openId = params.get("submission");

  const hrefFor = (next: string, id?: string) => {
    const search = new URLSearchParams({ tab: "submissions" });
    if (next !== "pending") search.set("state", next);
    if (id) search.set("submission", id);
    return `/review?${search.toString()}`;
  };

  // Returning from a detail puts the cursor back on the row it opened.
  const returnTo = useRef<string | null>(null);
  if (openId) returnTo.current = openId;

  const listRow = useListNavigation<string>((id) => navigate(hrefFor(state.id, id)));
  const query = useSubmissionList(state.id);
  const subs = query.data ?? [];
  const served = useServedSkills(
    openId ? [] : subs.filter((s) => s.version == null).map((s) => s.skill_name),
  );

  if (openId) {
    return <SubmissionDetail key={openId} id={openId} backHref={hrefFor(state.id)} />;
  }

  const refocus = returnTo.current;
  returnTo.current = null;

  return (
    <div className="flex flex-col gap-3">
      <p className="text-xs text-text-tertiary">
        Skill archives that agents send for human review. Nothing is published until a person approves it.
      </p>
      <div role="group" aria-label="Filter by state" className="flex flex-wrap gap-1">
        {STATES.map((s) => (
          <Link
            key={s.id}
            to={hrefFor(s.id)}
            aria-current={s.id === state.id ? "true" : undefined}
            className={cn(
              "text-xs px-2.5 py-1 rounded-md border transition-colors",
              s.id === state.id
                ? "border-border bg-bg-tertiary text-text-primary"
                : "border-transparent text-text-secondary hover:bg-bg-tertiary",
            )}
          >
            {s.label}
          </Link>
        ))}
      </div>

      <div className="border border-border rounded-lg bg-bg-secondary">
        {query.isLoading && <RowsSkeleton rows={3} />}
        {query.isError && (
          <EmptyState
            message={query.error.message}
            action={
              <Button variant="outline" size="xs" onClick={() => query.refetch()}>
                Retry
              </Button>
            }
          />
        )}
        {query.isSuccess && subs.length === 0 && (
          <EmptyState
            className="py-16"
            message={state.empty}
            action={
              state.id === "pending" || state.id === "all" ? (
                <a
                  href={DOCS_URL}
                  target="_blank"
                  rel="noreferrer"
                  className="text-[13px] text-accent hover:underline"
                >
                  Read how an agent uses <span className="font-mono">skael submissions submit</span>
                </a>
              ) : (
                <Link to={hrefFor("pending")} className="text-[13px] text-accent hover:underline">
                  Show pending
                </Link>
              )
            }
          />
        )}
        {subs.map((sub) => (
          <SubmissionRow
            key={sub.id}
            sub={sub}
            kind={submissionKind(sub, served.get(sub.skill_name))}
            to={hrefFor(state.id, sub.id)}
            rowProps={listRow(sub.id)}
            focusOnMount={sub.id === refocus}
          />
        ))}
      </div>
    </div>
  );
}
