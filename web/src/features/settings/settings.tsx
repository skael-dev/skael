import { useState, useRef, useEffect, useCallback } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Copy, Check, Plus, Trash2, AlertTriangle, Key } from "lucide-react";
import { listSkills, listApiKeys, createApiKey, deleteApiKey } from "@/api/sdk.gen";
import type { ListBody, ListKeysBody, ApiKeyInfo, CreateKeyResponse } from "@/api/types.gen";
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

// ── Relative time helper ─────────────────────────────────────
function relativeTime(dateStr: string | null): string {
  if (!dateStr) return "never";
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffSec = Math.floor(diffMs / 1000);
  if (diffSec < 60) return "just now";
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  const diffDay = Math.floor(diffHr / 24);
  if (diffDay < 30) return `${diffDay}d ago`;
  const diffMon = Math.floor(diffDay / 30);
  return `${diffMon}mo ago`;
}

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
  const queryClient = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<ApiKeyInfo | null>(null);
  const [newKeyName, setNewKeyName] = useState("");
  const [createdKey, setCreatedKey] = useState<CreateKeyResponse | null>(null);
  const [copied, setCopied] = useState(false);

  const { data: keysData, isLoading } = useQuery({
    queryKey: ["api-keys"],
    queryFn: async () => {
      const res = await listApiKeys();
      return res.data as ListKeysBody | undefined;
    },
  });

  const keys = keysData?.keys ?? [];

  const createMutation = useMutation({
    mutationFn: async (name: string) => {
      const res = await createApiKey({ body: { name } });
      return res.data as CreateKeyResponse;
    },
    onSuccess: (data) => {
      setCreatedKey(data);
      setNewKeyName("");
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      await deleteApiKey({ path: { id } });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["api-keys"] });
      setDeleteTarget(null);
    },
  });

  const handleCopyKey = async (key: string) => {
    try {
      await navigator.clipboard.writeText(key);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard denied
    }
  };

  const handleCloseCreate = () => {
    setCreateOpen(false);
    setCreatedKey(null);
    setNewKeyName("");
    createMutation.reset();
    queryClient.invalidateQueries({ queryKey: ["api-keys"] });
  };

  return (
    <div>
      <SectionHeader title="API & Keys" desc="Programmatic access to your skills" />
      <Card>
        {/* Key list */}
        {isLoading ? (
          <div className="px-3.5 py-6 text-center text-xs text-text-tertiary">
            Loading keys...
          </div>
        ) : keys.length === 0 ? (
          <div className="px-3.5 py-6 text-center">
            <Key className="size-5 text-text-tertiary mx-auto mb-2" />
            <div className="text-[13px] text-text-secondary mb-1">No API keys yet</div>
            <div className="text-[11px] text-text-tertiary">
              Create a key to authenticate CLI and API access.
            </div>
          </div>
        ) : (
          keys.map((key, i) => (
            <div
              key={key.id}
              className="flex items-center gap-3 px-3.5 py-3"
              style={{
                borderBottom:
                  i < keys.length - 1 ? "1px solid var(--color-border)" : "none",
              }}
            >
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2 mb-0.5">
                  <span className="text-[13px] text-text-primary font-medium truncate">
                    {key.name}
                  </span>
                  <code className="text-[11px] font-mono text-text-tertiary bg-bg-tertiary px-1.5 py-0.5 rounded border border-border">
                    {key.prefix}...
                  </code>
                </div>
                <div className="flex items-center gap-3 text-[11px] text-text-tertiary">
                  <span>Last used: {relativeTime(key.last_used_at)}</span>
                  <span>Created: {relativeTime(key.created_at)}</span>
                </div>
              </div>
              <button
                onClick={() => setDeleteTarget(key)}
                className="flex items-center justify-center size-7 text-text-tertiary hover:text-destructive border border-transparent hover:border-destructive/30 rounded-[5px] cursor-pointer transition-colors duration-100"
              >
                <Trash2 className="size-3.5" />
              </button>
            </div>
          ))
        )}

        {/* Create button */}
        <div
          className="px-3.5 py-3"
          style={{ borderTop: keys.length > 0 ? "1px solid var(--color-border)" : "none" }}
        >
          <Button
            variant="outline"
            size="sm"
            className="w-full border-border text-text-secondary"
            onClick={() => setCreateOpen(true)}
          >
            <Plus className="size-3 mr-1.5" />
            Create API Key
          </Button>
        </div>
      </Card>

      {/* Create key dialog */}
      <Dialog open={createOpen} onOpenChange={(open) => { if (!open) handleCloseCreate(); }}>
        <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
          <DialogHeader>
            <DialogTitle className="text-text-primary">
              {createdKey ? "API Key Created" : "Create API Key"}
            </DialogTitle>
            <DialogDescription className="text-text-secondary">
              {createdKey
                ? "Copy your key now. It won't be shown again."
                : "Give your key a name to identify it later."}
            </DialogDescription>
          </DialogHeader>

          {createdKey ? (
            <div>
              <div className="p-3 bg-bg-tertiary border border-border rounded-lg mb-3">
                <code className="text-[12px] font-mono text-text-primary break-all leading-relaxed">
                  {createdKey.key}
                </code>
              </div>
              <button
                onClick={() => handleCopyKey(createdKey.key)}
                className="flex items-center gap-1.5 w-full justify-center h-8 px-3 text-xs border border-border bg-bg-secondary hover:bg-bg-tertiary rounded-[5px] cursor-pointer transition-colors duration-100 font-sans text-text-secondary mb-3"
              >
                {copied ? (
                  <Check className="size-3 text-accent" />
                ) : (
                  <Copy className="size-3" />
                )}
                {copied ? "Copied" : "Copy to clipboard"}
              </button>
              <div className="flex items-start gap-2 p-2.5 bg-warning/10 border border-warning/20 rounded-md">
                <AlertTriangle className="size-3.5 text-warning shrink-0 mt-0.5" />
                <span className="text-[11px] text-warning leading-relaxed">
                  This key won't be shown again. Copy it now.
                </span>
              </div>
            </div>
          ) : (
            <div>
              <input
                type="text"
                placeholder="e.g. CI/CD Pipeline"
                value={newKeyName}
                onChange={(e) => setNewKeyName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && newKeyName.trim()) {
                    createMutation.mutate(newKeyName.trim());
                  }
                }}
                className="w-full px-3 py-2 bg-bg-tertiary border border-border rounded-[5px] text-sm text-text-primary outline-none focus:border-border-active transition-colors font-sans placeholder:text-text-tertiary"
                autoFocus
              />
            </div>
          )}

          <DialogFooter>
            {createdKey ? (
              <Button
                size="sm"
                className="bg-accent text-bg-primary hover:bg-accent/90"
                onClick={handleCloseCreate}
              >
                Done
              </Button>
            ) : (
              <>
                <Button
                  variant="outline"
                  size="sm"
                  className="border-border text-text-secondary"
                  onClick={handleCloseCreate}
                >
                  Cancel
                </Button>
                <Button
                  size="sm"
                  className="bg-accent text-bg-primary hover:bg-accent/90"
                  disabled={!newKeyName.trim() || createMutation.isPending}
                  onClick={() => createMutation.mutate(newKeyName.trim())}
                >
                  {createMutation.isPending ? "Creating..." : "Create"}
                </Button>
              </>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => { if (!open) setDeleteTarget(null); }}>
        <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
          <DialogHeader>
            <div className="flex items-center gap-2 mb-1">
              <AlertTriangle className="size-4 text-destructive shrink-0" />
              <DialogTitle className="text-text-primary">
                Delete API Key?
              </DialogTitle>
            </div>
            <DialogDescription className="text-text-secondary">
              This will permanently delete the key{" "}
              <strong className="text-text-primary">{deleteTarget?.name}</strong>.
              Any integrations using this key will stop working.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              className="border-border text-text-secondary"
              onClick={() => setDeleteTarget(null)}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              disabled={deleteMutation.isPending}
              onClick={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
            >
              {deleteMutation.isPending ? "Deleting..." : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
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
            className="shrink-0 opacity-50 cursor-not-allowed"
            disabled
            title="Coming in a future release"
          >
            Regenerate
          </Button>
        </div>
      </Card>
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
