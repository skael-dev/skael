import { useState, useEffect, useMemo, useRef, useLayoutEffect } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, ShieldCheck, ShieldAlert, Clock, Download } from "lucide-react";
import { getSkill, getSkillActivations, listSkillVersions, reviewSkill, unreviewSkill } from "@/api/sdk.gen";
import type { Skill, ActivationSummary, Version, ListVersionsBody } from "@/api/types.gen";
import { MarkdownRenderer } from "@/features/skills/markdown-renderer";
import { FileTree } from "@/features/skills/file-tree";
import { FileViewer, FileViewerFallback } from "@/features/skills/file-viewer";
import { VersionList } from "@/features/skills/version-list";
import { SecurityBadge } from "@/features/security/security-badge";
import { ReviewStatus } from "@/features/security/review-status";
import { ScanFindings } from "@/features/security/scan-findings";
import type { ScanReport } from "@/features/security/scan-findings";
import { cn } from "@/lib/utils";

// ── Tag colors (mirrors skill-card) ──────────────────────────────
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

// ── Helpers ───────────────────────────────────────────────────────
function formatRelativeTime(dateString: string | null): string {
  if (!dateString) return "—";
  const now = Date.now();
  const then = new Date(dateString).getTime();
  const diffMs = now - then;
  const diffDay = Math.floor(diffMs / 86_400_000);
  if (diffDay < 1) {
    const diffHr = Math.floor(diffMs / 3_600_000);
    if (diffHr < 1) return "just now";
    return `${diffHr}h ago`;
  }
  if (diffDay < 7) return `${diffDay}d ago`;
  if (diffDay < 30) return `${Math.floor(diffDay / 7)}w ago`;
  if (diffDay < 365) return `${Math.floor(diffDay / 30)}mo ago`;
  return `${Math.floor(diffDay / 365)}y ago`;
}

function extractTags(skill: Skill): string[] {
  if (!skill.frontmatter) return [];
  const fm = skill.frontmatter as Record<string, unknown>;
  if (Array.isArray(fm.tags)) {
    return fm.tags.filter((t): t is string => typeof t === "string");
  }
  return [];
}

// ── MetaCell ──────────────────────────────────────────────────────
function MetaCell({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary">
        {label}
      </span>
      <span className="text-[13px] text-text-primary" style={{ fontVariantNumeric: "tabular-nums" }}>
        {value}
      </span>
    </div>
  );
}

// ── SlidingTabs ───────────────────────────────────────────────────
type TabDef = {
  id: string;
  label: string;
  disabled?: boolean;
};

function SlidingTabs({
  tabs,
  activeTab,
  onChange,
}: {
  tabs: TabDef[];
  activeTab: string;
  onChange: (id: string) => void;
}) {
  const refs = useRef<Record<string, HTMLButtonElement | null>>({});
  const [indicator, setIndicator] = useState({ left: 0, width: 0, ready: false });

  useLayoutEffect(() => {
    const el = refs.current[activeTab];
    if (el) {
      setIndicator({ left: el.offsetLeft, width: el.offsetWidth, ready: true });
    }
  }, [activeTab]);

  return (
    <div className="relative flex">
      {tabs.map((tab) => (
        <button
          key={tab.id}
          ref={(el) => { refs.current[tab.id] = el; }}
          disabled={tab.disabled}
          onClick={() => !tab.disabled && onChange(tab.id)}
          className={cn(
            "flex items-center gap-1.5 px-3.5 py-3 text-[13px] font-normal font-sans border-none bg-transparent cursor-pointer transition-colors duration-150 outline-none",
            tab.disabled
              ? "text-text-tertiary cursor-not-allowed opacity-45"
              : activeTab === tab.id
              ? "text-text-primary font-medium"
              : "text-text-secondary hover:text-text-primary"
          )}
        >
          {tab.label}
          {tab.disabled && (
            <span className="text-[9px] font-mono px-1 py-px rounded bg-bg-tertiary text-text-tertiary">
              P2
            </span>
          )}
        </button>
      ))}
      {/* Sliding accent underline */}
      <div
        className="absolute bottom-[-1px] h-0.5 bg-accent rounded-sm transition-none"
        style={{
          left: indicator.left,
          width: indicator.width,
          transition: indicator.ready
            ? "left 0.28s cubic-bezier(0.22,1,0.36,1), width 0.28s cubic-bezier(0.22,1,0.36,1)"
            : "none",
          opacity: indicator.width ? 1 : 0,
        }}
      />
    </div>
  );
}

// ── TOC extraction ────────────────────────────────────────────────
type TocItem = { id: string; label: string; level: number };

function extractToc(markdown: string): TocItem[] {
  const lines = markdown.split("\n");
  const items: TocItem[] = [];
  for (const line of lines) {
    const m = line.match(/^(#{1,3})\s+(.+)/);
    if (m && m[1] && m[2]) {
      const level = m[1].length;
      const label = m[2].trim();
      // Generate id matching GitHub style: lowercase, replace spaces with hyphens
      const id = label
        .toLowerCase()
        .replace(/[^\w\s-]/g, "")
        .replace(/\s+/g, "-");
      items.push({ id, label, level });
    }
  }
  return items;
}

// ── TableOfContents ───────────────────────────────────────────────
function TableOfContents({
  items,
  activeId,
  onSelect,
}: {
  items: TocItem[];
  activeId: string;
  onSelect: (id: string) => void;
}) {
  if (items.length === 0) return null;

  return (
    <div className="w-[180px] shrink-0 sticky top-6 self-start">
      <div className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary mb-3">
        On this page
      </div>
      <nav className="flex flex-col">
        {items.map((item) => (
          <a
            key={item.id}
            href={`#${item.id}`}
            onClick={(e) => {
              e.preventDefault();
              onSelect(item.id);
              document.getElementById(item.id)?.scrollIntoView({ behavior: "smooth" });
            }}
            className={cn(
              "block py-1.5 pl-3 text-[12px] no-underline transition-colors duration-150",
              item.level > 1 && "pl-5",
              activeId === item.id
                ? "text-text-primary border-l-2 border-accent"
                : "text-text-tertiary border-l-2 border-border hover:text-text-secondary"
            )}
          >
            {item.label}
          </a>
        ))}
      </nav>
    </div>
  );
}

// ── Content Tab ───────────────────────────────────────────────────
function TabContent({ skill }: { skill: Skill }) {
  const content = skill.content ?? "";
  const tocItems = extractToc(content);
  const [activeSection, setActiveSection] = useState(tocItems[0]?.id ?? "");

  // Intersection observer to update active TOC item
  useEffect(() => {
    if (tocItems.length === 0) return;

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setActiveSection(entry.target.id);
          }
        }
      },
      { rootMargin: "-20% 0px -70% 0px" }
    );

    for (const item of tocItems) {
      const el = document.getElementById(item.id);
      if (el) observer.observe(el);
    }

    return () => observer.disconnect();
  }, [content]); // eslint-disable-line react-hooks/exhaustive-deps

  if (!content) {
    return (
      <div className="text-text-tertiary text-sm py-12 text-center">
        No content available for this skill.
      </div>
    );
  }

  return (
    <div className="flex gap-12 max-w-[1000px]">
      <article className="flex-1 min-w-0 max-w-[680px]">
        <MarkdownRenderer content={content} />
      </article>
      {tocItems.length > 0 && (
        <TableOfContents
          items={tocItems}
          activeId={activeSection}
          onSelect={setActiveSection}
        />
      )}
    </div>
  );
}

// ── Files Tab ────────────────────────────────────────────────────
function TabFiles({ skill, versions }: { skill: Skill; versions: Version[] }) {
  const [activeFile, setActiveFile] = useState("SKILL.md");

  // Get file manifest from the latest version
  const latestVersion = versions.length > 0
    ? [...versions].sort((a, b) => b.version - a.version)[0]
    : null;

  const fileManifest = latestVersion?.file_manifest ?? [];

  // For SKILL.md we can show the content directly from the skill object
  const fileContent = useMemo(() => {
    if (activeFile === "SKILL.md" && skill.content) {
      return skill.content;
    }
    return null;
  }, [activeFile, skill.content]);

  if (fileManifest.length === 0) {
    return (
      <div className="text-text-tertiary text-sm py-12 text-center">
        No files available. Publish a version to see the file manifest.
      </div>
    );
  }

  return (
    <div className="flex gap-6 max-w-[1200px] min-h-[480px]">
      <FileTree
        files={fileManifest}
        activeFile={activeFile}
        onSelect={setActiveFile}
      />
      {fileContent ? (
        <FileViewer
          content={fileContent}
          filename={activeFile}
          skillName={skill.name}
        />
      ) : (
        <FileViewerFallback filename={activeFile} />
      )}
    </div>
  );
}

// ── Usage Tab ────────────────────────────────────────────────────
function TabUsage({ skill, activations }: { skill: Skill; activations: ActivationSummary | undefined }) {
  const [period, setPeriod] = useState<number>(30);

  const { data: periodActivations } = useQuery({
    queryKey: ["skill-activations", skill.name, period],
    queryFn: () =>
      getSkillActivations({ path: { name: skill.name }, query: { days: period } }).then(
        (r) => r.data as ActivationSummary
      ),
    enabled: period !== 30, // For 30d we already have the data
  });

  const data = period === 30 ? activations : periodActivations ?? activations;

  const totalCount = data?.total_count ?? 0;
  const uniqueDevs = data?.unique_devs ?? 0;
  const avgPerDay = period > 0 ? Math.round(totalCount / period) : 0;
  const lastTriggered = data?.last_triggered;
  const byAgent = data?.by_agent ?? {};

  // Format last triggered time
  const lastTriggeredText = lastTriggered
    ? formatRelativeTime(lastTriggered)
    : "—";

  // Calculate total for agent percentages
  const agentTotal = Object.values(byAgent).reduce((sum, v) => sum + v, 0);

  return (
    <div className="max-w-[760px]">
      {/* KPI row */}
      <div className="grid grid-cols-4 gap-2.5 mb-8">
        {[
          { label: `Invocations - ${period}d`, value: totalCount.toLocaleString() },
          { label: "Unique devs", value: uniqueDevs },
          { label: "Avg per day", value: avgPerDay },
          { label: "Last triggered", value: lastTriggeredText },
        ].map((kpi) => (
          <div
            key={kpi.label}
            className="p-3.5 bg-bg-secondary border border-border rounded-lg"
          >
            <div className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary mb-2">
              {kpi.label}
            </div>
            <span
              className="text-[22px] font-medium text-text-primary leading-none tracking-tight"
              style={{ fontVariantNumeric: "tabular-nums" }}
            >
              {kpi.value}
            </span>
          </div>
        ))}
      </div>

      {/* Period toggle */}
      <div className="flex items-center gap-3 mb-6">
        <span className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary">
          Time period
        </span>
        <div className="flex gap-1">
          {[
            { label: "7d", days: 7 },
            { label: "30d", days: 30 },
            { label: "90d", days: 90 },
          ].map((opt) => (
            <button
              key={opt.days}
              onClick={() => setPeriod(opt.days)}
              className={cn(
                "px-2 py-1 text-[11px] rounded border border-border cursor-pointer font-sans transition-colors duration-100",
                period === opt.days
                  ? "bg-bg-tertiary text-text-primary"
                  : "bg-transparent text-text-tertiary hover:text-text-secondary"
              )}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </div>

      {/* Agent breakdown */}
      {Object.keys(byAgent).length > 0 && (
        <>
          <div className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary mb-3">
            Agent breakdown
          </div>
          <div className="border border-border rounded-lg overflow-hidden">
            {Object.entries(byAgent)
              .sort(([, a], [, b]) => b - a)
              .map(([agent, count], i, arr) => {
                const pct = agentTotal > 0 ? (count / agentTotal) * 100 : 0;
                return (
                  <div
                    key={agent}
                    className={cn(
                      "flex items-center gap-3 px-4 py-3 relative",
                      i < arr.length - 1 && "border-b border-border"
                    )}
                  >
                    {/* Background bar */}
                    <div
                      className="absolute left-0 top-0 bottom-0 bg-accent/5 pointer-events-none"
                      style={{ width: `${pct}%` }}
                    />
                    {/* Avatar circle */}
                    <div className="size-6 rounded-full bg-bg-tertiary border border-border flex items-center justify-center text-[11px] font-mono text-text-secondary z-[1]">
                      {agent[0]?.toUpperCase()}
                    </div>
                    <span className="text-[13px] text-text-primary z-[1]">
                      {agent}
                    </span>
                    <span className="flex-1" />
                    <span
                      className="text-xs text-text-primary z-[1]"
                      style={{ fontVariantNumeric: "tabular-nums" }}
                    >
                      {count.toLocaleString()}
                    </span>
                    <span
                      className="text-[11px] text-text-tertiary z-[1] w-11 text-right"
                      style={{ fontVariantNumeric: "tabular-nums" }}
                    >
                      {pct.toFixed(0)}%
                    </span>
                  </div>
                );
              })}
          </div>
        </>
      )}

      {Object.keys(byAgent).length === 0 && totalCount === 0 && (
        <div className="text-text-tertiary text-sm py-12 text-center">
          No usage data yet. Activate this skill to start tracking.
        </div>
      )}
    </div>
  );
}

// ── Security Tab ─────────────────────────────────────────────────
function TabSecurity({
  skill,
  scanReport,
  scanLoading,
}: {
  skill: Skill;
  scanReport: ScanReport | null;
  scanLoading: boolean;
}) {
  const queryClient = useQueryClient();

  const reviewMutation = useMutation({
    mutationFn: () => reviewSkill({ path: { name: skill.name } }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["skill", skill.name] });
    },
  });

  const unreviewMutation = useMutation({
    mutationFn: () => unreviewSkill({ path: { name: skill.name } }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["skill", skill.name] });
    },
  });

  const isReviewed = !!skill.reviewed_at;
  const isPending = reviewMutation.isPending || unreviewMutation.isPending;

  return (
    <div className="max-w-[800px]">
      {/* Security overview card */}
      <div className="flex items-center justify-between p-4 bg-bg-secondary border border-border rounded-lg mb-6">
        <div className="flex items-center gap-4">
          {/* Security status */}
          <div className="flex items-center gap-2.5">
            {scanReport ? (
              <>
                {scanReport.status === "clean" ? (
                  <ShieldCheck size={20} className="text-accent" />
                ) : (
                  <ShieldAlert
                    size={20}
                    className={cn(
                      scanReport.status === "critical"
                        ? "text-danger"
                        : scanReport.status === "warn"
                        ? "text-warning"
                        : "text-text-tertiary"
                    )}
                  />
                )}
                <div>
                  <div className="flex items-center gap-2">
                    <SecurityBadge status={scanReport.status} showLabel />
                  </div>
                  <div className="text-[10px] text-text-tertiary mt-0.5">
                    {(scanReport.findings ?? []).length} finding{(scanReport.findings ?? []).length !== 1 ? "s" : ""} detected
                  </div>
                </div>
              </>
            ) : scanLoading ? (
              <div className="text-xs text-text-tertiary">Loading scan results...</div>
            ) : (
              <div className="text-xs text-text-tertiary">No scan data available</div>
            )}
          </div>

          {/* Divider */}
          <div className="w-px h-8 bg-border" />

          {/* Review status */}
          <div className="flex items-center gap-2">
            <ReviewStatus reviewedAt={skill.reviewed_at} />
            <div>
              <div className="text-xs text-text-secondary">
                {isReviewed ? "Reviewed" : "Unreviewed"}
              </div>
              {isReviewed && skill.reviewed_at && (
                <div className="flex items-center gap-1 text-[10px] text-text-tertiary mt-0.5">
                  <Clock size={9} />
                  {formatRelativeTime(skill.reviewed_at)}
                  {skill.reviewed_by && ` by ${skill.reviewed_by}`}
                </div>
              )}
            </div>
          </div>
        </div>

        {/* Review action button */}
        <button
          onClick={() => {
            if (isReviewed) {
              unreviewMutation.mutate();
            } else {
              reviewMutation.mutate();
            }
          }}
          disabled={isPending}
          className={cn(
            "flex items-center gap-1.5 h-[30px] px-3 text-[12px] font-sans cursor-pointer rounded-md transition-colors duration-150",
            isReviewed
              ? "bg-transparent border border-border text-text-secondary hover:bg-bg-tertiary"
              : "bg-accent text-bg-primary border border-accent hover:opacity-90",
            isPending && "opacity-50 cursor-not-allowed"
          )}
        >
          {isPending
            ? "Updating..."
            : isReviewed
            ? "Unmark Reviewed"
            : "Mark Reviewed"}
        </button>
      </div>

      {/* Scan findings */}
      {scanReport && (
        <ScanFindings
          findings={scanReport.findings ?? []}
          scanStatus={scanReport.status}
        />
      )}
    </div>
  );
}

// ── Placeholder tab (for changelog) ──────────────────────────────
function TabPlaceholder({ label }: { label: string }) {
  return (
    <div className="flex items-center justify-center py-24 text-text-tertiary text-sm">
      {label} — coming soon
    </div>
  );
}

// ── Loading skeleton ──────────────────────────────────────────────
function SkeletonLine({ className }: { className?: string }) {
  return (
    <div
      className={cn(
        "rounded bg-bg-secondary animate-pulse",
        className
      )}
    />
  );
}

// ── Main SkillDetail component ────────────────────────────────────
const TABS: TabDef[] = [
  { id: "content", label: "Content" },
  { id: "files", label: "Files" },
  { id: "versions", label: "Versions" },
  { id: "usage", label: "Usage" },
  { id: "security", label: "Security" },
  { id: "changelog", label: "Changelog", disabled: true },
];

// ── Fetch scan report (raw Chi route, not in generated client) ───
async function fetchScanReport(name: string): Promise<ScanReport | null> {
  try {
    const res = await fetch(`/api/skills/${encodeURIComponent(name)}/scan`, {
      headers: {
        "X-API-Key": localStorage.getItem("skael-api-key") ?? "",
      },
    });
    if (!res.ok) return null;
    return (await res.json()) as ScanReport;
  } catch {
    return null;
  }
}

export function SkillDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useState("content");

  const skillQuery = useQuery({
    queryKey: ["skill", name],
    queryFn: () => getSkill({ path: { name: name! } }).then((r) => r.data as Skill),
    enabled: !!name,
  });

  const activationsQuery = useQuery({
    queryKey: ["skill-activations", name],
    queryFn: () =>
      getSkillActivations({ path: { name: name! } }).then(
        (r) => r.data as ActivationSummary
      ),
    enabled: !!name,
  });

  const versionsQuery = useQuery({
    queryKey: ["skill-versions", name],
    queryFn: () =>
      listSkillVersions({ path: { name: name! } }).then(
        (r) => (r.data as ListVersionsBody)?.versions ?? []
      ),
    enabled: !!name,
  });

  const scanQuery = useQuery({
    queryKey: ["skill-scan", name],
    queryFn: () => fetchScanReport(name!),
    enabled: !!name,
  });

  const importSourceQuery = useQuery({
    queryKey: ["import-source", name],
    queryFn: async () => {
      const res = await fetch(`/api/skills/${encodeURIComponent(name!)}/source`, { credentials: "include" });
      if (!res.ok) return null;
      const data = await res.json();
      return data as { skill_name: string; source_url: string; source_ref: string; commit_sha: string; imported_at: string } | null;
    },
    enabled: !!name,
  });

  const skill = skillQuery.data;
  const activations = activationsQuery.data;
  const versions = versionsQuery.data ?? [];
  const scanReport = scanQuery.data ?? null;
  const importSource = importSourceQuery.data;

  const tags = skill ? extractTags(skill) : [];

  // Active status: last_triggered within 14 days
  const isActive =
    activations?.last_triggered
      ? (Date.now() - new Date(activations.last_triggered).getTime()) / 86_400_000 < 14
      : false;

  const author = (() => {
    if (!skill) return "—";
    // Try to get published_by from latest version via frontmatter or reviewed_by
    const fm = skill.frontmatter as Record<string, unknown> | null;
    if (fm && typeof fm.author === "string") return fm.author;
    if (skill.reviewed_by) return skill.reviewed_by;
    return "—";
  })();

  // Error state
  if (skillQuery.isError) {
    return (
      <div className="flex flex-col items-center justify-center h-full gap-4 text-text-secondary">
        <span className="text-text-tertiary text-sm">Skill not found</span>
        <button
          onClick={() => navigate("/")}
          className="text-accent text-sm hover:underline"
        >
          Back to explorer
        </button>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full overflow-auto">
      {/* Hero header */}
      <div className="px-12 pt-12 pb-0 shrink-0 max-w-[1280px]">
        {/* Back link */}
        <button
          onClick={() => navigate("/")}
          className="flex items-center gap-1.5 text-[11px] text-text-tertiary hover:text-text-secondary mb-5 transition-colors duration-150 cursor-pointer bg-transparent border-none p-0 font-sans"
        >
          <ArrowLeft size={12} />
          Skills
        </button>

        {/* "Skill" label */}
        <div className="text-[11px] uppercase tracking-[0.1em] text-text-tertiary mb-3.5">
          Skill
        </div>

        {/* Title row */}
        <div className="flex items-center justify-between gap-4 mb-3.5">
          <div className="flex items-center gap-3.5 min-w-0">
            {skillQuery.isLoading ? (
              <SkeletonLine className="h-9 w-64" />
            ) : (
              <h1 className="font-mono text-3xl font-medium tracking-tight text-text-primary m-0 whitespace-nowrap">
                {skill?.name}
              </h1>
            )}
            {/* Status dot */}
            <span
              className={cn(
                "size-2.5 rounded-full shrink-0",
                isActive
                  ? "bg-accent shadow-[0_0_8px_var(--color-accent)]"
                  : "bg-warning"
              )}
              title={isActive ? "active" : "stale"}
            />
          </div>

          {/* Action buttons */}
          <div className="flex gap-1.5 shrink-0">
            <button
              onClick={() => {
                if (name) navigator.clipboard?.writeText(`/skills/${name}`);
              }}
              className={cn(
                "flex items-center gap-1.5 h-[30px] px-2.5 text-[12px] font-sans cursor-pointer rounded-md",
                "bg-transparent border border-border text-text-secondary",
                "hover:bg-bg-secondary transition-colors duration-150"
              )}
            >
              Copy path
            </button>
          </div>
        </div>

        {/* Description */}
        {skillQuery.isLoading ? (
          <div className="mb-4 flex flex-col gap-1.5">
            <SkeletonLine className="h-4 w-[520px]" />
            <SkeletonLine className="h-4 w-[380px]" />
          </div>
        ) : (
          <p className="text-sm text-text-secondary m-0 max-w-[720px] leading-relaxed mb-4">
            {skill?.description || "No description."}
          </p>
        )}

        {/* Meta strip */}
        {skillQuery.isLoading ? (
          <div className="flex gap-6 mb-7">
            {[100, 80, 90, 70, 110].map((w, i) => (
              <SkeletonLine key={i} className={`h-9 w-[${w}px]`} />
            ))}
          </div>
        ) : (
          <div className="flex items-center gap-6 mb-7 flex-wrap">
            <MetaCell label="Version" value={skill?.latest_version ? `v${skill.latest_version}` : "—"} />
            <MetaCell label="Author" value={author} />
            <MetaCell
              label="Invocations"
              value={activations?.total_count.toLocaleString() ?? "—"}
            />
            <MetaCell
              label="Unique devs"
              value={activations?.unique_devs ?? "—"}
            />
            <MetaCell
              label="Last updated"
              value={skill ? formatRelativeTime(skill.updated_at) : "—"}
            />
            {importSource && (
              <div className="flex items-center gap-1.5 text-[11px] text-text-tertiary">
                <Download size={11} />
                <span>
                  Imported from{" "}
                  <a
                    href={importSource.source_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-accent hover:underline"
                  >
                    {importSource.source_url.replace("https://github.com/", "")}
                  </a>
                  {importSource.source_ref && ` · ${importSource.source_ref}`}
                  {importSource.commit_sha && ` @ ${importSource.commit_sha.slice(0, 7)}`}
                  {importSource.imported_at && ` · ${formatRelativeTime(importSource.imported_at)}`}
                </span>
              </div>
            )}
            <div className="flex-1" />
            {/* Tags */}
            {tags.length > 0 && (
              <div className="flex gap-2 flex-wrap">
                {tags.map((tag) => (
                  <span
                    key={tag}
                    className="inline-flex items-center gap-1.5 text-[11px] text-text-secondary"
                  >
                    <span
                      className={cn(
                        "size-[5px] rounded-full shrink-0",
                        TAG_COLORS[tag] ?? "bg-text-tertiary"
                      )}
                    />
                    {tag}
                  </span>
                ))}
              </div>
            )}
          </div>
        )}
      </div>

      {/* Sticky tab bar */}
      <div className="sticky top-0 z-10 flex px-12 border-b border-border bg-bg-primary shrink-0">
        <SlidingTabs tabs={TABS} activeTab={activeTab} onChange={setActiveTab} />
      </div>

      {/* Tab content */}
      <div className="px-12 pt-7 pb-12 max-w-[1280px]">
        {activeTab === "content" && skill && <TabContent skill={skill} />}
        {activeTab === "content" && !skill && !skillQuery.isLoading && (
          <TabPlaceholder label="Content" />
        )}
        {activeTab === "files" && skill && (
          <TabFiles skill={skill} versions={versions} />
        )}
        {activeTab === "versions" && (
          <VersionList versions={versions} />
        )}
        {activeTab === "usage" && skill && (
          <TabUsage skill={skill} activations={activations} />
        )}
        {activeTab === "security" && skill && (
          <TabSecurity
            skill={skill}
            scanReport={scanReport}
            scanLoading={scanQuery.isLoading}
          />
        )}
      </div>
    </div>
  );
}
