import { cn } from "@/lib/utils";

export function EmptyState({
  message,
  action,
  className,
}: {
  message: string;
  action?: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-wrap items-center justify-center gap-x-3 gap-y-2 px-3.5 py-8 text-center",
        className,
      )}
    >
      <span className="text-[13px] text-text-secondary">{message}</span>
      {action}
    </div>
  );
}
