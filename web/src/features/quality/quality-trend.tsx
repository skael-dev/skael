import { useQuery } from "@tanstack/react-query";
import { getSkillQualitySeries } from "@/api/sdk.gen";
import type { Series } from "@/api/types.gen";

// The server groups scores into comparable series: same suite, panel, tier,
// engine version and agent CLI version. Only the CURRENT series is ever
// charted — mixing incomparable scores into one line would make a movement
// look like a skill regression when it was really a panel change. Earlier
// series are listed collapsed, each with the engine's own reason (rendered
// verbatim — never paraphrased here, which would create a second definition
// of comparability in the UI).

const CHART_WIDTH = 300;
const CHART_HEIGHT = 150;
const CHART_PAD = 8;

function buildPath(points: { version: number; headline_score: number }[]) {
  const scores = points.map((p) => p.headline_score);
  const min = Math.min(...scores, 0);
  const max = Math.max(...scores, 100);
  const range = max - min || 1;
  const innerWidth = CHART_WIDTH - CHART_PAD * 2;
  const innerHeight = CHART_HEIGHT - CHART_PAD * 2;

  return points.map((p, i) => {
    const x =
      points.length === 1
        ? CHART_PAD
        : CHART_PAD + (i / (points.length - 1)) * innerWidth;
    const y =
      CHART_PAD + innerHeight - ((p.headline_score - min) / range) * innerHeight;
    return { ...p, x, y };
  });
}

function CurrentSeriesChart({ points }: { points: Series["points"] }) {
  const pts = points ?? [];

  if (pts.length === 0) {
    return <div className="text-[11px] text-text-tertiary">No scores yet.</div>;
  }

  if (pts.length === 1) {
    const [p] = pts;
    if (!p) return null;
    return (
      <div className="text-[11px] text-text-secondary">
        One score so far — not enough to plot a trend.{" "}
        <span data-point title={`v${p.version} · ${Math.round(p.headline_score)}`}>
          v{p.version} · {Math.round(p.headline_score)}
        </span>
      </div>
    );
  }

  const plotted = buildPath(pts);
  const linePath = plotted.map((p) => `${p.x},${p.y}`).join(" ");

  return (
    <div className="bg-bg-secondary border border-border rounded-lg p-3 mb-2">
      <svg
        viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`}
        className="w-full h-[150px]"
        preserveAspectRatio="none"
      >
        <polyline
          points={linePath}
          fill="none"
          stroke="var(--color-chart-1, currentColor)"
          strokeWidth={2}
        />
        {plotted.map((p) => (
          <circle
            key={p.version}
            data-point
            cx={p.x}
            cy={p.y}
            r={3}
            fill="var(--color-chart-1, currentColor)"
          >
            <title>{`v${p.version} · ${Math.round(p.headline_score)}`}</title>
          </circle>
        ))}
      </svg>
    </div>
  );
}

export function QualityTrend({ skillName }: { skillName: string }) {
  const seriesQuery = useQuery({
    queryKey: ["skill-quality-series", skillName],
    queryFn: async () => {
      const res = await getSkillQualitySeries({ path: { name: skillName } });
      if (!res.data) throw new Error("Failed to load quality series");
      return res.data;
    },
  });

  if (seriesQuery.isLoading) {
    return (
      <div className="h-[150px] bg-bg-secondary border border-border rounded-lg animate-pulse-soft mb-6" />
    );
  }

  if (seriesQuery.isError) {
    return (
      <div className="text-sm text-danger mb-6">Could not load the quality trend.</div>
    );
  }

  const series = seriesQuery.data?.series ?? [];

  if (series.length === 0) {
    return (
      <div className="text-[11px] text-text-tertiary mb-6">No scores yet.</div>
    );
  }

  // Select by the explicit `current` flag, not position. The contract
  // promises the current series comes first, but trusting position anyway
  // is a needless bet: if that ordering were ever violated upstream, this
  // component would silently chart a non-current — possibly incomparable —
  // series and look completely normal while doing it. If nothing is
  // flagged current, that's a server contract violation; charting an
  // arbitrary series would be a confident wrong answer, so fall back to the
  // same quiet no-trend state used for empty history instead.
  const current = series.find((s) => s.current);
  const earlier = series.filter((s) => s !== current);

  if (!current) {
    return (
      <div className="text-[11px] text-text-tertiary mb-6">No scores yet.</div>
    );
  }

  return (
    <div className="mb-6">
      <CurrentSeriesChart points={current.points} />
      {earlier.map((s) => {
        const count = s.points?.length ?? 0;
        return (
          <details key={s.key} className="text-[11px] text-text-tertiary">
            <summary>
              {count} earlier score{count === 1 ? "" : "s"} not comparable
            </summary>
            {/* The engine's own words. Paraphrasing here would put a second
                definition of comparability in the UI. */}
            <p className="mt-1">{s.reason}</p>
          </details>
        );
      })}
    </div>
  );
}
