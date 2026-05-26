import { useQuery } from "@tanstack/react-query";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";

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

async function fetchTimeSeries(days: number): Promise<DailyCount[]> {
  const res = await fetch(`/api/analytics/timeseries?days=${days}`, {
    credentials: "include",
  });
  if (!res.ok) return [];
  return res.json();
}

export function ActivationsChart({ days }: { days: number }) {
  const { data, isLoading } = useQuery({
    queryKey: ["analytics", "timeseries", days],
    queryFn: () => fetchTimeSeries(days),
  });

  if (isLoading) {
    return (
      <div className="h-[200px] bg-bg-secondary border border-border rounded-lg animate-pulse-soft" />
    );
  }

  const series = data ?? [];
  const hasData = series.some((d) => d.count > 0);

  if (!hasData) {
    return (
      <div className="h-[200px] bg-bg-secondary border border-border rounded-lg flex items-center justify-center">
        <p className="text-sm text-text-tertiary">No activation data for this period</p>
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
