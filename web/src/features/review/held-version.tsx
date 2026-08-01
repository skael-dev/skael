import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ShieldQuestion } from "lucide-react";
import { reviewSkillVersion } from "@/api/sdk.gen";
import type { HeldVersion as HeldVersionType, Reason, ScanReport } from "@/api/types.gen";
import { ScanFindings } from "@/features/security/scan-findings";
import { useRunEval } from "@/features/quality/use-run-eval";

// The gate's own decision shape isn't in the generated types beyond
// `unknown` (Decision is opaque JSON on the wire), so we narrow it here for
// rendering only. We never re-derive appealability from this — `clears` is
// rendered verbatim, exactly as the gate wrote it.
type GateDecision = {
  outcome: string;
  reasons: Reason[] | null;
};

function ReasonRow({ reason }: { reason: Reason }) {
  return (
    <div className="border-b border-border last:border-b-0 px-4 py-3">
      <div className="flex items-center gap-3 flex-wrap">
        <span className="text-xs font-mono text-text-primary">{reason.rule}</span>
        <span className="text-[11px] text-text-tertiary uppercase tracking-wider">
          {reason.severity}
        </span>
        <span className="text-[11px] text-text-tertiary font-mono">
          {reason.file}:{reason.line}
        </span>
      </div>
      <div className="text-xs text-text-secondary mt-1.5 leading-relaxed">{reason.message}</div>
      {/* The gate owns this sentence. Render it verbatim — no lookup table,
          no parsing, no inference of appealability from its content or
          absence. An unappealable finding still carries a sentence here. */}
      <div className="text-[11px] text-text-tertiary mt-1.5 italic">Clears: {reason.clears}</div>
    </div>
  );
}

export function HeldVersion({
  held,
  canDecide,
  decideDisabledReason,
}: {
  held: HeldVersionType;
  canDecide: boolean;
  decideDisabledReason?: string;
}) {
  const [reason, setReason] = useState("");
  const qc = useQueryClient();
  const { run, isPending: evalPending, isError: evalError, error: evalErr } = useRunEval(
    held.skill_name,
  );

  const decide = useMutation({
    mutationFn: async (action: "approve" | "reject") => {
      const res = await reviewSkillVersion({
        path: { name: held.skill_name, version: held.version },
        body: { action, reason },
      });
      if (res.error) throw new Error(res.error.detail ?? `Failed to ${action}`);
      return res.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["review-queue"] });
    },
  });

  const decision = (held.gate_decision ?? null) as GateDecision | null;
  const scanResult = (held.scan_result ?? null) as ScanReport | null;
  const reasons = decision?.reasons ?? [];
  const findings = scanResult?.findings ?? [];

  const disabled = !canDecide || decide.isPending;

  return (
    <div className="border border-border rounded-lg overflow-hidden">
      <div className="px-4 py-3 bg-bg-secondary border-b border-border flex items-center justify-between flex-wrap gap-2">
        <div className="flex items-center gap-2">
          <ShieldQuestion size={16} className="text-warning" />
          <span className="text-sm font-medium text-text-primary">{held.skill_name}</span>
          <span className="text-xs text-text-tertiary font-mono">v{held.version}</span>
        </div>
        <div className="text-[11px] text-text-tertiary">
          Held {new Date(held.created_at).toLocaleString()}
          {held.published_by ? ` · published by ${held.published_by}` : ""}
        </div>
      </div>

      {/* Gate decision — the reasons this version was held, and per-reason
          resolution paths in the gate's own words. */}
      {reasons.length > 0 && (
        <div className="border-b border-border">
          {reasons.map((r, i) => (
            <ReasonRow key={`${r.rule}-${r.file}-${r.line}-${i}`} reason={r} />
          ))}
        </div>
      )}

      <div className="p-4">
        <ScanFindings findings={findings} scanStatus={scanResult?.status ?? ""} />
      </div>

      <div className="px-4 py-3 border-t border-border bg-bg-secondary flex flex-col gap-2">
        <div className="flex items-center gap-3 flex-wrap">
          <button
            onClick={run}
            disabled={evalPending}
            className="text-xs text-accent hover:underline disabled:opacity-50"
          >
            {evalPending ? "Queueing…" : "Run eval"}
          </button>
          {evalError && (
            <span className="text-[11px] text-danger">{evalErr?.message ?? "Failed to queue eval"}</span>
          )}
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <input
            type="text"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="Reason (recorded on the version)"
            disabled={disabled}
            className="flex-1 min-w-[200px] text-xs bg-bg-primary border border-border rounded px-2 py-1.5 text-text-primary placeholder:text-text-tertiary disabled:opacity-50"
          />
          <button
            onClick={() => decide.mutate("approve")}
            disabled={disabled}
            title={!canDecide ? decideDisabledReason : undefined}
            className="text-xs font-medium px-3 py-1.5 rounded bg-accent text-bg-primary hover:opacity-90 disabled:opacity-40 disabled:cursor-not-allowed transition-opacity"
          >
            Approve
          </button>
          <button
            onClick={() => decide.mutate("reject")}
            disabled={disabled}
            title={!canDecide ? decideDisabledReason : undefined}
            className="text-xs font-medium px-3 py-1.5 rounded border border-border text-text-secondary hover:bg-bg-tertiary disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
          >
            Reject
          </button>
          {/* The "why" a member can't decide is already stated above, in the
              gate's own clears sentence for each finding — this just points
              at it rather than restating appealability a second way. */}
          {!canDecide && decideDisabledReason && (
            <span className="text-[11px] text-text-tertiary" data-testid="decide-disabled-reason">
              {decideDisabledReason}
            </span>
          )}
          {decide.isError && (
            <span className="text-[11px] text-danger">{(decide.error as Error).message}</span>
          )}
        </div>
      </div>
    </div>
  );
}
