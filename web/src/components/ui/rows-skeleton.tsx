import { Skeleton } from "@/components/ui/skeleton";

export function RowsSkeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div role="status" aria-label="Loading" aria-busy="true">
      {Array.from({ length: rows }).map((_, i) => (
        <div
          key={i}
          className="flex items-center gap-3 px-3.5 py-3 border-b border-border last:border-b-0"
        >
          <Skeleton className="h-3.5 w-1/3" />
          <Skeleton className="h-3.5 w-1/5 ml-auto" />
        </div>
      ))}
    </div>
  );
}
