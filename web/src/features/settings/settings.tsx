import { useState, useRef, useEffect, useCallback } from "react";
import { useQuery } from "@tanstack/react-query";
import { Copy, Check, Eye, EyeOff, AlertTriangle } from "lucide-react";
import { listSkills } from "@/api/sdk.gen";
import type { ListBody } from "@/api/types.gen";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";

// ── Sub-nav sections ─────────────────────────────────────────
const SECTIONS = [
  { id: "workspace", label: "Workspace" },
  { id: "api", label: "API & Keys" },
  { id: "sync", label: "Sync Targets" },
  { id: "danger", label: "Danger Zone" },
] as const;

type SectionId = (typeof SECTIONS)[number]["id"];

// ── Section header ───────────────────────────────────────────
function SectionHeader({
  title,
  desc,
}: {
  title: string;
  desc: string;
}) {
  return (
    <div className="mb-3">
      <h2 className="text-base font-medium text-text-primary m-0 mb-1 tracking-tight">
        {title}
      </h2>
      <p className="text-xs text-text-tertiary m-0">{desc}</p>
    </div>
  );
}

// ── Card ─────────────────────────────────────────────────────
function Card({
  children,
  danger,
}: {
  children: React.ReactNode;
  danger?: boolean;
}) {
  return (
    <div
      className="bg-bg-secondary rounded-lg overflow-hidden"
      style={{
        border: `1px solid ${danger ? "rgba(239,68,68,0.30)" : "var(--color-border)"}`,
      }}
    >
      {children}
    </div>
  );
}

// ── Row ──────────────────────────────────────────────────────
function Row({
  label,
  value,
  mono,
  last,
}: {
  label: string;
  value: React.ReactNode;
  mono?: boolean;
  last?: boolean;
}) {
  return (
    <div
      className="flex justify-between items-center gap-3 px-3.5 py-3"
      style={{ borderBottom: last ? "none" : "1px solid var(--color-border)" }}
    >
      <span className="text-[13px] text-text-secondary whitespace-nowrap shrink-0">
        {label}
      </span>
      <span
        className={[
          "text-[13px] text-text-primary text-right whitespace-nowrap overflow-hidden text-ellipsis min-w-0",
          mono ? "font-mono" : "",
        ].join(" ")}
      >
        {value}
      </span>
    </div>
  );
}

// ── Workspace section ─────────────────────────────────────────
function WorkspaceSection({ skillsTotal }: { skillsTotal: number }) {
  return (
    <div>
      <SectionHeader
        title="Workspace"
        desc="Settings for this workspace and server"
      />
      <Card>
        <Row label="Workspace name" value="skael" />
        <Row label="Server URL" value={window.location.origin} mono />
        <Row label="Platform version" value="v0.1.0" mono />
        <Row
          label="Skills count"
          value={`${skillsTotal} skill${skillsTotal !== 1 ? "s" : ""}`}
          mono
          last
        />
      </Card>
    </div>
  );
}

// ── API & Keys section ────────────────────────────────────────
function ApiSection() {
  const [revealed, setRevealed] = useState(false);
  const [copied, setCopied] = useState(false);

  const maskedKey = "sk-live-" + "•".repeat(12);
  const realKey = "sk-live-••••••••••••"; // placeholder — server doesn't expose key via API

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(realKey);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard denied — silently ignore
    }
  };

  return (
    <div>
      <SectionHeader title="API & Keys" desc="Programmatic access to your skills" />
      <Card>
        <div className="p-3.5">
          <div className="text-xs text-text-secondary mb-2">API Key</div>
          <div className="flex items-center gap-2">
            <div className="flex-1 px-3 py-[7px] bg-bg-tertiary border border-border rounded-[5px] font-mono text-xs text-text-primary overflow-hidden text-ellipsis whitespace-nowrap">
              {revealed ? realKey : maskedKey}
            </div>
            <button
              onClick={() => setRevealed((r) => !r)}
              className="flex items-center gap-1.5 h-7 px-3 text-xs text-text-secondary border border-border bg-bg-secondary hover:bg-bg-tertiary rounded-[5px] cursor-pointer transition-colors duration-100 whitespace-nowrap"
            >
              {revealed ? (
                <EyeOff className="size-3" />
              ) : (
                <Eye className="size-3" />
              )}
              {revealed ? "Hide" : "Reveal"}
            </button>
            <button
              onClick={handleCopy}
              className="flex items-center gap-1.5 h-7 px-3 text-xs text-text-secondary border border-border bg-bg-secondary hover:bg-bg-tertiary rounded-[5px] cursor-pointer transition-colors duration-100 whitespace-nowrap"
            >
              {copied ? (
                <Check className="size-3 text-accent" />
              ) : (
                <Copy className="size-3" />
              )}
              {copied ? "Copied" : "Copy"}
            </button>
          </div>
          <div className="text-[11px] text-text-tertiary mt-2 font-mono">
            Configure in your agent with{" "}
            <code className="text-text-secondary">SKAEL_API_KEY</code>
          </div>
        </div>
      </Card>
    </div>
  );
}

// ── Sync targets section ──────────────────────────────────────
function SyncTargetsSection() {
  const agents = [
    { name: "claude-code", path: "~/.claude/skills/" },
    { name: "codex", path: "~/.codex/skills/" },
  ];

  return (
    <div>
      <SectionHeader
        title="Sync Targets"
        desc="Where your skills get installed"
      />
      <Card>
        <div className="px-3.5 py-3" style={{ borderBottom: "1px solid var(--color-border)" }}>
          <p className="text-[13px] text-text-secondary m-0">
            Run{" "}
            <code className="font-mono text-text-primary bg-bg-tertiary px-1.5 py-0.5 rounded text-xs">
              skael doctor
            </code>{" "}
            to see sync target status and diagnose issues.
          </p>
        </div>
        {agents.map((agent, i) => (
          <div
            key={agent.name}
            className="flex items-center gap-3 px-3.5 py-3"
            style={{
              borderBottom:
                i < agents.length - 1
                  ? "1px solid var(--color-border)"
                  : "none",
            }}
          >
            <div className="size-2 rounded-full bg-text-tertiary shrink-0" />
            <div className="flex-1 min-w-0">
              <div className="text-[13px] text-text-primary font-mono font-medium">
                {agent.name}
              </div>
              <div className="text-[11px] text-text-tertiary font-mono">
                {agent.path}
              </div>
            </div>
            <span className="text-[10px] font-mono text-text-tertiary bg-bg-tertiary px-1.5 py-0.5 rounded uppercase tracking-wide">
              cli only
            </span>
          </div>
        ))}
      </Card>
    </div>
  );
}

// ── Danger zone section ───────────────────────────────────────
function DangerSection() {
  const [dialogOpen, setDialogOpen] = useState(false);

  const handleConfirm = () => {
    setDialogOpen(false);
    alert("Not implemented yet");
  };

  return (
    <div>
      <SectionHeader
        title="Danger Zone"
        desc="Irreversible and destructive actions"
      />
      <Card danger>
        <div className="px-3.5 py-3 flex items-center justify-between gap-3">
          <div>
            <div className="text-[13px] text-text-primary mb-0.5">
              Regenerate API Key
            </div>
            <div className="text-[11px] text-text-tertiary">
              All existing integrations will break and need updating
            </div>
          </div>
          <Button
            variant="destructive"
            size="sm"
            className="shrink-0"
            onClick={() => setDialogOpen(true)}
          >
            Regenerate
          </Button>
        </div>
      </Card>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
          <DialogHeader>
            <div className="flex items-center gap-2 mb-1">
              <AlertTriangle className="size-4 text-destructive shrink-0" />
              <DialogTitle className="text-text-primary">
                Regenerate API Key?
              </DialogTitle>
            </div>
            <DialogDescription className="text-text-secondary">
              This will immediately invalidate your current API key. All CLI
              clients and integrations using the old key will stop working until
              reconfigured.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              className="border-border text-text-secondary"
              onClick={() => setDialogOpen(false)}
            >
              Cancel
            </Button>
            <Button variant="destructive" size="sm" onClick={handleConfirm}>
              Yes, regenerate
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// ── Main page ─────────────────────────────────────────────────
export function Settings() {
  const [activeSection, setActiveSection] = useState<SectionId>("workspace");
  const sectionRefs = useRef<Partial<Record<SectionId, HTMLDivElement | null>>>(
    {}
  );
  const scrollRef = useRef<HTMLDivElement | null>(null);

  const { data: listData } = useQuery({
    queryKey: ["skills", "list"],
    queryFn: async () => {
      const res = await listSkills();
      return res.data as ListBody | undefined;
    },
  });

  const skillsTotal = listData?.total ?? 0;

  // Track active section based on scroll position
  const handleScroll = useCallback(() => {
    const container = scrollRef.current;
    if (!container) return;
    const containerTop = container.getBoundingClientRect().top;

    let current: SectionId = "workspace";
    for (const s of SECTIONS) {
      const el = sectionRefs.current[s.id];
      if (!el) continue;
      const rect = el.getBoundingClientRect();
      if (rect.top - containerTop <= 80) {
        current = s.id;
      }
    }
    setActiveSection(current);
  }, []);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    el.addEventListener("scroll", handleScroll, { passive: true });
    return () => el.removeEventListener("scroll", handleScroll);
  }, [handleScroll]);

  const scrollTo = (id: SectionId) => {
    setActiveSection(id);
    sectionRefs.current[id]?.scrollIntoView({
      behavior: "smooth",
      block: "start",
    });
  };

  return (
    <div className="flex h-full overflow-hidden">
      {/* Sub-nav */}
      <div
        className="w-[200px] shrink-0 bg-bg-primary px-3 py-6"
        style={{ borderRight: "1px solid var(--color-border)" }}
      >
        <div className="text-[11px] text-text-tertiary font-mono uppercase tracking-widest px-2.5 pb-3">
          Settings
        </div>
        {SECTIONS.map((s) => (
          <button
            key={s.id}
            onClick={() => scrollTo(s.id)}
            className={[
              "w-full text-left px-2.5 py-1.5 text-[13px] rounded-[5px] cursor-pointer transition-colors duration-100 mb-0.5 font-sans",
              activeSection === s.id
                ? "bg-bg-tertiary text-text-primary font-medium"
                : "text-text-secondary hover:bg-bg-secondary",
            ].join(" ")}
          >
            {s.label}
          </button>
        ))}
      </div>

      {/* Scrollable content */}
      <div ref={scrollRef} className="flex-1 overflow-auto px-10 py-10">
        <div className="max-w-[640px] mx-auto flex flex-col gap-9">
          <div
            ref={(el) => {
              sectionRefs.current.workspace = el;
            }}
          >
            <WorkspaceSection skillsTotal={skillsTotal} />
          </div>

          <div
            ref={(el) => {
              sectionRefs.current.api = el;
            }}
          >
            <ApiSection />
          </div>

          <div
            ref={(el) => {
              sectionRefs.current.sync = el;
            }}
          >
            <SyncTargetsSection />
          </div>

          <div
            ref={(el) => {
              sectionRefs.current.danger = el;
            }}
          >
            <DangerSection />
          </div>

          {/* Bottom spacer */}
          <div className="h-10" />
        </div>
      </div>
    </div>
  );
}
