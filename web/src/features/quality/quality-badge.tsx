import { cn } from "@/lib/utils";
import type { QualitySummary } from "@/api/types.gen";

export type QualityBadgeProps = {
  quality?: QualitySummary | null;
  latestVersion: number;
  showLabel?: boolean;
};

// Colour bands describe the score, not a policy: the engine defines no
// thresholds, so these are read-aids and nothing keys a decision on them.
function trackColor(score: number): string {
  if (score >= 70) return "bg-accent";
  if (score >= 40) return "bg-warning";
  return "bg-danger";
}

export function QualityBadge({ quality, latestVersion, showLabel = false }: QualityBadgeProps) {
  // Unscored is the common case initially and must read neutral. A zero here
  // would say "measured, and bad" about something never measured at all.
  if (!quality) {
    return (
      <span
        className="inline-flex items-center gap-1.5 text-[11px] text-text-tertiary"
        title="Not yet scored"
      >
        —{showLabel && <span>Not scored</span>}
      </span>
    );
  }

  const score = Math.round(quality.headline_score);
  const stale = quality.version < latestVersion;
  // A version can be scored but not (yet, or no longer) the one actually
  // served — most visibly a skill whose only version is held for review:
  // latestVersion is 0 (nothing released) while quality.version is 1, so
  // stale (version < latestVersion) is false even though the scored bundle
  // is unservable. Render this distinctly from both "current" and "stale"
  // rather than falling through to a confident "current" badge.
  const notServed = quality.version > latestVersion;

  // An incomplete panel means the minimum was taken over fewer members than
  // intended. That is a measurement we could not complete, not a bad one.
  if (!quality.panel_complete) {
    return (
      <span
        className="inline-flex items-center gap-1 text-[11px] text-text-tertiary"
        title="Incomplete panel — not a final score"
      >
        ~{score}
        {showLabel && <span>Incomplete</span>}
      </span>
    );
  }

  const verifiedLabel = quality.verified ? "Verified" : "Attested, not verified";
  const title = notServed
    ? `${verifiedLabel} · scored on v${quality.version} · not currently served` +
      (latestVersion > 0 ? ` (current v${latestVersion})` : " (nothing released yet)")
    : stale
      ? `${verifiedLabel} · scored on v${quality.version} · current v${latestVersion}`
      : quality.verified
        ? `Verified score`
        : `Attested, not verified`;

  return (
    <span className="inline-flex items-center gap-1.5 text-[11px]" title={title}>
      <span className="font-mono tabular-nums text-text-primary">{score}</span>
      <span
        // Verified vs attested is carried by track weight rather than colour,
        // so the distinction survives a colourblind reader and greyscale.
        data-track={quality.verified ? "solid" : "hairline"}
        className={cn(
          "relative h-1 w-6 shrink-0 overflow-hidden rounded-full",
          quality.verified ? "bg-bg-tertiary" : "bg-transparent border border-border",
        )}
      >
        <span
          className={cn("absolute inset-y-0 left-0", trackColor(score))}
          style={{ width: `${Math.max(0, Math.min(100, score))}%` }}
        />
      </span>
      {notServed ? (
        <span className="text-[9px] text-warning" aria-hidden="true">
          ⚠
        </span>
      ) : (
        stale && (
          <span className="text-[9px] text-text-tertiary" aria-hidden="true">
            ↑
          </span>
        )
      )}
      {showLabel && <span className="text-text-secondary">{quality.verified ? "Verified" : "Attested"}</span>}
    </span>
  );
}
