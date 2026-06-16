import { CheckCircle2, AlertCircle } from "lucide-react";

type SpecBadgeProps = {
  compliance?: string;
};

export function SpecBadge({ compliance }: SpecBadgeProps) {
  if (!compliance || compliance === "none") return null;

  if (compliance === "full") {
    return (
      <span
        className="inline-flex items-center gap-1 text-[11px] text-emerald-400"
        title="Full spec compliance"
      >
        <CheckCircle2 className="size-3.5" />
      </span>
    );
  }

  if (compliance === "partial") {
    return (
      <span
        className="inline-flex items-center gap-1 text-[11px] text-amber-400"
        title="Partial spec compliance"
      >
        <AlertCircle className="size-3.5" />
      </span>
    );
  }

  return null;
}
