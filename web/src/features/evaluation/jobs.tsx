import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowLeft, Square } from "lucide-react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { toast } from "sonner";
import { cancelEvalJob, listEvalJobEvents, listEvalJobs } from "@/api/sdk.gen";
import type { EvalJobEvent, EvalJobSummary } from "@/api/types.gen";
import { isPrivileged, useAuth } from "@/app/auth-provider";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { RowsSkeleton } from "@/components/ui/rows-skeleton";
import { throwOnApiError } from "@/lib/api-error";
import { relativeTime } from "@/lib/relative-time";
import { cn } from "@/lib/utils";
import { EvaluationTabs } from "./evaluation";

const JOBS_KEY = ["eval-jobs"];
const POLL_MS = 5_000;

const active = (j: EvalJobSummary) => j.status === "queued" || j.status === "running";

function money(n: number) {
  if (!n) return "—";
  return n < 0.01 ? "<$0.01" : `$${n.toFixed(2)}`;
}

function duration(ms?: number) {
  if (!ms) return "";
  const s = Math.round(ms / 1000);
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

function StatusBadge({ j }: { j: EvalJobSummary }) {
  const label = j.stalled ? "stalled" : j.status;
  const tone =
    j.stalled || j.status === "failed"
      ? "text-danger border-danger/40"
      : j.status === "running"
        ? "text-accent border-accent/40"
        : j.status === "done"
          ? "text-success border-success/40"
          : "text-text-secondary border-border";
  return <span className={cn("text-[11px] px-1.5 py-0.5 rounded border whitespace-nowrap", tone)}>{label}</span>;
}

function Progress({ j }: { j: EvalJobSummary }) {
  const live = active(j);
  if (!j.sessions_total) {
    const text = live ? j.phase || (j.status === "queued" ? "waiting for a worker" : "starting") : j.status === "failed" ? j.last_error.split("\n")[0] : "—";
    return <span className={cn("text-xs line-clamp-2 max-w-[320px]", j.status === "failed" ? "text-danger" : "text-text-tertiary")}>{text}</span>;
  }
  const pct = Math.min(100, Math.round((j.sessions_done / j.sessions_total) * 100));
  return (
    <div className="flex flex-col gap-1 min-w-[140px]">
      <div className="h-1.5 bg-bg-tertiary rounded-full overflow-hidden" aria-hidden>
        <div className={cn("h-full", j.sessions_failed ? "bg-warning" : live ? "bg-accent" : "bg-text-tertiary")} style={{ width: `${pct}%` }} />
      </div>
      <span className="text-[11px] text-text-tertiary tabular-nums">
        {live && `${j.phase} · `}
        {j.sessions_done}/{j.sessions_total} sessions
        {j.sessions_failed > 0 && <span className="text-warning"> · {j.sessions_failed} failed</span>}
      </span>
    </div>
  );
}

function useStop() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      const res = await cancelEvalJob({ path: { id } });
      throwOnApiError("stop the job", res);
    },
    onSuccess: () => {
      toast.success("Stopped. The worker ends its sessions on its next heartbeat.");
      qc.invalidateQueries({ queryKey: JOBS_KEY });
    },
    onError: (err) => toast.error(err.message),
  });
}

function StopButton({ j }: { j: EvalJobSummary }) {
  const stop = useStop();
  if (!active(j)) return null;
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      disabled={stop.isPending}
      onClick={(e) => {
        e.stopPropagation();
        stop.mutate(j.id);
      }}
    >
      <Square size={12} /> Stop
    </Button>
  );
}

function Guard({ children }: { children: React.ReactNode }) {
  const { user } = useAuth();
  if (!isPrivileged(user)) {
    return (
      <div className="px-5 md:px-10 py-8">
        <EmptyState message="Only an owner or an admin can see evaluation jobs." />
      </div>
    );
  }
  return <>{children}</>;
}

function Header({ desc }: { desc: string }) {
  return (
    <div className="flex flex-col gap-4">
      <div>
        <h1 className="text-2xl font-medium tracking-tight text-text-primary m-0 mb-1">Evaluation</h1>
        <p className="text-sm text-text-secondary m-0">{desc}</p>
      </div>
      <EvaluationTabs />
    </div>
  );
}

export function EvalJobs() {
  return (
    <Guard>
      <JobsList />
    </Guard>
  );
}

function JobsList() {
  const navigate = useNavigate();
  const jobs = useQuery({
    queryKey: JOBS_KEY,
    refetchInterval: (q) => ((q.state.data ?? []).some(active) ? POLL_MS : 30_000),
    queryFn: async () => {
      const res = await listEvalJobs();
      throwOnApiError("load the jobs", res);
      return res.data!.jobs ?? [];
    },
  });
  const list = jobs.data ?? [];
  return (
    <div className="px-5 md:px-10 py-8 max-w-[1100px] w-full mx-auto flex flex-col gap-8">
      <Header desc="Every queued and running evaluation, and those that finished this week. A running job with no event for 3 minutes shows as stalled." />
      {jobs.isLoading && <RowsSkeleton rows={4} />}
      {jobs.data && list.length === 0 && (
        <div className="bg-bg-secondary border border-border rounded-lg">
          <EmptyState message="No evaluation has run this week." />
        </div>
      )}
      {list.length > 0 && (
        <div className="bg-bg-secondary border border-border rounded-lg overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-[11px] uppercase tracking-wider text-text-tertiary">
                <th className="font-medium px-4 py-2">Skill</th>
                <th className="font-medium px-4 py-2">Status</th>
                <th className="font-medium px-4 py-2">Progress</th>
                <th className="font-medium px-4 py-2 text-right">Cost</th>
                <th className="font-medium px-4 py-2">Worker</th>
                <th className="font-medium px-4 py-2">Last event</th>
                <th className="px-2" />
              </tr>
            </thead>
            <tbody>
              {list.map((j) => (
                <tr
                  key={j.id}
                  onClick={() => navigate(`/settings/evaluation/jobs/${j.id}`)}
                  className="border-t border-border cursor-pointer hover:bg-bg-tertiary/40 transition-colors"
                >
                  <td className="px-4 py-3">
                    <div className="flex flex-col">
                      <span className="text-text-primary">
                        {j.skill} <span className="text-text-tertiary">v{j.version}</span>
                      </span>
                      <span className="text-[11px] text-text-tertiary">
                        {j.contest_id ? "contest" : j.tier} · {relativeTime(j.created_at)}
                      </span>
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    <StatusBadge j={j} />
                  </td>
                  <td className="px-4 py-3">
                    <Progress j={j} />
                  </td>
                  <td className="px-4 py-3 text-right tabular-nums text-text-secondary">{money(j.cost_usd)}</td>
                  <td className="px-4 py-3 font-mono text-[11px] text-text-secondary">{j.worker_id || "—"}</td>
                  <td className={cn("px-4 py-3 text-xs", j.stalled ? "text-danger" : "text-text-tertiary")}>
                    {j.last_event_at ? relativeTime(j.last_event_at) : "—"}
                  </td>
                  <td className="px-2 py-3 text-right">
                    <StopButton j={j} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

export function EvalJob() {
  return (
    <Guard>
      <JobView />
    </Guard>
  );
}

const KIND_LABEL: Record<string, string> = {
  phase: "Phase",
  health: "Health check",
  plan: "Plan",
  session_start: "Session started",
  session_end: "Session ended",
  grade: "Graded",
  cancelled: "Stopped",
  failed: "Job failed",
};

function EventRow({ e }: { e: EvalJobEvent }) {
  const failed = e.ok === false || e.kind === "failed";
  const session = [e.task && `task ${e.task}`, e.condition, e.model, e.attempt && e.attempt > 1 ? `attempt ${e.attempt}` : ""].filter(Boolean).join(" · ");
  let detail: React.ReactNode = session;
  switch (e.kind) {
    case "phase":
      detail = e.phase;
      break;
    case "plan":
      detail = `${e.total} sessions to run`;
      break;
    case "health":
      detail = `${e.model} · ${e.ok ? "ok" : "failed"}`;
      break;
    case "cancelled":
      detail = e.actor ? `by ${e.actor}` : "";
      break;
    case "session_end": {
      const facts = [duration(e.duration_ms), e.cost_usd ? money(e.cost_usd) : "", e.input_tokens ? `${e.input_tokens + (e.output_tokens ?? 0)} tokens` : ""];
      detail = [session, ...facts].filter(Boolean).join(" · ");
      break;
    }
    case "grade":
      detail = [session, duration(e.duration_ms)].filter(Boolean).join(" · ");
      break;
  }
  return (
    <div className="grid grid-cols-[72px_minmax(0,1fr)] sm:grid-cols-[90px_140px_minmax(0,1fr)] gap-x-3 gap-y-0.5 px-4 py-2 border-t border-border first:border-t-0 text-xs">
      <span className="text-text-tertiary tabular-nums">{new Date(e.at).toLocaleTimeString()}</span>
      <span className={cn("text-text-secondary", failed && "text-danger")}>{KIND_LABEL[e.kind] ?? e.kind}</span>
      <div className="col-span-2 sm:col-span-1 min-w-0 flex flex-col gap-0.5">
        <span className="text-text-primary break-words">{detail}</span>
        {e.error && <span className="text-danger font-mono text-[11px] whitespace-pre-wrap break-words">{e.error}</span>}
      </div>
    </div>
  );
}

function JobView() {
  const { id = "" } = useParams();
  const q = useQuery({
    queryKey: ["eval-job-events", id],
    refetchInterval: (query) => (query.state.data?.job && active(query.state.data.job) ? POLL_MS : false),
    queryFn: async () => {
      const res = await listEvalJobEvents({ path: { id } });
      throwOnApiError("load the job", res);
      return res.data!;
    },
  });
  const j = q.data?.job;
  const events = q.data?.events ?? [];
  return (
    <div className="px-5 md:px-10 py-8 max-w-[1100px] w-full mx-auto flex flex-col gap-6">
      <Header desc="One evaluation's progress. Each failed session shows the provider's own error." />
      <Link to="/settings/evaluation/jobs" className="text-xs text-text-tertiary hover:text-text-primary inline-flex items-center gap-1 self-start">
        <ArrowLeft size={12} /> All jobs
      </Link>
      {q.isLoading && <RowsSkeleton rows={6} />}
      {q.error && <EmptyState message={q.error.message} />}
      {j && (
        <div className="bg-bg-secondary border border-border rounded-lg px-4 py-4 flex flex-wrap items-center gap-x-6 gap-y-3">
          <div className="flex flex-col">
            <Link to={`/skills/${encodeURIComponent(j.skill)}`} className="text-base text-text-primary hover:underline">
              {j.skill} <span className="text-text-tertiary">v{j.version}</span>
            </Link>
            <span className="text-[11px] text-text-tertiary">
              {j.contest_id ? "contest" : j.tier} · requested by {j.requested_by} · attempt {j.attempts}
            </span>
          </div>
          <StatusBadge j={j} />
          <Progress j={j} />
          <span className="text-sm tabular-nums text-text-secondary">{money(j.cost_usd)}</span>
          <span className="text-xs font-mono text-text-tertiary">{j.worker_id}</span>
          <div className="ml-auto">
            <StopButton j={j} />
          </div>
          {j.stalled && (
            <p className="basis-full text-xs text-danger m-0 flex gap-2">
              <AlertTriangle size={14} className="shrink-0 mt-0.5" />
              No event for over 3 minutes. Check the worker's logs, or stop the job.
            </p>
          )}
          {j.status === "failed" && j.last_error && <p className="basis-full text-xs text-danger font-mono m-0 whitespace-pre-wrap break-words">{j.last_error}</p>}
        </div>
      )}
      {j && events.length === 0 && (
        <div className="bg-bg-secondary border border-border rounded-lg">
          <EmptyState message={j.status === "queued" ? "Waiting for a worker to claim this job." : "No events recorded for this job."} />
        </div>
      )}
      {events.length > 0 && (
        <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
          {events.map((e) => (
            <EventRow key={e.id} e={e} />
          ))}
        </div>
      )}
    </div>
  );
}
