import { useState, useRef, useEffect, useMemo } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { TrendingUp, Layers, AlertTriangle, Search, ArrowUpDown, Copy, Check, Zap } from "lucide-react";
import { Checkbox } from "@/components/ui/checkbox";
import { Button } from "@/components/ui/button";
import { SkillCard } from "@/features/skills/skill-card";
import { analyticsOverview, analyticsSkills, bulkReviewSkills } from "@/api/sdk.gen";
import type { SkillAnalytics, OverviewData } from "@/api/types.gen";
import { cn } from "@/lib/utils";

// ── Onboarding empty state ────────────────────────────────────
const INSTALL_COMMANDS: Record<string, string> = {
  curl: "curl -fsSL skael.dev/install | sh",
  brew: "brew install skael",
  go: "go install github.com/skael-dev/skael/cmd/skael@latest",
};

function Onboarding() {
  const [installer, setInstaller] = useState<"curl" | "brew" | "go">("curl");
  const [copied, setCopied] = useState(false);

  const cmd = INSTALL_COMMANDS[installer];

  function handleCopy() {
    navigator.clipboard?.writeText(cmd);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }

  return (
    <div className="flex-1 overflow-auto relative flex flex-col">
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
            <SetupStep
              step="skael setup &lt;url&gt; &lt;api-key&gt;"
              desc="Point the CLI at this server and authenticate."
            />
            <SetupStep
              step="skael publish ./my-skill"
              desc="Publish your first skill — runs security scan automatically."
            />
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

function SetupStep({ step, desc }: { step: string; desc: string }) {
  return (
    <div className="p-4 bg-bg-secondary border border-border rounded-lg hover:border-border-active transition-colors duration-150">
      <code
        className="block font-mono text-[12px] text-accent mb-2"
        dangerouslySetInnerHTML={{ __html: step }}
      />
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

// ── Helpers ───────────────────────────────────────────────────
function extractTags(skills: SkillAnalytics[]): string[] {
  const tags = new Set<string>();
  for (const s of skills) {
    if (
      s &&
      typeof s === "object" &&
      "tags" in s &&
      Array.isArray((s as unknown as { tags: string[] }).tags)
    ) {
      for (const t of (s as unknown as { tags: string[] }).tags) {
        tags.add(t);
      }
    }
    // Also extract from the skill name patterns as a fallback
    // The frontmatter tags aren't in SkillAnalytics, so we rely on
    // whatever the API provides.
  }
  return Array.from(tags).sort();
}

function matchesQuery(skill: SkillAnalytics, q: string): boolean {
  if (!q) return true;
  const lower = q.toLowerCase();
  return (
    skill.name.toLowerCase().includes(lower) ||
    (skill.description ?? "").toLowerCase().includes(lower)
  );
}

type SortKey = "updated" | "name" | "usage";

function sortSkills(skills: SkillAnalytics[], key: SortKey): SkillAnalytics[] {
  const sorted = [...skills];
  switch (key) {
    case "name":
      return sorted.sort((a, b) => a.name.localeCompare(b.name));
    case "usage":
      return sorted.sort((a, b) => b.activations - a.activations);
    case "updated":
    default:
      return sorted.sort(
        (a, b) =>
          new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
      );
  }
}

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

  const { data: skillsData, isLoading } = useQuery({
    queryKey: ["analytics", "skills"],
    queryFn: async () => {
      const res = await analyticsSkills({ query: { days: 30 } });
      return (res.data as SkillAnalytics[] | null) ?? [];
    },
  });

  const skills = skillsData ?? [];

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

  // Derived data
  const allTags = useMemo(() => extractTags(skills), [skills]);

  const filtered = useMemo(() => {
    let result = skills.filter((s) => matchesQuery(s, query));
    if (tagFilter) {
      result = result.filter((s) => {
        // Tag matching on name or description as a lightweight heuristic
        // since SkillAnalytics doesn't expose tags directly
        return (
          s.name.includes(tagFilter) ||
          (s.description ?? "").toLowerCase().includes(tagFilter)
        );
      });
    }
    return sortSkills(result, sortBy);
  }, [skills, query, tagFilter, sortBy]);

  const anyChecked = selected.size > 0;
  const allChecked = filtered.length > 0 && selected.size === filtered.length;

  // Stats
  const totalActivations = overviewData?.total_activations ?? 0;
  const activeSkills = overviewData?.active_skills ?? 0;
  const needsAttention =
    (overviewData?.security.warning ?? 0) +
    (overviewData?.security.critical ?? 0) +
    skills.filter((s) => !s.reviewed_at).length;

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

  // ── Empty state — onboarding ──────────────────────────────
  if (!isLoading && skills.length === 0) {
    return <Onboarding />;
  }

  return (
    <div className="flex flex-col h-full overflow-auto relative">
      {/* Hero */}
      <div className="relative overflow-hidden">
        <div className="px-12 pt-12 relative max-w-screen-xl">
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
            Author, version, and sync Claude skills across your team.
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

      {/* Filter + list */}
      <div className="px-12 pb-12 flex-1 flex flex-col min-h-0 max-w-screen-xl">
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

          {filtered.length === 0 && skills.length > 0 && (
            <div className="text-center py-16 text-text-secondary">
              <div className="text-sm mb-2">Nothing matches that filter</div>
              <div className="text-xs text-text-tertiary">
                Try clearing the search or selecting a different tag
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
