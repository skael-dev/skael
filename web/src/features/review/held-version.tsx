import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Check, ShieldQuestion } from "lucide-react";
import { reviewSkillVersion } from "@/api/sdk.gen";
import type { HeldVersion as HeldVersionType, Reason } from "@/api/types.gen";
import { useAuth } from "@/app/auth-provider";
import { ScanFindings, type ScanReport } from "@/features/security/scan-findings";
import { useRunEval } from "@/features/quality/use-run-eval";
import { cn } from "@/lib/utils";

// The gate's own decision shape isn't in the generated types beyond
// `unknown` (Decision is opaque JSON on the wire), so we narrow it here for
// rendering only. We never re-derive appealability from this — `clears` is
// rendered verbatim, exactly as the gate wrote it.
type GateDecision = {
  outcome: string;
  reasons: Reason[] | null;
};

// gate_decision is opaque JSON on the wire — a malformed or absent payload
// must render as "no decision recorded" rather than crash on `.reasons`.
function isGateDecision(value: unknown): value is GateDecision {
  return (
    !!value &&
    typeof value === "object" &&
    typeof (value as { outcome?: unknown }).outcome === "string"
  );
}

// ── Per-reason hold kinds ─────────────────────────────────────────────
//
// internal/skill.HeldVersion.HoldReasons/Outstanding carry the reason
// *kinds* the gate holds a version for (internal/gate.ReasonScan = "scan",
// internal/gate.ReasonOwnership = "ownership") — not one entry per scan
// finding. HoldReasons is every kind the version was ever held for;
// Outstanding is the subset with no approval yet. They can differ once one
// kind clears but the version stays held on another — the exact case this
// screen must render honestly: a cleared kind reads as cleared, never as
// the version being released.
const REASON_LABELS: Record<string, string> = {
  scan: "Security finding",
  ownership: "Ownership approval",
};

function reasonLabel(kind: string): string {
  return REASON_LABELS[kind] ?? kind;
}

type AuthUser = { id: string; role: string } | null;

function isPrivileged(user: AuthUser): boolean {
  return user != null && (user.role === "owner" || user.role === "admin");
}

// canDecideReason mirrors the authorization switch in
// internal/skill/review_routes.go's registerReviewRoutes step 4: an instance
// admin/owner may clear either kind; a skill's owner may clear only the
// ownership kind, never a scan finding. Kept purely client-side (never used
// to bypass anything, only to decide what's worth letting someone click) —
// the server re-checks on every request.
function canDecideReason(
  reasonKind: string,
  user: AuthUser,
  held: HeldVersionType,
): { allowed: boolean; reasonText: string } {
  if (reasonKind === "ownership") {
    const isSkillOwner = user != null && (held.owners ?? []).some((o) => o.id === user.id);
    return {
      allowed: isPrivileged(user) || isSkillOwner,
      reasonText: "Only an owner of this skill, or an instance admin, can decide this.",
    };
  }
  return {
    allowed: isPrivileged(user),
    reasonText: "Only an owner or admin can clear a security finding.",
  };
}

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

// ── Hold reason chips ───────────────────────────────────────────────
//
// One chip per kind ever held (HoldReasons), styled by whether it's still
// outstanding or already cleared. A cleared chip never says "released" —
// that word describes the whole version, and with anything still
// outstanding the version is not released.
function ReasonChips({ holdReasons, outstanding }: { holdReasons: string[]; outstanding: string[] }) {
  if (holdReasons.length === 0) return null;
  return (
    <div className="px-4 py-3 border-b border-border flex flex-wrap gap-2">
      {holdReasons.map((kind) => {
        const cleared = !outstanding.includes(kind);
        return (
          <span
            key={kind}
            className={cn(
              "inline-flex items-center gap-1.5 text-[11px] px-2 py-1 rounded-full border",
              cleared
                ? "border-border text-text-tertiary bg-bg-tertiary"
                : "border-warning/40 text-warning bg-warning/10",
            )}
          >
            {cleared ? <Check className="size-3" /> : <ShieldQuestion className="size-3" />}
            {reasonLabel(kind)} — {cleared ? "cleared" : "outstanding"}
          </span>
        );
      })}
    </div>
  );
}

// ── Version diff (raw Chi route, not in the generated client) ───────
type VersionDiffFile = { path: string; status: string };
type VersionDiffResp = { against: number; skill_md: string; files: VersionDiffFile[] };

async function fetchVersionDiff(name: string, version: number): Promise<VersionDiffResp | null> {
  try {
    const res = await fetch(
      `/api/skills/${encodeURIComponent(name)}/versions/${version}/diff`,
      { credentials: "include" },
    );
    if (!res.ok) return null;
    return (await res.json()) as VersionDiffResp;
  } catch {
    return null;
  }
}

function diffLineClass(line: string): string {
  if (line.startsWith("+++") || line.startsWith("---")) return "text-text-tertiary";
  if (line.startsWith("+")) return "text-accent";
  if (line.startsWith("-")) return "text-danger";
  return "text-text-secondary";
}

// A file addition outside SKILL.md is the case a reviewer is most likely to
// miss skimming prose — a skill can add an executable it never mentions.
// Flagged distinctly rather than left to blend in with modified/removed
// rows.
function FileChangeRow({ file }: { file: VersionDiffFile }) {
  const flagged = file.status === "added" && file.path !== "SKILL.md";
  return (
    <div
      className={cn(
        "flex items-center gap-2 text-[11px] px-2 py-1.5 rounded",
        flagged ? "bg-warning/10 border border-warning/30" : "text-text-secondary",
      )}
    >
      {flagged && (
        <AlertTriangle
          role="img"
          aria-label="New file outside SKILL.md"
          className="size-3 text-warning shrink-0"
        />
      )}
      <span className="font-mono text-text-primary">{file.path}</span>
      <span className="text-text-tertiary uppercase tracking-wide">{file.status}</span>
    </div>
  );
}

function VersionDiffPanel({ diff }: { diff: VersionDiffResp }) {
  const lines = diff.skill_md.split("\n").filter((l) => l.length > 0);
  return (
    <div className="p-4 border-t border-border">
      <div className="text-[11px] uppercase tracking-wide text-text-tertiary mb-2">
        {diff.against === 0 ? "First version — nothing to compare against" : `Diff vs v${diff.against}`}
      </div>
      {lines.length > 0 && (
        <pre className="text-[11px] font-mono bg-bg-primary border border-border rounded p-2.5 overflow-x-auto whitespace-pre">
          {lines.map((line, i) => (
            <div key={i} className={diffLineClass(line)}>
              {line}
            </div>
          ))}
        </pre>
      )}
      {diff.files.length > 0 && (
        <div className="mt-3 flex flex-col gap-1">
          {diff.files.map((f) => (
            <FileChangeRow key={f.path} file={f} />
          ))}
        </div>
      )}
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
  const { user } = useAuth();
  const { run, isPending: evalPending, isError: evalError, error: evalErr } = useRunEval(
    held.skill_name,
    held.version,
  );

  const decide = useMutation({
    mutationFn: async ({
      action,
      holdReason,
    }: {
      action: "approve" | "reject";
      holdReason?: string;
    }) => {
      const res = await reviewSkillVersion({
        path: { name: held.skill_name, version: held.version },
        body: { action, reason, ...(holdReason ? { hold_reason: holdReason } : {}) },
      });
      if (res.error) throw new Error(res.error.detail ?? `Failed to ${action}`);
      return res.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["review-queue"] });
    },
  });

  const diffQuery = useQuery({
    queryKey: ["version-diff", held.skill_name, held.version],
    queryFn: () => fetchVersionDiff(held.skill_name, held.version),
  });

  const rawDecision = held.gate_decision;
  const decision = isGateDecision(rawDecision) ? rawDecision : null;
  const malformedDecision = rawDecision != null && !isGateDecision(rawDecision);
  const scanResult = (held.scan_result ?? null) as ScanReport | null;
  const reasons = decision?.reasons ?? [];
  const findings = scanResult?.findings ?? [];

  const holdReasons = held.hold_reasons ?? [];
  const outstanding = held.outstanding ?? [];
  const perReason = holdReasons.length > 0;

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

      {/* Per-reason chips — a partly-cleared hold must never read as
          released: each hold kind renders its own cleared/outstanding
          state, independent of the others. */}
      <ReasonChips holdReasons={holdReasons} outstanding={outstanding} />

      {/* Gate decision — the reasons this version was held, and per-reason
          resolution paths in the gate's own words. */}
      {reasons.length > 0 && (
        <div className="border-b border-border">
          {reasons.map((r, i) => (
            <ReasonRow key={`${r.rule}-${r.file}-${r.line}-${i}`} reason={r} />
          ))}
        </div>
      )}
      {malformedDecision && (
        <div className="border-b border-border px-4 py-3 text-[11px] text-text-tertiary italic">
          No decision recorded.
        </div>
      )}

      <div className="p-4">
        <ScanFindings findings={findings} scanStatus={scanResult?.status ?? ""} />
      </div>

      {diffQuery.data && <VersionDiffPanel diff={diffQuery.data} />}

      <div className="px-4 py-3 border-t border-border bg-bg-secondary flex flex-col gap-2">
        <div className="flex items-center gap-3 flex-wrap">
          <button
            onClick={run}
            disabled={!canDecide || evalPending}
            title={!canDecide ? decideDisabledReason : undefined}
            className="text-xs text-accent hover:underline disabled:opacity-50"
          >
            {evalPending ? "Queueing…" : "Run eval"}
          </button>
          {evalError && (
            <span className="text-[11px] text-danger">{evalErr?.message ?? "Failed to queue eval"}</span>
          )}
        </div>

        <input
          type="text"
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="Reason (recorded on the version)"
          disabled={disabled}
          className="w-full text-xs bg-bg-primary border border-border rounded px-2 py-1.5 text-text-primary placeholder:text-text-tertiary disabled:opacity-50"
        />

        {perReason ? (
          // One approve/reject pair per outstanding reason kind. A control
          // for a kind the current actor cannot clear is disabled WITH the
          // reason stated — never hidden — same as the eval button above
          // and Task 18's "Manage" button.
          <div className="flex flex-col gap-2">
            {outstanding.length === 0 ? (
              <span className="text-[11px] text-text-tertiary italic">
                Nothing outstanding — every hold reason has cleared.
              </span>
            ) : (
              outstanding.map((kind) => {
                const { allowed, reasonText } = canDecideReason(kind, user, held);
                const kindDisabled = !allowed || decide.isPending;
                return (
                  <div key={kind} className="flex items-center gap-2 flex-wrap">
                    <span className="text-[11px] text-text-tertiary w-[160px] shrink-0">
                      {reasonLabel(kind)}
                    </span>
                    <button
                      onClick={() => decide.mutate({ action: "approve", holdReason: kind })}
                      disabled={kindDisabled}
                      title={!allowed ? reasonText : undefined}
                      className="text-xs font-medium px-3 py-1.5 rounded bg-accent text-bg-primary hover:opacity-90 disabled:opacity-40 disabled:cursor-not-allowed transition-opacity"
                    >
                      Approve
                    </button>
                    <button
                      onClick={() => decide.mutate({ action: "reject", holdReason: kind })}
                      disabled={kindDisabled}
                      title={!allowed ? reasonText : undefined}
                      className="text-xs font-medium px-3 py-1.5 rounded border border-border text-text-secondary hover:bg-bg-tertiary disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
                    >
                      Reject
                    </button>
                    {!allowed && (
                      <span className="text-[11px] text-text-tertiary">{reasonText}</span>
                    )}
                  </div>
                );
              })
            )}
            {decide.isError && (
              <span className="text-[11px] text-danger">{(decide.error as Error).message}</span>
            )}
          </div>
        ) : (
          <div className="flex items-center gap-2 flex-wrap">
            <button
              onClick={() => decide.mutate({ action: "approve" })}
              disabled={disabled}
              title={!canDecide ? decideDisabledReason : undefined}
              className="text-xs font-medium px-3 py-1.5 rounded bg-accent text-bg-primary hover:opacity-90 disabled:opacity-40 disabled:cursor-not-allowed transition-opacity"
            >
              Approve
            </button>
            <button
              onClick={() => decide.mutate({ action: "reject" })}
              disabled={disabled}
              title={!canDecide ? decideDisabledReason : undefined}
              className="text-xs font-medium px-3 py-1.5 rounded border border-border text-text-secondary hover:bg-bg-tertiary disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
            >
              Reject
            </button>
            {/* The "why" a member can't decide is already stated above, in
                the gate's own clears sentence for each finding — this just
                points at it rather than restating appealability a second
                way. */}
            {!canDecide && decideDisabledReason && (
              <span className="text-[11px] text-text-tertiary" data-testid="decide-disabled-reason">
                {decideDisabledReason}
              </span>
            )}
            {decide.isError && (
              <span className="text-[11px] text-danger">{(decide.error as Error).message}</span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
