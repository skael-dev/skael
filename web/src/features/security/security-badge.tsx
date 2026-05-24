import { cn } from "@/lib/utils";

type StatusInfo = { color: string; label: string };

const statusConfig = new Map<string, StatusInfo>([
  ["clean", { color: "bg-accent", label: "Clean" }],
  ["info", { color: "bg-text-tertiary", label: "Info" }],
  ["warn", { color: "bg-warning", label: "Warning" }],
  ["critical", { color: "bg-danger", label: "Critical" }],
]);

const fallback: StatusInfo = { color: "bg-text-tertiary", label: "Info" };

type SecurityBadgeProps = {
  status: string;
  showLabel?: boolean;
};

export function SecurityBadge({ status, showLabel = false }: SecurityBadgeProps) {
  const config = statusConfig.get(status) ?? fallback;

  return (
    <span className="inline-flex items-center gap-1.5">
      <span
        className={cn("size-1.5 shrink-0 rounded-full", config.color)}
        title={config.label}
      />
      {showLabel && (
        <span className="text-[11px] text-text-secondary">{config.label}</span>
      )}
    </span>
  );
}
