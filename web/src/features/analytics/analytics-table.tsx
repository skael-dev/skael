import { useState } from "react";
import { Link } from "react-router-dom";
import { ArrowUp, ArrowDown } from "lucide-react";
import {
  Table,
  TableHeader,
  TableBody,
  TableHead,
  TableRow,
  TableCell,
} from "@/components/ui/table";
import { SecurityBadge } from "@/features/security/security-badge";
import { ReviewStatus } from "@/features/security/review-status";
import type { SkillAnalytics } from "@/api/types.gen";
import { cn } from "@/lib/utils";

// ── Relative time ──────────────────────────────────────────────
function formatRelativeTime(dateStr: string | null): string {
  if (!dateStr) return "—";
  const now = Date.now();
  const then = new Date(dateStr).getTime();
  const diffMs = now - then;
  const diffSec = Math.floor(diffMs / 1000);
  if (diffSec < 60) return "just now";
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  const diffDay = Math.floor(diffHr / 24);
  if (diffDay < 7) return `${diffDay}d ago`;
  const diffWk = Math.floor(diffDay / 7);
  if (diffWk < 5) return `${diffWk}w ago`;
  const diffMo = Math.floor(diffDay / 30);
  return `${diffMo}mo ago`;
}

// ── Sort state ─────────────────────────────────────────────────
type SortKey = "name" | "activations" | "unique_devs" | "last_triggered" | "security_status";
type SortDir = "asc" | "desc";

function sortSkills(
  skills: SkillAnalytics[],
  key: SortKey,
  dir: SortDir
): SkillAnalytics[] {
  const sorted = [...skills].sort((a, b) => {
    let cmp = 0;
    switch (key) {
      case "name":
        cmp = a.name.localeCompare(b.name);
        break;
      case "activations":
        cmp = a.activations - b.activations;
        break;
      case "unique_devs":
        cmp = a.unique_devs - b.unique_devs;
        break;
      case "last_triggered": {
        const ta = a.last_triggered ? new Date(a.last_triggered).getTime() : 0;
        const tb = b.last_triggered ? new Date(b.last_triggered).getTime() : 0;
        cmp = ta - tb;
        break;
      }
      case "security_status":
        cmp = a.security_status.localeCompare(b.security_status);
        break;
    }
    return dir === "asc" ? cmp : -cmp;
  });
  return sorted;
}

// ── Sortable column header ─────────────────────────────────────
function SortableHead({
  col,
  label,
  active,
  dir,
  onSort,
  className,
}: {
  col: SortKey;
  label: string;
  active: boolean;
  dir: SortDir;
  onSort: (col: SortKey) => void;
  className?: string;
}) {
  const Arrow = dir === "asc" ? ArrowUp : ArrowDown;
  return (
    <TableHead
      className={cn(
        "text-[10px] uppercase tracking-widest text-text-tertiary select-none cursor-pointer hover:text-text-secondary transition-colors",
        className
      )}
      onClick={() => onSort(col)}
    >
      <span className="inline-flex items-center gap-1">
        {label}
        {active && <Arrow className="size-3 text-text-secondary" />}
      </span>
    </TableHead>
  );
}

// ── Main component ─────────────────────────────────────────────
type AnalyticsTableProps = {
  skills: SkillAnalytics[];
};

export function AnalyticsTable({ skills }: AnalyticsTableProps) {
  const [sortKey, setSortKey] = useState<SortKey>("activations");
  const [sortDir, setSortDir] = useState<SortDir>("desc");

  function handleSort(col: SortKey) {
    if (col === sortKey) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(col);
      setSortDir("desc");
    }
  }

  const sorted = sortSkills(skills, sortKey, sortDir);

  const headProps = (col: SortKey) => ({
    col,
    active: sortKey === col,
    dir: sortDir,
    onSort: handleSort,
  });

  return (
    <Table>
      <TableHeader>
        <TableRow className="border-border hover:bg-transparent">
          <SortableHead {...headProps("name")} label="Skill" className="pl-0" />
          <SortableHead {...headProps("activations")} label="Activations" className="text-right" />
          <SortableHead {...headProps("unique_devs")} label="Devs" className="text-right" />
          <SortableHead {...headProps("last_triggered")} label="Last triggered" />
          <SortableHead {...headProps("security_status")} label="Security" />
        </TableRow>
      </TableHeader>
      <TableBody>
        {sorted.map((skill) => {
          const isDead = skill.activations === 0;
          return (
            <TableRow
              key={skill.name}
              className={cn(
                "border-border hover:bg-bg-secondary transition-colors",
                isDead && "opacity-50"
              )}
            >
              {/* Skill name */}
              <TableCell className="pl-0 py-3">
                <Link
                  to={`/skills/${skill.name}`}
                  className="font-mono text-[13px] text-text-primary hover:text-accent transition-colors"
                >
                  {skill.name}
                </Link>
                {skill.description && (
                  <div className="text-[11px] text-text-tertiary mt-0.5 font-sans truncate max-w-[280px]">
                    {skill.description}
                  </div>
                )}
              </TableCell>

              {/* Activations */}
              <TableCell className="text-right font-mono tabular-nums text-[13px] text-text-secondary">
                {skill.activations.toLocaleString()}
              </TableCell>

              {/* Devs */}
              <TableCell className="text-right font-mono tabular-nums text-[13px] text-text-secondary">
                {skill.unique_devs}
              </TableCell>

              {/* Last triggered */}
              <TableCell className="text-[12px] text-text-secondary">
                {formatRelativeTime(skill.last_triggered)}
              </TableCell>

              {/* Security */}
              <TableCell>
                <div className="flex items-center gap-2">
                  <SecurityBadge status={skill.security_status} showLabel />
                  <ReviewStatus reviewedAt={skill.reviewed_at} />
                </div>
              </TableCell>
            </TableRow>
          );
        })}

        {sorted.length === 0 && (
          <TableRow className="hover:bg-transparent">
            <TableCell colSpan={5} className="text-center py-16 text-text-tertiary text-sm">
              No skills data available for this period
            </TableCell>
          </TableRow>
        )}
      </TableBody>
    </Table>
  );
}
