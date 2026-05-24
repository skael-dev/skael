import { Layers, Activity, TrendingUp, Shield } from "lucide-react";
import { cn } from "@/lib/utils";
import type { OverviewData } from "@/api/types.gen";

type KpiTileProps = {
  icon: React.ElementType;
  label: string;
  value: number | string;
  sub?: string;
};

function KpiTile({ icon: Icon, label, value, sub }: KpiTileProps) {
  return (
    <div
      className={cn(
        "flex items-center gap-3.5 p-4 bg-bg-secondary border border-border rounded-lg",
        "transition-all duration-150 hover:border-border-active hover:-translate-y-px"
      )}
    >
      <div className="size-9 rounded-[7px] bg-bg-tertiary border border-border flex items-center justify-center shrink-0">
        <Icon className="size-[15px] text-text-secondary" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-[10px] uppercase tracking-widest text-text-tertiary mb-1.5">
          {label}
        </div>
        <div className="flex items-baseline gap-2">
          <span
            className="text-2xl font-medium text-text-primary leading-none tracking-tight tabular-nums"
          >
            {typeof value === "number" ? value.toLocaleString() : value}
          </span>
          {sub && (
            <span className="text-[11px] text-text-tertiary tabular-nums">
              {sub}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}

type KpiStripProps = {
  data: OverviewData;
  days: number;
};

export function KpiStrip({ data, days }: KpiStripProps) {
  const { total_skills, active_skills, total_activations, security } = data;
  const securitySub = [
    security.clean > 0 ? `${security.clean} clean` : null,
    security.warning > 0 ? `${security.warning} warn` : null,
    security.critical > 0 ? `${security.critical} critical` : null,
  ]
    .filter(Boolean)
    .join(", ");

  return (
    <div className="grid grid-cols-4 gap-2.5">
      <KpiTile
        icon={Layers}
        label="Total skills"
        value={total_skills}
      />
      <KpiTile
        icon={Activity}
        label={`Active (${days}d)`}
        value={active_skills}
      />
      <KpiTile
        icon={TrendingUp}
        label="Total activations"
        value={total_activations}
      />
      <KpiTile
        icon={Shield}
        label="Security"
        value={security.critical > 0 ? "Issues" : "Clean"}
        sub={securitySub || undefined}
      />
    </div>
  );
}
