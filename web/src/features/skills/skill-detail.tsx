import { useState, useEffect, useRef, useLayoutEffect } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { getSkill, getSkillActivations } from "@/api/sdk.gen";
import type { Skill, ActivationSummary } from "@/api/types.gen";
import { MarkdownRenderer } from "@/features/skills/markdown-renderer";
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

// ── Placeholder tabs ──────────────────────────────────────────────
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

  const skill = skillQuery.data;
  const activations = activationsQuery.data;

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
        {activeTab === "files" && <TabPlaceholder label="Files tab" />}
        {activeTab === "versions" && <TabPlaceholder label="Versions tab" />}
        {activeTab === "usage" && <TabPlaceholder label="Usage tab" />}
        {activeTab === "security" && <TabPlaceholder label="Security tab" />}
      </div>
    </div>
  );
}
