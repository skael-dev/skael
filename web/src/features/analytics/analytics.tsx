import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { BarChart3 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { KpiStrip } from "./kpi-strip";
import { ActivationsChart } from "./activations-chart";
import { AnalyticsTable } from "./analytics-table";
import { analyticsOverview, analyticsSkills } from "@/api/sdk.gen";
import type { OverviewData, SkillAnalytics } from "@/api/types.gen";
import { cn } from "@/lib/utils";

const PERIOD_OPTIONS = [
  { label: "7d", value: 7 },
  { label: "30d", value: 30 },
  { label: "90d", value: 90 },
] as const;

type Days = typeof PERIOD_OPTIONS[number]["value"];

// ── Skeleton placeholders ──────────────────────────────────────
function KpiSkeleton() {
  return (
    <div className="grid grid-cols-4 gap-2.5">
      {Array.from({ length: 4 }).map((_, i) => (
        <div
          key={i}
          className="flex items-center gap-3.5 p-4 bg-bg-secondary border border-border rounded-lg"
        >
          <Skeleton className="size-9 rounded-[7px] bg-bg-tertiary" />
          <div className="flex-1 space-y-2">
            <Skeleton className="h-2 w-16 bg-bg-tertiary" />
            <Skeleton className="h-6 w-10 bg-bg-tertiary" />
          </div>
        </div>
      ))}
    </div>
  );
}

function TableSkeleton() {
  return (
    <div className="space-y-px">
      <div className="flex gap-4 py-2.5 border-b border-border">
        {[120, 80, 60, 90, 80].map((w, i) => (
          <Skeleton key={i} className={`h-2 bg-bg-tertiary`} style={{ width: w }} />
        ))}
      </div>
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className="flex gap-4 py-3 border-b border-border">
          <Skeleton className="h-4 w-32 bg-bg-tertiary" />
          <Skeleton className="h-4 w-14 ml-auto bg-bg-tertiary" />
          <Skeleton className="h-4 w-8 bg-bg-tertiary" />
          <Skeleton className="h-4 w-16 bg-bg-tertiary" />
          <Skeleton className="h-4 w-16 bg-bg-tertiary" />
        </div>
      ))}
    </div>
  );
}

// ── Main Analytics page ────────────────────────────────────────
export function Analytics() {
  const [days, setDays] = useState<Days>(30);

  const { data: overview, isLoading: overviewLoading } = useQuery({
    queryKey: ["analytics", "overview", days],
    queryFn: async () => {
      const res = await analyticsOverview({ query: { days } });
      return res.data as OverviewData | undefined;
    },
  });

  const { data: skills, isLoading: skillsLoading } = useQuery({
    queryKey: ["analytics", "skills", days],
    queryFn: async () => {
      const res = await analyticsSkills({ query: { days, limit: 100 } });
      return (res.data?.skills as SkillAnalytics[] | null) ?? [];
    },
  });

  const isLoading = overviewLoading || skillsLoading;

  return (
    <div className="flex flex-col min-h-full">
      {/* Page header */}
      <div className="px-12 pt-12 max-w-screen-xl w-full mx-auto">
        <div className="text-[11px] text-text-tertiary uppercase tracking-[0.1em] mb-3.5">
          Workspace
        </div>

        <div className="flex items-center justify-between gap-4 mb-3.5">
          <div className="flex items-center gap-3.5 min-w-0">
            <h1 className="text-[34px] font-medium tracking-tight text-text-primary m-0">
              Analytics
            </h1>
            <BarChart3 className="size-5 text-text-tertiary mt-1" />
          </div>

          {/* Time period toggle */}
          <div className="flex items-center gap-1 p-1 bg-bg-secondary border border-border rounded-lg">
            {PERIOD_OPTIONS.map(({ label, value }) => (
              <Button
                key={value}
                size="sm"
                variant={days === value ? "secondary" : "outline"}
                onClick={() => setDays(value)}
                className={cn(
                  "h-7 px-3 text-xs border-0 transition-colors",
                  days === value
                    ? "bg-bg-tertiary text-text-primary"
                    : "bg-transparent text-text-secondary hover:text-text-primary hover:bg-bg-tertiary"
                )}
              >
                {label}
              </Button>
            ))}
          </div>
        </div>

        <p className="text-sm text-text-secondary m-0 mb-9 max-w-lg leading-relaxed">
          Skill usage, security posture, and developer adoption across your team.
        </p>

        {/* KPI strip */}
        <div className="mb-9">
          {isLoading || !overview ? (
            <KpiSkeleton />
          ) : (
            <KpiStrip data={overview} days={days} />
          )}
        </div>

        {/* Activations chart */}
        <div className="mb-9">
          <ActivationsChart days={days} />
        </div>
      </div>

      {/* Table section */}
      <div className="px-12 pb-12 flex-1 flex flex-col min-h-0 max-w-screen-xl w-full mx-auto">
        <div className="text-[11px] text-text-tertiary uppercase tracking-widest mb-4">
          Skills breakdown
        </div>

        {isLoading ? (
          <TableSkeleton />
        ) : (
          <AnalyticsTable skills={skills ?? []} />
        )}
      </div>
    </div>
  );
}
