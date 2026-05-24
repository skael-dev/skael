import type { Version } from "@/api/types.gen";
import { cn } from "@/lib/utils";

// ── Helpers ──────────────────────────────────────────────────────

function formatRelativeTime(dateString: string): string {
  const now = Date.now();
  const then = new Date(dateString).getTime();
  const diffMs = now - then;
  const diffDay = Math.floor(diffMs / 86_400_000);
  if (diffDay < 1) {
    const diffHr = Math.floor(diffMs / 3_600_000);
    if (diffHr < 1) return "just now";
    return `${diffHr}h ago`;
  }
  if (diffDay < 7) return `${diffDay}d ago`;
  if (diffDay < 30) return `${Math.floor(diffDay / 7)}w ago`;
  if (diffDay < 365) return `${Math.floor(diffDay / 30)}mo ago`;
  return `${Math.floor(diffDay / 365)}y ago`;
}

// ── VersionList ──────────────────────────────────────────────────

type VersionListProps = {
  versions: Version[];
};

export function VersionList({ versions }: VersionListProps) {
  if (versions.length === 0) {
    return (
      <div className="text-text-tertiary text-sm py-12 text-center">
        No versions published yet.
      </div>
    );
  }

  // Versions should already be ordered desc by version number from the API,
  // but sort to be safe
  const sorted = [...versions].sort((a, b) => b.version - a.version);

  return (
    <div className="max-w-[720px]">
      <div className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary mb-4">
        {sorted.length} version{sorted.length !== 1 ? "s" : ""}
      </div>

      <div className="relative">
        {/* Vertical timeline line */}
        <div
          className="absolute left-[11px] top-2 bottom-2 w-px bg-border"
          aria-hidden
        />

        {sorted.map((ver, i) => {
          const isLatest = i === 0;
          return (
            <div
              key={ver.id}
              className="flex gap-4 py-3.5 relative"
            >
              {/* Timeline dot */}
              <div
                className={cn(
                  "size-[22px] rounded-full shrink-0 z-[1] flex items-center justify-center text-[10px] font-semibold",
                  isLatest
                    ? "bg-accent border-2 border-accent text-bg-primary shadow-[0_0_12px_var(--color-accent-surface)]"
                    : "bg-bg-secondary border-2 border-border text-text-tertiary"
                )}
              >
                {isLatest ? "★" : ""}
              </div>

              {/* Content */}
              <div className="flex-1 pt-px min-w-0">
                {/* Version header */}
                <div className="flex items-baseline gap-2.5 mb-1 flex-wrap">
                  <span className="font-mono text-sm font-medium text-text-primary">
                    v{ver.version}
                  </span>
                  {isLatest && (
                    <span className="text-[9px] text-accent bg-accent/10 px-1.5 py-px rounded uppercase tracking-[0.04em]">
                      current
                    </span>
                  )}
                  {ver.published_by && (
                    <span className="text-xs text-text-secondary">
                      by {ver.published_by}
                    </span>
                  )}
                  <span className="text-[11px] text-text-tertiary">
                    · {formatRelativeTime(ver.created_at)}
                  </span>
                </div>

                {/* Changelog */}
                {ver.changelog && (
                  <div className="text-[13px] text-text-secondary leading-relaxed mb-2">
                    {ver.changelog}
                  </div>
                )}

                {/* File count */}
                {ver.file_manifest && ver.file_manifest.length > 0 && (
                  <div className="flex items-center gap-3">
                    <span className="font-mono text-[11px] text-text-tertiary">
                      {ver.file_manifest.length} file{ver.file_manifest.length !== 1 ? "s" : ""}
                    </span>
                  </div>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
