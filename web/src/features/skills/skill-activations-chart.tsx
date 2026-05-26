import { useQuery } from "@tanstack/react-query";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  type ChartConfig,
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";

type AgentDailyRow = Record<string, string | number>;

const AGENT_COLORS: Record<string, string> = {
  "claude-code": "var(--color-chart-1)",
  codex: "var(--color-chart-2)",
  cursor: "var(--color-chart-3)",
  opencode: "var(--color-chart-4)",
};

function getAgentColor(agent: string, index: number): string {
  return AGENT_COLORS[agent] ?? `var(--color-chart-${(index % 4) + 1})`;
}

async function fetchSkillTimeSeries(
  name: string,
  days: number
): Promise<AgentDailyRow[]> {
  const res = await fetch(
    `/api/skills/${encodeURIComponent(name)}/timeseries?days=${days}`,
    { credentials: "include" }
  );
  if (!res.ok) return [];
  return res.json();
}

function extractAgents(series: AgentDailyRow[]): string[] {
  const agentSet = new Set<string>();
  for (const row of series) {
    for (const key of Object.keys(row)) {
      if (key !== "date") agentSet.add(key);
    }
  }
  return Array.from(agentSet).sort();
}

export function SkillActivationsChart({
  skillName,
  days,
}: {
  skillName: string;
  days: number;
}) {
  const { data, isLoading } = useQuery({
    queryKey: ["skill-timeseries", skillName, days],
    queryFn: () => fetchSkillTimeSeries(skillName, days),
  });

  if (isLoading) {
    return (
      <div className="h-[200px] bg-bg-secondary border border-border rounded-lg animate-pulse-soft mb-6" />
    );
  }

  const series = data ?? [];
  const agents = extractAgents(series);
  const hasData = series.some((row) =>
    agents.some((a) => (row[a] as number) > 0)
  );

  if (!hasData) {
    return (
      <div className="h-[200px] bg-bg-secondary border border-border rounded-lg flex items-center justify-center mb-6">
        <p className="text-sm text-text-tertiary">
          No activation data for this period
        </p>
      </div>
    );
  }

  const chartConfig: ChartConfig = {};
  agents.forEach((agent, i) => {
    chartConfig[agent] = {
      label: agent,
      color: getAgentColor(agent, i),
    };
  });

  return (
    <div className="bg-bg-secondary border border-border rounded-lg p-4 mb-6">
      <div className="flex items-center justify-between mb-3">
        <div className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary">
          Activations by agent
        </div>
        <div className="flex items-center gap-3">
          {agents.map((agent, i) => (
            <div key={agent} className="flex items-center gap-1.5">
              <div
                className="size-2 rounded-full"
                style={{ backgroundColor: getAgentColor(agent, i) }}
              />
              <span className="text-[11px] text-text-tertiary">{agent}</span>
            </div>
          ))}
        </div>
      </div>
      <ChartContainer config={chartConfig} className="h-[180px] w-full">
        <AreaChart
          accessibilityLayer
          data={series}
          margin={{ left: 0, right: 8, top: 4, bottom: 0 }}
        >
          <defs>
            {agents.map((agent) => (
              <linearGradient
                key={agent}
                id={`fill-${agent}`}
                x1="0"
                y1="0"
                x2="0"
                y2="1"
              >
                <stop
                  offset="5%"
                  stopColor={`var(--color-${agent})`}
                  stopOpacity={0.5}
                />
                <stop
                  offset="95%"
                  stopColor={`var(--color-${agent})`}
                  stopOpacity={0}
                />
              </linearGradient>
            ))}
          </defs>
          <CartesianGrid
            vertical={false}
            stroke="var(--color-border)"
            strokeDasharray="3 3"
          />
          <XAxis
            dataKey="date"
            tickLine={false}
            axisLine={false}
            tickMargin={8}
            minTickGap={40}
            tick={{ fontSize: 11, fill: "var(--color-text-tertiary)" }}
            tickFormatter={(value: string) => {
              const d = new Date(value + "T00:00:00");
              return d.toLocaleDateString("en-US", {
                month: "short",
                day: "numeric",
              });
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
                indicator="dot"
                labelFormatter={(value: string) =>
                  new Date(value + "T00:00:00").toLocaleDateString("en-US", {
                    weekday: "short",
                    month: "short",
                    day: "numeric",
                  })
                }
              />
            }
          />
          {agents.map((agent) => (
            <Area
              key={agent}
              dataKey={agent}
              type="monotone"
              fill={`url(#fill-${agent})`}
              stroke={`var(--color-${agent})`}
              strokeWidth={2}
              stackId="agents"
            />
          ))}
        </AreaChart>
      </ChartContainer>
    </div>
  );
}
