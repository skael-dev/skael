import { useNavigate } from "react-router-dom";
import { Checkbox } from "@/components/ui/checkbox";
import { SecurityBadge } from "@/features/security/security-badge";
import { ReviewStatus } from "@/features/security/review-status";
import type { SkillAnalytics } from "@/api/types.gen";
import { cn } from "@/lib/utils";

const TAG_COLORS: Record<string, string> = {
  review: "bg-purple-400",
  deploy: "bg-emerald-400",
  security: "bg-red-400",
  testing: "bg-blue-400",
  api: "bg-amber-400",
  db: "bg-cyan-400",
  ops: "bg-orange-400",
  frontend: "bg-pink-400",
  deprecated: "bg-slate-400",
};

type SkillCardProps = {
  skill: SkillAnalytics;
  checked: boolean;
  onCheck: (checked: boolean) => void;
  anyChecked: boolean;
};

function formatRelativeTime(dateString: string): string {
  const now = Date.now();
  const then = new Date(dateString).getTime();
  const diffMs = now - then;
  const diffMin = Math.floor(diffMs / 60_000);
  const diffHr = Math.floor(diffMs / 3_600_000);
  const diffDay = Math.floor(diffMs / 86_400_000);

  if (diffMin < 1) return "just now";
  if (diffMin < 60) return `${diffMin}m ago`;
  if (diffHr < 24) return `${diffHr}h ago`;
  if (diffDay < 7) return `${diffDay}d ago`;
  if (diffDay < 30) return `${Math.floor(diffDay / 7)}w ago`;
  if (diffDay < 365) return `${Math.floor(diffDay / 30)}mo ago`;
  return `${Math.floor(diffDay / 365)}y ago`;
}

function getFirstTag(skill: SkillAnalytics): string | null {
  // SkillAnalytics doesn't expose tags directly.
  // Attempt to extract from description keywords as a lightweight heuristic.
  const known = Object.keys(TAG_COLORS);
  for (const tag of known) {
    if (skill.name.includes(tag) || (skill.description ?? "").toLowerCase().includes(tag)) {
      return tag;
    }
  }
  return null;
}

function isActive(lastTriggered: string | null): boolean {
  if (!lastTriggered) return false;
  const daysSince =
    (Date.now() - new Date(lastTriggered).getTime()) / 86_400_000;
  return daysSince < 14;
}

export function SkillCard({
  skill,
  checked,
  onCheck,
  anyChecked,
}: SkillCardProps) {
  const navigate = useNavigate();
  const tag = getFirstTag(skill as SkillAnalytics);
  const active = isActive(skill.last_triggered);
  const status = active ? "active" : "stale";

  return (
    <div
      onClick={() => navigate(`/skills/${skill.name}`)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          navigate(`/skills/${skill.name}`);
        }
      }}
      tabIndex={0}
      className={cn(
        "group relative grid items-center gap-4 border-b border-border px-3.5 py-[15px] cursor-pointer transition-colors duration-150",
        "hover:bg-bg-secondary",
        "before:absolute before:left-0 before:top-1/2 before:-translate-y-1/2 before:w-0.5 before:h-0 before:bg-accent before:rounded-sm before:transition-all before:duration-200",
        "hover:before:h-[60%]",
        "focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-border-active"
      )}
      style={{
        gridTemplateColumns: "28px 12px 1fr 80px 80px 110px",
      }}
    >
      {/* Checkbox */}
      <div
        className={cn(
          "flex items-center justify-center transition-opacity duration-150",
          anyChecked ? "opacity-100" : "opacity-0 group-hover:opacity-100"
        )}
        onClick={(e) => e.stopPropagation()}
      >
        <Checkbox
          checked={checked}
          onCheckedChange={(v) => onCheck(v === true)}
        />
      </div>

      {/* Status dot */}
      <div className="flex items-center justify-center">
        <span
          className={cn(
            "size-1.5 rounded-full shrink-0",
            status === "active" ? "bg-accent" : "bg-warning"
          )}
          title={status}
        />
      </div>

      {/* Name + tag + description */}
      <div className="flex flex-col gap-1 min-w-0">
        <div className="flex items-center gap-3 flex-wrap">
          <span className="font-mono font-medium text-[13px] text-text-primary whitespace-nowrap">
            {skill.name}
          </span>
          {tag && (
            <span className="inline-flex items-center gap-1.5 text-[11px] text-text-secondary whitespace-nowrap">
              <span
                className={cn(
                  "size-[5px] rounded-full shrink-0",
                  TAG_COLORS[tag] ?? "bg-text-tertiary"
                )}
              />
              {tag}
            </span>
          )}
        </div>
        <span className="text-xs text-text-tertiary truncate min-w-0">
          {skill.description}
        </span>
      </div>

      {/* Invocations */}
      <span className="text-[13px] text-text-primary text-right" style={{ fontVariantNumeric: "tabular-nums" }}>
        {skill.activations.toLocaleString()}
      </span>

      {/* Security + review */}
      <div className="flex items-center justify-end gap-2">
        <SecurityBadge status={skill.security_status} showLabel />
        <ReviewStatus reviewedAt={skill.reviewed_at} />
      </div>

      {/* Version + time */}
      <span className="text-[11px] text-text-tertiary text-right whitespace-nowrap">
        v{skill.latest_version} · {formatRelativeTime(skill.updated_at)}
      </span>
    </div>
  );
}
