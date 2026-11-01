import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { AlertTriangle, ArrowLeft, ExternalLink } from "lucide-react";
import { toast } from "sonner";
import {
  decideSubmission,
  getSubmission,
  getSubmissionDiff,
  listSkillVersions,
} from "@/api/sdk.gen";
import type { Submission, SubmissionEdit, SubmissionFileDiff } from "@/api/types.gen";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { RowsSkeleton } from "@/components/ui/rows-skeleton";
import { ScanFindings, type ScanReport } from "@/features/security/scan-findings";
import { formatSize } from "@/features/skills/file-content";
import { buildFileSearch } from "@/features/skills/file-link";
import { apiErrorMessage } from "@/lib/api-error";
import { relativeTime } from "@/lib/relative-time";
import { diffLineClass } from "./held-version";

const DECISION_STEPS: Record<number, string> = {
  403: "Ask one of them to decide it.",
  409: "Reload the page to see the decision.",
  422: "Change the files or the reason and try again.",
};

// The server omits empty content, so an empty text file has none. A text file
// over the server's edit limit has none either, and its size tells them apart.
function isEditable(file: SubmissionFileDiff): boolean {
  return file.status !== "removed" && !file.binary && (file.content !== undefined || file.size === 0);
}

// A textarea reports line endings as LF, so a CRLF file compares as LF.
function changedEdits(files: SubmissionFileDiff[], drafts: Record<string, string>): SubmissionEdit[] {
  return files.flatMap((file) => {
    const draft = drafts[file.path];
    if (draft === undefined || !isEditable(file)) return [];
    const original = (file.content ?? "").replace(/\r\n?/g, "\n");
    return draft === original ? [] : [{ path: file.path, content: draft }];
  });
}

function versionHref(sub: Submission): string {
  return `/skills/${encodeURIComponent(sub.skill_name)}${buildFileSearch({ version: sub.version })}`;
}

function versionsKey(name: string) {
  return ["submission-versions", name];
}

async function fetchVersions(name: string) {
  const res = await listSkillVersions({ path: { name } });
  if (res.error !== undefined) throw new Error(apiErrorMessage("load the versions", res));
  return res.data?.versions ?? [];
}

// The state is in the key because a decision changes what the diff compares with.
function useSubmissionDiff(id: string, state: string) {
  return useQuery({
    queryKey: ["submission-diff", id, state],
    queryFn: async () => {
      const res = await getSubmissionDiff({ path: { id } });
      if (res.error !== undefined) throw new Error(apiErrorMessage("load the diff", res));
      return res.data!;
    },
  });
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-2 min-w-0">
      <h3 className="text-[11px] font-mono uppercase tracking-[0.08em] text-text-tertiary">{title}</h3>
      {children}
    </section>
  );
}

function ApprovalResult({ sub }: { sub: Submission }) {
  const versions = useQuery({
    queryKey: versionsKey(sub.skill_name),
    queryFn: () => fetchVersions(sub.skill_name),
  });
  const gate = versions.data?.find((v) => v.version === sub.version)?.gate_state;
  const open = (
    <Link to={versionHref(sub)} className="text-accent hover:underline font-mono">
      Open v{sub.version}
    </Link>
  );

  if (versions.isLoading) return <RowsSkeleton rows={1} />;
  if (gate === "needs_review") {
    return (
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px]">
        <span className="text-warning">
          The publish gate holds v{sub.version}. Machines cannot read it until a reviewer releases it.
        </span>
        {open}
        <Link to="/review" className="text-accent hover:underline">
          Open held versions
        </Link>
      </div>
    );
  }
  if (gate === "rejected") {
    return (
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px]">
        <span className="text-text-secondary">A reviewer rejected v{sub.version} after this approval.</span>
        {open}
      </div>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px]">
      <span className="text-text-secondary">
        {gate === undefined ? `This approval created v${sub.version}.` : `v${sub.version} is published.`}
      </span>
      {open}
    </div>
  );
}

function DecisionPanel({ sub }: { sub: Submission }) {
  const approved = sub.state === "approved";
  return (
    <div className="border border-border rounded-lg bg-bg-secondary px-4 py-3 flex flex-col gap-2 min-w-0">
      <div className="text-[13px] text-text-primary break-words">
        {approved ? "Approved" : "Rejected"} by <span className="font-mono">{sub.decided_by}</span>
        {sub.decided_at && (
          <span className="font-mono text-[11px] text-text-tertiary"> · {relativeTime(sub.decided_at)}</span>
        )}
        {sub.edited && <span className="text-text-secondary"> · edited before approval</span>}
      </div>
      {sub.reason && (
        <p className="text-[13px] text-text-secondary whitespace-pre-wrap break-words">{sub.reason}</p>
      )}
      {approved && sub.version != null && <ApprovalResult sub={sub} />}
    </div>
  );
}

function FileBody({ file }: { file: SubmissionFileDiff }) {
  if (file.binary) {
    return (
      <p className="px-3 py-2.5 text-xs text-text-secondary">
        {file.status === "removed"
          ? "Binary file. The submission removes it."
          : `Binary file, ${formatSize(file.size)}. The diff does not show its content.`}
      </p>
    );
  }
  if (!file.diff) {
    return <p className="px-3 py-2.5 text-xs text-text-secondary">Empty file.</p>;
  }
  const lines = file.diff.split("\n");
  if (lines[lines.length - 1] === "") lines.pop();
  return (
    <pre className="text-[11px] font-mono p-3 overflow-auto max-h-[32rem] whitespace-pre bg-bg-primary">
      {lines.map((line, i) => (
        <div key={i} className={diffLineClass(line)}>
          {line || " "}
        </div>
      ))}
    </pre>
  );
}

function FileCard({ file }: { file: SubmissionFileDiff }) {
  const flagged = file.status === "added" && file.path !== "SKILL.md";
  return (
    <div className="border border-border rounded-lg overflow-hidden min-w-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 bg-bg-secondary border-b border-border">
        {flagged && (
          <AlertTriangle
            role="img"
            aria-label="New file outside SKILL.md"
            className="size-3 text-warning shrink-0"
          />
        )}
        <span className="font-mono text-xs text-text-primary break-all">{file.path}</span>
        <span className="font-mono text-[11px] text-text-tertiary uppercase tracking-wide">{file.status}</span>
        {file.status !== "removed" && (
          <span className="font-mono text-[11px] text-text-tertiary ml-auto">{formatSize(file.size)}</span>
        )}
      </div>
      <FileBody file={file} />
    </div>
  );
}

function DiffSection({ id, state }: { id: string; state: string }) {
  const query = useSubmissionDiff(id, state);

  if (query.isLoading) {
    return (
      <Section title="Diff">
        <div className="border border-border rounded-lg bg-bg-secondary">
          <RowsSkeleton rows={3} />
        </div>
      </Section>
    );
  }
  if (!query.data) {
    return (
      <Section title="Diff">
        <EmptyState
          className="border border-border rounded-lg"
          message={query.error?.message ?? "Could not load the diff. Reload the page and try again."}
          action={
            <Button variant="outline" size="xs" onClick={() => query.refetch()}>
              Retry
            </Button>
          }
        />
      </Section>
    );
  }

  const files = query.data.files ?? [];
  const changed = files.filter((f) => f.status !== "unchanged");
  const unchanged = files.filter((f) => f.status === "unchanged");
  const against = query.data.against;
  return (
    <Section title={against === 0 ? "Diff · new skill" : `Diff against v${against}`}>
      {state !== "pending" && against !== 0 && (
        <p className="text-xs text-text-tertiary">
          This compares the archive the agent sent with v{against}, the version served now. It can differ from the
          diff at the time of the decision, and it does not show a reviewer's edits as additions.
        </p>
      )}
      {changed.map((f) => (
        <FileCard key={f.path} file={f} />
      ))}
      {unchanged.length > 0 && (
        <p className="text-xs text-text-secondary break-words">
          {unchanged.length} unchanged:{" "}
          <span className="font-mono text-text-tertiary">{unchanged.map((f) => f.path).join(", ")}</span>
        </p>
      )}
    </Section>
  );
}

function EditFields({
  files,
  drafts,
  onChange,
}: {
  files: SubmissionFileDiff[];
  drafts: Record<string, string>;
  onChange: (path: string, value: string) => void;
}) {
  return (
    <div className="flex flex-col gap-3">
      {files.map((file) => {
        if (file.status === "removed") return null;
        if (!isEditable(file)) {
          return (
            <p key={file.path} className="text-xs text-text-tertiary break-words">
              <span className="font-mono">{file.path}</span>:{" "}
              {file.binary ? "a binary file" : "over the edit limit"}, so it publishes as submitted.
            </p>
          );
        }
        const value = drafts[file.path] ?? file.content ?? "";
        return (
          <label key={file.path} className="flex flex-col gap-1 min-w-0">
            <span className="font-mono text-xs text-text-secondary break-all">{file.path}</span>
            <textarea
              value={value}
              rows={Math.min(24, Math.max(4, value.split("\n").length + 1))}
              spellCheck={false}
              onChange={(e) => onChange(file.path, e.target.value)}
              className="w-full rounded-md border border-border bg-bg-primary p-2.5 font-mono text-xs text-text-primary"
            />
          </label>
        );
      })}
    </div>
  );
}

function DecisionActions({ sub }: { sub: Submission }) {
  const qc = useQueryClient();
  const [mode, setMode] = useState<"actions" | "edit" | "reject">("actions");
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [reason, setReason] = useState("");
  const [reasonMissing, setReasonMissing] = useState(false);

  const diff = useSubmissionDiff(sub.id, sub.state);
  const files = diff.data?.files ?? [];
  const edits = changedEdits(files, drafts);

  const decide = useMutation({
    mutationFn: async (body: { action: "approve" | "reject"; reason?: string; edits?: SubmissionEdit[] }) => {
      const res = await decideSubmission({ path: { id: sub.id }, body });
      if (res.error !== undefined) {
        throw new Error(apiErrorMessage(`${body.action} the submission`, res, DECISION_STEPS));
      }
      return res.data!;
    },
    onSuccess: async (decided) => {
      qc.setQueryData(["submission", sub.id], decided);
      qc.invalidateQueries({ queryKey: ["submissions"] });
      qc.invalidateQueries({ queryKey: ["review-queue"] });
      qc.invalidateQueries({ queryKey: ["submission-skill", sub.skill_name] });
      if (decided.state === "rejected") {
        toast.success(`Rejected the submission. ${decided.consumer} can read the reason.`);
        return;
      }
      const versions = await qc
        .fetchQuery({ queryKey: versionsKey(sub.skill_name), queryFn: () => fetchVersions(sub.skill_name) })
        .catch(() => []);
      const held = versions.find((v) => v.version === decided.version)?.gate_state === "needs_review";
      if (held) toast.warning(`The publish gate holds ${sub.skill_name} v${decided.version}`);
      else toast.success(`Approved ${sub.skill_name} v${decided.version}`);
    },
    onError: (err) => {
      toast.error(err.message);
      qc.invalidateQueries({ queryKey: ["submission", sub.id] });
    },
  });

  const submitReject = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = reason.trim();
    if (!trimmed) {
      setReasonMissing(true);
      return;
    }
    decide.mutate({ action: "reject", reason: trimmed });
  };

  const switchMode = (next: "actions" | "edit" | "reject") => {
    decide.reset();
    setMode(next);
  };
  const secondary = "border-border text-text-secondary";

  return (
    <div className="border border-border rounded-lg bg-bg-secondary p-4 flex flex-col gap-3 min-w-0">
      {mode === "actions" && (
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" disabled={decide.isPending} onClick={() => decide.mutate({ action: "approve" })}>
            {decide.isPending ? "Approving…" : "Approve"}
          </Button>
          <Button
            variant="outline"
            size="sm"
            className={secondary}
            disabled={decide.isPending || !diff.data}
            onClick={() => switchMode("edit")}
          >
            Edit then approve
          </Button>
          <Button
            variant="outline"
            size="sm"
            className={secondary}
            disabled={decide.isPending}
            onClick={() => switchMode("reject")}
          >
            Reject
          </Button>
        </div>
      )}

      {mode === "edit" && (
        <>
          <EditFields
            files={files}
            drafts={drafts}
            onChange={(path, value) => setDrafts((cur) => ({ ...cur, [path]: value }))}
          />
          <div className="flex flex-wrap items-center gap-2">
            <Button
              size="sm"
              disabled={decide.isPending || edits.length === 0}
              onClick={() => decide.mutate({ action: "approve", edits })}
            >
              {decide.isPending ? "Approving…" : "Approve with edits"}
            </Button>
            <Button
              variant="outline"
              size="sm"
              className={secondary}
              disabled={decide.isPending}
              onClick={() => switchMode("actions")}
            >
              Cancel
            </Button>
            <span className="text-xs text-text-tertiary">
              {edits.length === 0
                ? "Change a file to approve with edits."
                : `${edits.length} ${edits.length === 1 ? "file" : "files"} changed.`}
            </span>
          </div>
        </>
      )}

      {mode === "reject" && (
        <form onSubmit={submitReject} noValidate className="flex flex-col gap-2">
          <label className="flex flex-col gap-1">
            <span className="text-xs text-text-secondary">Reason. The agent reads it through the API and MCP.</span>
            <textarea
              value={reason}
              rows={3}
              autoFocus
              aria-invalid={reasonMissing}
              onChange={(e) => {
                setReason(e.target.value);
                setReasonMissing(false);
              }}
              className="w-full rounded-md border border-border bg-bg-primary p-2.5 text-[13px] text-text-primary"
            />
          </label>
          {reasonMissing && (
            <p role="alert" className="text-xs text-danger">
              A rejection needs a reason. Write what the agent must change.
            </p>
          )}
          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" variant="destructive" size="sm" disabled={decide.isPending}>
              {decide.isPending ? "Rejecting…" : "Reject submission"}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className={secondary}
              disabled={decide.isPending}
              onClick={() => switchMode("actions")}
            >
              Cancel
            </Button>
          </div>
        </form>
      )}

      {decide.isError && (
        <p role="alert" className="text-xs text-danger break-words">
          {decide.error.message}
        </p>
      )}
      <p className="text-xs text-text-tertiary">
        An owner of this skill, or an instance owner or admin, decides a submission. Approval publishes through the
        normal gate, so a blocking finding still holds the version.
      </p>
    </div>
  );
}

export function SubmissionDetail({ id, backHref }: { id: string; backHref: string }) {
  const query = useQuery({
    queryKey: ["submission", id],
    queryFn: async () => {
      const res = await getSubmission({ path: { id } });
      if (res.error !== undefined) {
        throw new Error(
          apiErrorMessage("load the submission", res, { 404: "Go back to the list and open another submission." }),
        );
      }
      return res.data!;
    },
    retry: false,
  });
  const sub = query.data;
  const back = (
    <Link
      to={backHref}
      className="inline-flex items-center gap-1.5 text-xs text-text-secondary hover:text-text-primary w-fit"
    >
      <ArrowLeft className="size-3" />
      All submissions
    </Link>
  );

  if (query.isLoading) {
    return (
      <div className="flex flex-col gap-4">
        {back}
        <div className="border border-border rounded-lg bg-bg-secondary">
          <RowsSkeleton rows={4} />
        </div>
      </div>
    );
  }
  if (!sub) {
    return (
      <div className="flex flex-col gap-4">
        {back}
        <EmptyState
          className="border border-border rounded-lg"
          message={query.error?.message ?? "Could not load the submission. Reload the page and try again."}
          action={
            <Button variant="outline" size="xs" onClick={() => query.refetch()}>
              Retry
            </Button>
          }
        />
      </div>
    );
  }

  const report = (sub.scan_result ?? null) as ScanReport | null;
  const findings = report?.findings ?? [];
  return (
    <div className="flex flex-col gap-5 min-w-0">
      {back}
      <div className="min-w-0">
        <h2 className="font-mono text-[15px] font-medium text-text-primary break-all">{sub.skill_name}</h2>
        <div className="flex flex-wrap gap-x-3 gap-y-1 mt-1 font-mono text-[11px] text-text-tertiary">
          <span>{sub.state}</span>
          <span>{sub.consumer}</span>
          <span>{relativeTime(sub.created_at)}</span>
        </div>
      </div>

      {sub.state !== "pending" && <DecisionPanel sub={sub} />}

      <Section title="Rationale">
        <p className="text-[13px] text-text-primary whitespace-pre-wrap break-words">{sub.rationale}</p>
      </Section>

      <Section title="Run">
        {sub.run_link ? (
          <a
            href={sub.run_link}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-start gap-1.5 text-[13px] font-mono text-accent hover:underline break-all w-fit"
          >
            {sub.run_link}
            <ExternalLink className="size-3 shrink-0 mt-1" />
          </a>
        ) : (
          <p className="text-[13px] text-text-secondary">The agent sent no run link.</p>
        )}
        {sub.external_run_id && (
          <p className="font-mono text-[11px] text-text-tertiary break-all">Run id {sub.external_run_id}</p>
        )}
      </Section>

      <Section title="Scan">
        {findings.length > 0 ? (
          <ScanFindings findings={findings} scanStatus={report?.status ?? ""} />
        ) : (
          <p className="text-[13px] text-text-secondary">The scan found nothing.</p>
        )}
      </Section>

      <DiffSection id={id} state={sub.state} />

      {sub.state === "pending" && <DecisionActions sub={sub} />}
    </div>
  );
}
