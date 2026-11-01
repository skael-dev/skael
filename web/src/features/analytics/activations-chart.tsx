import { useQuery } from "@tanstack/react-query";
import { prefersReducedMotion } from "@/lib/motion";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import type { AnalyticsFilter } from "./analytics-filters";

type DailyCount = {
  date: string;
  count: number;
};

const chartConfig = {
  count: {
    label: "Activations",
    color: "var(--color-chart-1)",
  },
} satisfies ChartConfig;

async function fetchTimeSeries(days: number, filter: AnalyticsFilter): Promise<DailyCount[]> {
  const query = new URLSearchParams({ days: String(days), ...filter });
  const res = await fetch(`/api/analytics/timeseries?${query}`, {
    credentials: "include",
  });
  if (!res.ok) throw new Error("Failed to load the activations chart");
  return res.json();
}

export function ActivationsChart({ days, filter }: { days: number; filter: AnalyticsFilter }) {
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ["analytics", "timeseries", days, filter],
    queryFn: () => fetchTimeSeries(days, filter),
  });

  if (isLoading) {
    return (
      <div className="h-[200px] bg-bg-secondary border border-border rounded-lg animate-pulse-soft" />
    );
  }

  if (isError) {
    return (
      <div className="h-[200px] bg-bg-secondary border border-border rounded-lg flex flex-wrap items-center justify-center gap-x-3 gap-y-2 px-4 text-center">
        <p className="text-sm text-text-tertiary">Couldn't load the chart. Check that the server is reachable, then retry.</p>
        <button onClick={() => refetch()} className="text-accent text-sm hover:underline">
          Retry
        </button>
      </div>
    );
  }

  const series = data ?? [];
  const hasData = series.some((d) => d.count > 0);

  if (!hasData) {
    return (
      <div className="h-[200px] bg-bg-secondary border border-border rounded-lg flex items-center justify-center px-4 text-center">
        <p className="text-sm text-text-tertiary">
          {Object.keys(filter).length > 0
            ? "No activations match this filter in this period. Clear the filter or select a longer period."
            : "No activation data for this period. Select a longer period."}
        </p>
      </div>
    );
  }

  return (
    <div className="bg-bg-secondary border border-border rounded-lg p-4">
      <div className="text-[11px] text-text-tertiary uppercase tracking-widest mb-3">
        Activations over time
      </div>
      <ChartContainer config={chartConfig} className="h-[180px] w-full">
        <AreaChart
          accessibilityLayer
          data={series}
          margin={{ left: 0, right: 8, top: 4, bottom: 0 }}
        >
          <defs>
            <linearGradient id="fillActivations" x1="0" y1="0" x2="0" y2="1">
              <stop offset="5%" stopColor="var(--color-count)" stopOpacity={0.5} />
              <stop offset="95%" stopColor="var(--color-count)" stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid vertical={false} stroke="var(--color-border)" strokeDasharray="3 3" />
          <XAxis
            dataKey="date"
            tickLine={false}
            axisLine={false}
            tickMargin={8}
            minTickGap={40}
            tick={{ fontSize: 11, fill: "var(--color-text-tertiary)" }}
            tickFormatter={(value: string) => {
              const d = new Date(value + "T00:00:00");
              return d.toLocaleDateString("en-US", { month: "short", day: "numeric" });
            }}
          />
          <YAxis
            tickLine={false}
            axisLine={false}
            width={32}
            tick={{ fontSize: 11, fill: "var(--color-text-tertiary)" }}
            allowDecimals={false}
          />
          <ChartTooltip
            cursor={false}
            content={
              <ChartTooltipContent
                indicator="line"
                labelFormatter={(value) =>
                  new Date(String(value) + "T00:00:00").toLocaleDateString("en-US", {
                    weekday: "short",
                    month: "short",
                    day: "numeric",
                  })
                }
              />
            }
          />
          <Area
            isAnimationActive={!prefersReducedMotion()}
            dataKey="count"
            type="monotone"
            fill="url(#fillActivations)"
            stroke="var(--color-count)"
            strokeWidth={2}
          />
        </AreaChart>
      </ChartContainer>
    </div>
  );
}
