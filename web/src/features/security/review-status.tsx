import { Check, Circle } from "lucide-react";

type ReviewStatusProps = {
  reviewedAt: string | null;
};

export function ReviewStatus({ reviewedAt }: ReviewStatusProps) {
  if (reviewedAt) {
    return (
      <span className="inline-flex items-center gap-1 text-accent" title="Reviewed">
        <Check className="size-3.5" />
      </span>
    );
  }

  return (
    <span className="inline-flex items-center gap-1 text-text-tertiary" title="Unreviewed">
      <Circle className="size-3" />
    </span>
  );
}
