import { useQuery } from "@tanstack/react-query";
import { Target } from "lucide-react";
import {
  CartesianGrid,
  ReferenceLine,
  Scatter,
  ScatterChart,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { analyticsSkills } from "@/api/sdk.gen";
import { rerunEval } from "@/api/sdk.gen";
import type { SkillAnalytics } from "@/api/types.gen";
import { ChartContainer, type ChartConfig } from "@/components/ui/chart";
import { QualityBadge } from "@/features/quality/quality-badge";

// analytics/skills' `limit` maxes at 100 server-side (internal/analytics/routes.go).
const MAX_LIMIT = 100;

// The true statistical median, no lean: the middle order statistic for an
// odd count, the mean of the two central values for an even count. This
// number is the claim "heavily used" rests on, so it must not be tuned to
// make any particular skill land on one side or the other.
export function median(values: number[]): number {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  // Even count: average the two middle values. Odd count: the middle value
  // itself. With one point, sorted.length === 1 and mid === 0, so this
  // returns that single point's activation count rather than NaN.
  return sorted.length % 2 === 0 ? (sorted[mid - 1] + sorted[mid]) / 2 : sorted[mid];
}

function RunEvalButton({ skillName }: { skillName: string }) {
  return (
    <button
      onClick={() => rerunEval({ path: { name: skillName }, body: {} })}
      className="text-[11px] text-accent hover:underline shrink-0"
    >
      Run eval
    </button>
  );
}

const chartConfig: ChartConfig = {
  activations: { label: "Activations", color: "var(--color-chart-1)" },
};

export function Quadrant() {
  const query = useQuery({
    queryKey: ["analytics", "quadrant"],
    queryFn: async () => {
      const res = await analyticsSkills({ query: { days: 30, limit: MAX_LIMIT } });
      if (res.error) throw res.error;
      return (res.data?.skills as SkillAnalytics[] | null) ?? [];
    },
  });

  if (query.isLoading) {
    return <div className="p-6 text-sm text-text-secondary">Loading…</div>;
  }

  if (query.isError) {
    return (
      <div className="p-6 text-sm text-text-secondary">
        Couldn't load analytics — is the server reachable?
      </div>
    );
  }

  const skills = query.data ?? [];

  // Three buckets, not two: an incomplete panel is neither a score nor an
  // absence of one, and folding it into either misreports it.
  const scored = skills.filter((s) => s.quality != null && s.quality.panel_complete);
  const incomplete = skills.filter((s) => s.quality != null && !s.quality.panel_complete);
  const unscored = skills.filter((s) => s.quality == null);

  const activationMedian = median(scored.map((s) => s.activations));

  // High-activation side is >= median (inclusive of the median itself), not
  // strictly >. Test data must therefore avoid landing a candidate exactly
  // on the median if the intent is to exercise exclusion — see
  // quadrant.test.tsx.
  const attention = scored
    .filter((s) => s.activations >= activationMedian && (s.quality!.headline_score ?? 0) < 50)
    .sort((a, b) => a.quality!.headline_score - b.quality!.headline_score);

  return (
    <div className="p-6 max-w-5xl mx-auto flex flex-col gap-6">
      <div>
        <div className="flex items-center gap-2">
          <Target size={18} className="text-accent" />
          <h1 className="text-lg font-medium text-text-primary">Activation × quality</h1>
        </div>
        <p className="text-xs text-text-tertiary mt-1 max-w-lg">
          Skills your team relies on daily, plotted against how well they measurably
          perform. High activation, low score is the report this product exists to
          produce.
        </p>
      </div>

      {scored.length === 0 ? (
        <div className="text-sm text-text-secondary py-8 text-center">
          No skills scored yet — run an evaluation to see them here.
        </div>
      ) : (
        <>
          <ChartContainer config={chartConfig} className="h-[360px] w-full">
            <ScatterChart margin={{ left: 8, right: 16, top: 16, bottom: 8 }}>
              <CartesianGrid stroke="var(--color-border)" strokeDasharray="3 3" />
              <XAxis
                type="number"
                dataKey="activations"
                name="Activations"
                tick={{ fontSize: 11, fill: "var(--color-text-tertiary)" }}
                tickLine={false}
                axisLine={false}
              />
              <YAxis
                type="number"
                dataKey="score"
                name="Score"
                domain={[0, 100]}
                tick={{ fontSize: 11, fill: "var(--color-text-tertiary)" }}
                tickLine={false}
                axisLine={false}
              />
              <ReferenceLine x={activationMedian} stroke="var(--color-border)" strokeDasharray="3 3" />
              <ReferenceLine y={50} stroke="var(--color-border)" strokeDasharray="3 3" />
              <Tooltip
                cursor={{ strokeDasharray: "3 3" }}
                content={({ active, payload }) => {
                  if (!active || !payload?.length) return null;
                  const p = payload[0].payload as { name: string; activations: number; score: number };
                  return (
                    <div className="rounded-md border border-border bg-bg-secondary px-2.5 py-1.5 text-[11px]">
                      <div className="text-text-primary font-medium">{p.name}</div>
                      <div className="text-text-tertiary">
                        {p.activations} activations · score {Math.round(p.score)}
                      </div>
                    </div>
                  );
                }}
              />
              <Scatter
                data={scored.map((s) => ({
                  name: s.name,
                  activations: s.activations,
                  score: s.quality!.headline_score,
                }))}
                fill="var(--color-chart-1)"
                shape={(props: unknown) => {
                  const { cx, cy, payload } = props as {
                    cx: number;
                    cy: number;
                    payload: { name: string; activations: number; score: number };
                  };
                  return (
                    <circle cx={cx} cy={cy} r={4} fill="var(--color-chart-1)" data-plotted="">
                      <title>
                        {payload.name} · {payload.activations} activations · score{" "}
                        {Math.round(payload.score)}
                      </title>
                    </circle>
                  );
                }}
              />
            </ScatterChart>
          </ChartContainer>

          {/* Plain-text list of every plotted skill, for screen readers and
              anything (including tests) that can't read SVG coordinates. */}
          <ul className="sr-only">
            {scored.map((s) => (
              <li key={s.name}>{s.name}</li>
            ))}
          </ul>

          <div>
            <div className="text-[11px] uppercase tracking-widest text-text-tertiary mb-2">
              High activation, low score
            </div>
            {attention.length === 0 ? (
              <div className="text-sm text-text-secondary">
                No high-activation skills are scoring low right now.
              </div>
            ) : (
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-left text-[11px] text-text-tertiary uppercase tracking-wide">
                    <th className="font-normal pb-2">Skill</th>
                    <th className="font-normal pb-2">Activations</th>
                    <th className="font-normal pb-2">Score</th>
                  </tr>
                </thead>
                <tbody>
                  {attention.map((s) => (
                    <tr key={s.name} data-testid="attention-row" className="border-t border-border">
                      <td className="py-2 text-text-primary">{s.name}</td>
                      <td className="py-2 text-text-secondary">{s.activations}</td>
                      <td className="py-2">
                        <QualityBadge quality={s.quality} latestVersion={s.latest_version} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </>
      )}

      <div className="grid grid-cols-2 gap-6 pt-4 border-t border-border">
        <div>
          <div className="text-[11px] uppercase tracking-widest text-text-tertiary mb-2">
            {unscored.length} skill{unscored.length === 1 ? "" : "s"} not scored
          </div>
          {unscored.length === 0 ? (
            <div className="text-xs text-text-tertiary">Every skill has been evaluated.</div>
          ) : (
            <ul className="flex flex-col gap-1.5">
              {unscored.map((s) => (
                <li key={s.name} className="flex items-center justify-between gap-2 text-sm">
                  <span className="text-text-primary">{s.name}</span>
                  <RunEvalButton skillName={s.name} />
                </li>
              ))}
            </ul>
          )}
        </div>

        <div>
          <div className="text-[11px] uppercase tracking-widest text-text-tertiary mb-2">
            {incomplete.length} incomplete
          </div>
          {incomplete.length === 0 ? (
            <div className="text-xs text-text-tertiary">No incomplete panels.</div>
          ) : (
            <ul className="flex flex-col gap-1.5">
              {incomplete.map((s) => (
                <li key={s.name} className="flex items-center justify-between gap-2 text-sm">
                  <span className="text-text-primary">{s.name}</span>
                  <RunEvalButton skillName={s.name} />
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}
