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

type Point = NonNullable<Series["points"]>[number];

// The headline and the lift are both 0-100 point scales, so they share one
// axis. The domain is anchored to [0, 100] and widened only far enough to hold
// a negative lift.
function scaleFor(points: Point[]) {
  const values = points.flatMap((p) =>
    p.lift === undefined || p.lift === null
      ? [p.headline_score]
      : [p.headline_score, p.lift],
  );
  const min = Math.min(...values, 0);
  const max = Math.max(...values, 100);
  const range = max - min || 1;
  const innerWidth = CHART_WIDTH - CHART_PAD * 2;
  const innerHeight = CHART_HEIGHT - CHART_PAD * 2;

  return {
    x: (i: number) =>
      points.length === 1
        ? CHART_PAD
        : CHART_PAD + (i / (points.length - 1)) * innerWidth,
    y: (v: number) => CHART_PAD + innerHeight - ((v - min) / range) * innerHeight,
  };
}

function buildPath(points: Point[]) {
  const scale = scaleFor(points);
  return points.map((p, i) => ({ ...p, x: scale.x(i), y: scale.y(p.headline_score) }));
}

// A version scored before lift existed, and one whose tier ran no baseline,
// both carry no lift. The line breaks there rather than dropping to zero, so a
// gap reads as "not measured" and never as "did not help".
function liftSegments(points: Point[]) {
  const scale = scaleFor(points);
  type Plotted = { x: number; y: number; version: number; lift: number };
  const segments: Plotted[][] = [];
  let run: Plotted[] = [];

  points.forEach((p, i) => {
    if (p.lift === undefined || p.lift === null) {
      if (run.length > 0) segments.push(run);
      run = [];
      return;
    }
    run.push({ x: scale.x(i), y: scale.y(p.lift), version: p.version, lift: p.lift });
  });
  if (run.length > 0) segments.push(run);
  return segments;
}

function formatLift(lift: number) {
  return `${lift >= 0 ? "+" : "\u2212"}${Math.abs(Math.round(lift))}`;
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
  const lifts = liftSegments(pts);

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
        {lifts.map((segment, i) => (
          <polyline
            key={`lift-${i}`}
            data-lift-segment
            points={segment.map((p) => `${p.x},${p.y}`).join(" ")}
            fill="none"
            stroke="var(--color-chart-2, currentColor)"
            strokeWidth={2}
            strokeDasharray="4 3"
          />
        ))}
        {lifts.flat().map((p) => (
          <circle
            key={`lift-${p.version}`}
            data-lift-point
            cx={p.x}
            cy={p.y}
            r={3}
            fill="var(--color-chart-2, currentColor)"
          >
            <title>{`v${p.version} · lift ${formatLift(p.lift)}`}</title>
          </circle>
        ))}
      </svg>
      <div className="mt-2 flex gap-4 text-[11px] text-text-tertiary">
        <span className="inline-flex items-center gap-1.5">
          <span
            className="inline-block w-3 h-0.5"
            style={{ background: "var(--color-chart-1, currentColor)" }}
          />
          Headline
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span
            className="inline-block w-3 h-0.5"
            style={{
              backgroundImage:
                "repeating-linear-gradient(to right, var(--color-chart-2, currentColor) 0 4px, transparent 4px 7px)",
            }}
          />
          Lift vs no skill
        </span>
      </div>
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
