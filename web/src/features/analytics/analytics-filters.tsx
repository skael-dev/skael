import { useQuery } from "@tanstack/react-query";
import { analyticsConsumers } from "@/api/sdk.gen";

export type AnalyticsFilter = { consumer?: string; role?: string };

export function readAnalyticsFilter(params: URLSearchParams): AnalyticsFilter {
  const filter: AnalyticsFilter = {};
  const consumer = params.get("consumer");
  const role = params.get("role");
  if (consumer) filter.consumer = consumer;
  if (role) filter.role = role;
  return filter;
}

function FilterSelect({
  label,
  allLabel,
  value,
  options,
  onChange,
}: {
  label: string;
  allLabel: string;
  value: string;
  options: string[];
  onChange: (value: string) => void;
}) {
  // A value from a shared URL may have aged out of the stored events.
  const choices = value && !options.includes(value) ? [value, ...options] : options;
  return (
    <div className="flex items-center gap-1.5 px-2.5 h-8 border border-border rounded-md text-text-secondary text-xs min-w-0">
      <select
        aria-label={label}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="bg-transparent border-none outline-none text-text-secondary text-xs font-sans cursor-pointer pr-3.5 min-w-0 max-w-full"
      >
        <option value="">{allLabel}</option>
        {choices.map((c) => (
          <option key={c} value={c}>
            {c}
          </option>
        ))}
      </select>
    </div>
  );
}

export function AnalyticsFilters({
  filter,
  params,
  onChange,
  onClear,
}: {
  filter: AnalyticsFilter;
  params: URLSearchParams;
  onChange: (next: URLSearchParams, options: { replace: boolean }) => void;
  onClear: () => void;
}) {
  const { data } = useQuery({
    queryKey: ["analytics", "consumers"],
    queryFn: async () => {
      const res = await analyticsConsumers();
      if (res.error) throw res.error;
      return res.data;
    },
  });

  const consumers = data?.consumers ?? [];
  const roles = data?.roles ?? [];
  const active = Object.keys(filter).length > 0;
  if (consumers.length === 0 && !active) return null;

  function set(key: "consumer" | "role", value: string) {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    onChange(next, { replace: true });
  }

  return (
    <div className="mb-5">
      <div className="flex flex-wrap items-center gap-2">
        <FilterSelect
          label="Filter by consumer"
          allLabel="All consumers"
          value={filter.consumer ?? ""}
          options={consumers}
          onChange={(v) => set("consumer", v)}
        />
        <FilterSelect
          label="Filter by role"
          allLabel="All roles"
          value={filter.role ?? ""}
          options={roles}
          onChange={(v) => set("role", v)}
        />
        {active && (
          <button onClick={onClear} className="text-accent text-xs hover:underline px-1">
            Clear filter
          </button>
        )}
      </div>
      {active && (
        <p className="text-[11px] text-text-tertiary mt-2 mb-0">
          The filter counts automated activations only. Total skills and Security cover the whole registry.
        </p>
      )}
    </div>
  );
}
