import { useState, useRef, useEffect } from "react";
import { useQuery, useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { TrendingUp, Layers, AlertTriangle, Search, ArrowUpDown, Copy, Check, Zap, Download } from "lucide-react";
import { ImportModal } from "@/features/import/import-modal";
import { UnregisteredTab } from "@/features/skills/unregistered-tab";
import { Checkbox } from "@/components/ui/checkbox";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { SkillCard } from "@/features/skills/skill-card";
import { analyticsOverview, analyticsSkills, skillsTags, bulkReviewSkills } from "@/api/sdk.gen";
import type { SkillAnalytics, OverviewData } from "@/api/types.gen";
import { cn } from "@/lib/utils";

// ── Debounce hook ─────────────────────────────────────────────
function useDebouncedValue<T>(value: T, ms: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return debounced;
}

// ── Onboarding empty state ────────────────────────────────────
const INSTALL_COMMANDS: Record<string, string> = {
  curl: "curl -fsSL skael.dev/install | sh",
  brew: "brew install skael",
  go: "go install github.com/skael-dev/skael/cmd/skael@latest",
};

function Onboarding() {
  const [installer, setInstaller] = useState<"curl" | "brew" | "go">("curl");
  const [copied, setCopied] = useState(false);

  const cmd = INSTALL_COMMANDS[installer]!;

  function handleCopy() {
    navigator.clipboard?.writeText(cmd);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }

  return (
    <div className="flex-1 relative flex flex-col">
      {/* Ambient gradient blob */}
      <div
        className="pointer-events-none absolute -top-[120px] -right-[100px] w-[520px] h-[520px] rounded-full"
        style={{
          background: "radial-gradient(circle, var(--color-accent) 0%, transparent 65%)",
          opacity: 0.10,
          filter: "blur(40px)",
        }}
      />
      <div
        className="pointer-events-none absolute top-[420px] -left-[80px] w-[420px] h-[420px] rounded-full"
        style={{
          background: "radial-gradient(circle, var(--color-info) 0%, transparent 65%)",
          opacity: 0.04,
          filter: "blur(40px)",
        }}
      />

      <div className="relative px-12 pt-16 pb-12 max-w-[880px] w-full mx-auto">
        {/* Heading */}
        <div className="animate-fade-up" style={{ animationDelay: "0ms" }}>
          <h1 className="text-4xl font-medium tracking-tight text-text-primary m-0 mb-3.5 leading-none">
            Welcome to Skael
          </h1>
        </div>

        <div className="animate-fade-up" style={{ animationDelay: "50ms" }}>
          <p className="text-[15px] text-text-secondary m-0 mb-9 max-w-[540px] leading-relaxed">
            Manage and track AI agent skills across your team. Install the CLI to get started.
          </p>
        </div>

        {/* Step 1: Install */}
        <div className="animate-fade-up" style={{ animationDelay: "120ms" }}>
          <div className="flex items-center gap-2 mb-3">
            <div className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary">
              1 · Install the CLI
            </div>
            <div className="flex-1" />
            {/* Tab switcher */}
            <div className="flex border border-border rounded-[5px] overflow-hidden">
              {(["curl", "brew", "go"] as const).map((k, i) => (
                <button
                  key={k}
                  onClick={() => setInstaller(k)}
                  className={cn(
                    "px-2.5 py-1 text-[11px] font-mono cursor-pointer transition-colors duration-100",
                    i > 0 && "border-l border-border",
                    installer === k
                      ? "bg-bg-tertiary text-text-primary"
                      : "bg-transparent text-text-tertiary hover:text-text-secondary"
                  )}
                  style={{ border: "none" }}
                >
                  {k}
                </button>
              ))}
            </div>
          </div>

          {/* Terminal card */}
          <div className="flex items-center gap-3 px-4 py-3.5 bg-bg-secondary border border-border-active rounded-lg relative overflow-hidden mb-11">
            <span className="font-mono text-[13px] text-accent select-none shrink-0">$</span>
            <code
              key={installer}
              className="font-mono text-[13px] text-text-primary flex-1 whitespace-nowrap overflow-auto animate-fade-in"
            >
              {cmd}
            </code>
            <button
              onClick={handleCopy}
              className={cn(
                "flex items-center gap-1.5 px-2.5 py-1 h-[26px] text-[11px] border rounded-[5px] cursor-pointer font-sans shrink-0 transition-all duration-150",
                copied
                  ? "bg-accent-surface text-accent border-accent"
                  : "bg-bg-tertiary text-text-secondary border-border hover:border-border-active"
              )}
            >
              {copied ? <Check className="size-[11px]" /> : <Copy className="size-[11px]" />}
              {copied ? "Copied" : "Copy"}
            </button>
          </div>
        </div>

        {/* Step 2: Setup */}
        <div className="animate-fade-up" style={{ animationDelay: "200ms" }}>
          <div className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary mb-3">
            2 · Connect to your registry
          </div>
          <div className="grid grid-cols-2 gap-2.5 mb-9">
            <SetupStep desc="Point the CLI at this server and authenticate.">
              skael setup <code className="px-1.5 py-0.5 bg-bg-tertiary border border-border rounded text-sm font-mono">&lt;url&gt;</code>{" "}<code className="px-1.5 py-0.5 bg-bg-tertiary border border-border rounded text-sm font-mono">&lt;api-key&gt;</code>
            </SetupStep>
            <SetupStep desc="Publish your first skill — runs security scan automatically.">
              skael publish ./my-skill
            </SetupStep>
          </div>
        </div>

        {/* Footer: what is a skill */}
        <div className="animate-fade-up" style={{ animationDelay: "280ms" }}>
          <div className="flex items-center gap-4 p-4 bg-bg-secondary border border-border rounded-lg">
            <div className="size-8 rounded-[7px] bg-bg-tertiary border border-border flex items-center justify-center shrink-0">
              <Zap className="size-[14px] text-text-secondary" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-[13px] text-text-primary mb-0.5">
                Not sure what a skill is?
              </div>
              <div className="text-[12px] text-text-tertiary leading-relaxed">
                A skill is a versioned SKILL.md file that gives Claude structured context for a specific task — code review, deploy checks, API patterns, and more.
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function SetupStep({ children, desc }: { children: React.ReactNode; desc: string }) {
  return (
    <div className="p-4 bg-bg-secondary border border-border rounded-lg hover:border-border-active transition-colors duration-150">
      <div className="block font-mono text-[12px] text-accent mb-2">
        {children}
      </div>
      <p className="text-[12px] text-text-tertiary m-0 leading-relaxed">{desc}</p>
    </div>
  );
}

// ── Tag colors ────────────────────────────────────────────────
const TAG_COLORS: Record<string, string> = {
  review: "bg-purple-400",
  deploy: "bg-emerald-400",
  security: "bg-red-400",
  testing: "bg-blue-400",
  api: "bg-amber-400",
  db: "bg-cyan-400",
  ops: "bg-orange-400",
  frontend: "bg-pink-400",
  deprecated: "bg-slate-400",
};

// Sort options exposed in the UI; mapped to the server's `sort` param.
type SortKey = "updated" | "name" | "usage";

// ── Stat tile ─────────────────────────────────────────────────
function StatTile({
  icon: Icon,
  label,
  value,
  sub,
  subColor,
}: {
  icon: React.ElementType;
  label: string;
  value: number | string;
  sub?: string;
  subColor?: string;
}) {
  return (
    <div className="flex items-center gap-3.5 p-4 bg-bg-secondary border border-border rounded-lg transition-all duration-150 hover:border-border-active hover:-translate-y-px">
      <div className="size-9 rounded-[7px] bg-bg-tertiary border border-border flex items-center justify-center shrink-0">
        <Icon className="size-[15px] text-text-secondary" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary mb-1.5">
          {label}
        </div>
        <div className="flex items-baseline gap-2">
          <span
            className="text-[22px] font-medium text-text-primary leading-none tracking-tight"
            style={{ fontVariantNumeric: "tabular-nums" }}
          >
            {typeof value === "number" ? value.toLocaleString() : value}
          </span>
          {sub && (
            <span
              className={cn(
                "text-[11px]",
                subColor ?? "text-text-tertiary"
              )}
              style={{ fontVariantNumeric: "tabular-nums" }}
            >
              {sub}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}

// ── Filter pill ───────────────────────────────────────────────
function FilterPill({
  active,
  onClick,
  label,
  color,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  color?: string;
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "px-2.5 py-1 text-[11px] border border-border rounded cursor-pointer whitespace-nowrap",
        "inline-flex items-center gap-1.5 transition-colors duration-150 font-sans",
        active
          ? "bg-bg-tertiary text-text-primary"
          : "bg-transparent text-text-secondary hover:bg-bg-secondary"
      )}
    >
      {color && (
        <span className={cn("size-[5px] rounded-full", color)} />
      )}
      {label}
    </button>
  );
}

// ── Main page ─────────────────────────────────────────────────
export function SkillList() {
  const queryClient = useQueryClient();
  const [query, setQuery] = useState("");
  const [tagFilter, setTagFilter] = useState<string | null>(null);
  const [sortBy, setSortBy] = useState<SortKey>("updated");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [importOpen, setImportOpen] = useState(false);
  const [activeTab, setActiveTab] = useState<"registry" | "unregistered">("registry");
  const inputRef = useRef<HTMLInputElement>(null);

  // Keyboard shortcut: "/" focuses search
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (
        e.key === "/" &&
        document.activeElement !== inputRef.current &&
        !(e.metaKey || e.ctrlKey)
      ) {
        e.preventDefault();
        inputRef.current?.focus();
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);

  // Data fetching
  const { data: overviewData } = useQuery({
    queryKey: ["analytics", "overview"],
    queryFn: async () => {
      const res = await analyticsOverview({ query: { days: 30 } });
      return res.data as OverviewData | undefined;
    },
  });

  const PAGE = 50;
  const debouncedQuery = useDebouncedValue(query, 250);
  const serverSort = sortBy === "usage" ? "activations" : sortBy;
  const skillsQuery = useInfiniteQuery({
    queryKey: ["analytics", "skills", { sort: serverSort, q: debouncedQuery, tag: tagFilter ?? "" }],
    initialPageParam: 0,
    queryFn: async ({ pageParam }) => {
      const res = await analyticsSkills({
        query: { days: 30, limit: PAGE, offset: pageParam as number, sort: serverSort, q: debouncedQuery, tag: tagFilter ?? "" },
      });
      return (res.data as { skills: SkillAnalytics[] | null; total: number }) ?? { skills: [], total: 0 };
    },
    getNextPageParam: (_last, pages) => {
      const loaded = pages.reduce((n, p) => n + (p.skills?.length ?? 0), 0);
      const total = pages[0]?.total ?? 0;
      return loaded < total ? loaded : undefined;
    },
  });
  const isLoading = skillsQuery.isLoading;

  const tagsQuery = useQuery({
    queryKey: ["skills", "tags"],
    queryFn: async () => (await skillsTags()).data?.tags ?? [],
  });

  const { data: unregisteredData } = useQuery({
    queryKey: ["analytics", "unregistered", 30],
    queryFn: async () => {
      const res = await fetch("/api/analytics/unregistered?days=30", { credentials: "include" });
      if (!res.ok) return [];
      return res.json() as Promise<{ name: string }[]>;
    },
  });
  const unregisteredCount = unregisteredData?.length ?? 0;

  const skills = skillsQuery.data?.pages.flatMap((p) => p.skills ?? []) ?? [];

  // Infinite scroll: load the next page when the sentinel scrolls into view.
  const sentinelRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el) return;
    const obs = new IntersectionObserver(
      (entries) => {
        if (entries[0].isIntersecting && skillsQuery.hasNextPage && !skillsQuery.isFetchingNextPage) {
          skillsQuery.fetchNextPage();
        }
      },
      { rootMargin: "300px" }
    );
    obs.observe(el);
    return () => obs.disconnect();
  }, [skillsQuery.hasNextPage, skillsQuery.isFetchingNextPage, skillsQuery]);

  // Bulk review mutation
  const bulkReview = useMutation({
    mutationFn: async (names: string[]) => {
      await bulkReviewSkills({ body: { names } });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["analytics"] });
      setSelected(new Set());
    },
  });

  // Derived data — filtering/sorting happen server-side; the list IS the result.
  const allTags = tagsQuery.data ?? [];
  const filtered = skills;

  const anyChecked = selected.size > 0;
  const allChecked = filtered.length > 0 && selected.size === filtered.length;

  // Stats
  const totalActivations = overviewData?.total_activations ?? 0;
  const activeSkills = overviewData?.active_skills ?? 0;
  const needsAttention =
    (overviewData?.security.warning ?? 0) +
    (overviewData?.security.critical ?? 0) +
    (overviewData?.unreviewed_skills ?? 0);

  // Selection handlers
  function toggleOne(name: string, checked: boolean) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (checked) next.add(name);
      else next.delete(name);
      return next;
    });
  }

  function toggleAll() {
    if (allChecked) {
      setSelected(new Set());
    } else {
      setSelected(new Set(filtered.map((s) => s.name)));
    }
  }

  // ── Loading skeleton ──────────────────────────────────────
  if (isLoading) {
    return (
      <div className="flex flex-col min-h-full px-12 pt-12">
        <Skeleton className="h-3 w-20 mb-3.5 bg-bg-secondary" />
        <Skeleton className="h-10 w-40 mb-3.5 bg-bg-secondary" />
        <Skeleton className="h-4 w-80 mb-9 bg-bg-secondary" />
        <div className="grid grid-cols-3 gap-2.5 mb-9 max-w-[880px]">
          {[0, 1, 2].map((i) => (
            <div key={i} className="flex items-center gap-3.5 p-4 bg-bg-secondary border border-border rounded-lg">
              <Skeleton className="size-9 rounded-[7px] bg-bg-tertiary" />
              <div className="flex-1 space-y-2">
                <Skeleton className="h-2 w-20 bg-bg-tertiary" />
                <Skeleton className="h-6 w-12 bg-bg-tertiary" />
              </div>
            </div>
          ))}
        </div>
        <div className="space-y-px">
          <div className="grid gap-4 px-3.5 py-2 border-b border-border" style={{ gridTemplateColumns: "28px 12px 1fr 80px 80px 110px" }}>
            {[0, 1, 2, 3, 4, 5].map((i) => (
              <Skeleton key={i} className="h-2 bg-bg-tertiary" />
            ))}
          </div>
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="grid gap-4 px-3.5 py-3 border-b border-border" style={{ gridTemplateColumns: "28px 12px 1fr 80px 80px 110px" }}>
              <Skeleton className="h-4 bg-bg-secondary" />
              <Skeleton className="h-4 w-2 bg-bg-secondary" />
              <Skeleton className="h-4 w-48 bg-bg-secondary" />
              <Skeleton className="h-4 bg-bg-secondary" />
              <Skeleton className="h-4 bg-bg-secondary" />
              <Skeleton className="h-4 bg-bg-secondary" />
            </div>
          ))}
        </div>
      </div>
    );
  }

  // ── Empty state — onboarding ──────────────────────────────
  if (!isLoading && skills.length === 0 && unregisteredCount === 0) {
    return <Onboarding />;
  }

  return (
    <div className="flex flex-col min-h-full relative">
      {/* Hero */}
      <div className="relative overflow-hidden">
        <div className="px-12 pt-12 relative max-w-screen-xl w-full mx-auto">
          <div className="text-[11px] text-text-tertiary uppercase tracking-[0.1em] mb-3.5">
            Workspace
          </div>

          <div className="flex items-center justify-between gap-4 mb-3.5">
            <div className="flex items-center gap-3.5 min-w-0">
              <h1 className="text-[34px] font-medium tracking-tight text-text-primary m-0">
                Skills
              </h1>
              <span className="size-2.5 rounded-full bg-accent shadow-[0_0_14px_var(--color-accent)]" />
            </div>
          </div>

          <p className="text-sm text-text-secondary m-0 mb-9 max-w-lg leading-relaxed">
            Author, version, and sync your team's AI skills across every agent.
          </p>

          {/* Stat tiles */}
          <div className="grid grid-cols-3 gap-2.5 mb-9 max-w-[880px]">
            <StatTile
              icon={TrendingUp}
              label="Invocations - 30d"
              value={totalActivations}
            />
            <StatTile
              icon={Layers}
              label="Active skills"
              value={activeSkills}
            />
            <StatTile
              icon={AlertTriangle}
              label="Needs attention"
              value={needsAttention}
              sub={needsAttention > 0 ? "unreviewed / warnings" : "all clear"}
              subColor={needsAttention > 0 ? "text-warning" : undefined}
            />
          </div>
        </div>
      </div>

      {/* Tab bar */}
      <div className="px-12 flex border-b border-border max-w-screen-xl w-full mx-auto">
        <button
          onClick={() => setActiveTab("registry")}
          className={`px-4 py-2.5 text-[13px] font-sans border-b-2 transition-colors cursor-pointer bg-transparent ${activeTab === "registry"
              ? "text-text-primary border-accent font-medium"
              : "text-text-secondary border-transparent hover:text-text-primary"
            }`}
        >
          Registry
        </button>
        <button
          onClick={() => setActiveTab("unregistered")}
          className={`px-4 py-2.5 text-[13px] font-sans border-b-2 transition-colors cursor-pointer bg-transparent flex items-center gap-2 ${activeTab === "unregistered"
              ? "text-text-primary border-accent font-medium"
              : "text-text-secondary border-transparent hover:text-text-primary"
            }`}
        >
          Unregistered
          {unregisteredCount > 0 && (
            <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-warning/20 text-warning font-medium">
              {unregisteredCount}
            </span>
          )}
        </button>
      </div>

      {/* Filter + list */}
      <div className="px-12 pb-12 flex-1 flex flex-col min-h-0 max-w-screen-xl w-full mx-auto">
        {activeTab === "unregistered" ? (
          <div className="mt-4">
            <UnregisteredTab days={30} />
          </div>
        ) : (
          <>
            {/* Filter bar */}
            <div className="flex items-center gap-2.5 mb-4">
              {/* Search input */}
              <div className="flex items-center gap-2 px-3 h-8 flex-[0_1_300px] bg-bg-secondary border border-border rounded-md transition-colors duration-150 focus-within:border-border-active">
                <Search className="size-[13px] text-text-tertiary shrink-0" />
                <input
                  ref={inputRef}
                  type="text"
                  placeholder="Filter skills..."
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  className="flex-1 bg-transparent border-none outline-none text-[13px] text-text-primary font-sans min-w-0 placeholder:text-text-tertiary"
                />
                {!query && (
                  <kbd className="font-mono text-[10px] text-text-tertiary px-[5px] py-px border border-border rounded-[3px]">
                    /
                  </kbd>
                )}
              </div>

              {/* Tag filter pills */}
              <div className="flex gap-1 items-center overflow-x-auto flex-1 min-w-0">
                <FilterPill
                  active={!tagFilter}
                  onClick={() => setTagFilter(null)}
                  label="all"
                />
                {allTags.map((t) => (
                  <FilterPill
                    key={t}
                    active={tagFilter === t}
                    onClick={() =>
                      setTagFilter(t === tagFilter ? null : t)
                    }
                    label={t}
                    color={TAG_COLORS[t]}
                  />
                ))}
              </div>

              {/* Sort dropdown */}
              <div className="flex items-center gap-1.5 px-2.5 h-8 border border-border rounded-md text-text-secondary text-xs shrink-0">
                <ArrowUpDown className="size-3" />
                <select
                  value={sortBy}
                  onChange={(e) => setSortBy(e.target.value as SortKey)}
                  className="bg-transparent border-none outline-none text-text-secondary text-xs font-sans cursor-pointer pr-3.5"
                >
                  <option value="updated">Updated</option>
                  <option value="name">Name</option>
                  <option value="usage">Usage</option>
                </select>
              </div>

              {/* Import button */}
              <Button
                onClick={() => setImportOpen(true)}
                variant="outline"
                className="h-8 text-xs"
              >
                <Download size={13} className="mr-1.5" />
                Import
              </Button>
            </div>

            {/* Bulk actions */}
            {anyChecked && (
              <div className="flex items-center gap-3 mb-3 px-3.5 py-2 bg-bg-secondary border border-border rounded-lg">
                <Checkbox
                  checked={allChecked}
                  onCheckedChange={toggleAll}
                />
                <span className="text-xs text-text-secondary">
                  {selected.size} selected
                </span>
                <Button
                  size="sm"
                  variant="outline"
                  className="ml-auto h-7 text-xs"
                  disabled={bulkReview.isPending}
                  onClick={() => bulkReview.mutate(Array.from(selected))}
                >
                  {bulkReview.isPending ? "Reviewing..." : "Mark Reviewed"}
                </Button>
              </div>
            )}

            {/* Column headers */}
            <div
              className="grid gap-4 px-3.5 py-2 text-[10px] text-text-tertiary uppercase tracking-[0.08em] border-b border-border"
              style={{
                gridTemplateColumns: "28px 12px 1fr 80px 80px 110px",
              }}
            >
              <span />
              <span />
              <span>Skill</span>
              <span className="text-right">Invocations</span>
              <span className="text-right">Security</span>
              <span className="text-right">Updated</span>
            </div>

            {/* Skill rows */}
            <div>
              {filtered.map((skill) => (
                <SkillCard
                  key={skill.name}
                  skill={skill}
                  checked={selected.has(skill.name)}
                  onCheck={(checked) => toggleOne(skill.name, checked)}
                  anyChecked={anyChecked}
                />
              ))}

              {skills.length === 0 && (debouncedQuery || tagFilter) && (
                <div className="text-center py-16 text-text-secondary">
                  <div className="text-sm mb-2">Nothing matches that filter</div>
                  <div className="text-xs text-text-tertiary">
                    Try clearing the search or selecting a different tag
                  </div>
                </div>
              )}

              {/* Infinite-scroll sentinel + loading indicator */}
              <div ref={sentinelRef} />
              {skillsQuery.isFetchingNextPage && (
                <div className="text-center py-4 text-xs text-text-tertiary">Loading more…</div>
              )}
            </div>
          </>
        )}
      </div>

      <ImportModal open={importOpen} onOpenChange={setImportOpen} />
    </div>
  );
}
