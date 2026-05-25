import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { UserPlus, EyeOff, GitMerge } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { toast } from "sonner";
import { cn } from "@/lib/utils";

type UnregisteredSkill = {
  name: string;
  activations: number;
  unique_devs: number;
  last_triggered: string | null;
  first_seen: string | null;
};

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
  return `${Math.floor(diffDay / 30)}mo ago`;
}

async function fetchUnregistered(days: number): Promise<UnregisteredSkill[]> {
  const res = await fetch(`/api/analytics/unregistered?days=${days}`, { credentials: "include" });
  if (!res.ok) return [];
  return res.json();
}

async function dismissSkill(name: string): Promise<void> {
  const res = await fetch("/api/analytics/dismiss", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({ name }),
  });
  if (!res.ok) throw new Error("Failed to dismiss");
}

async function registerSkill(name: string): Promise<void> {
  const res = await fetch("/api/skills/register", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.detail || body.title || "Failed to register");
  }
}

export function UnregisteredTab({ days }: { days: number }) {
  const queryClient = useQueryClient();
  const queryKey = ["analytics", "unregistered", days];
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const { data, isLoading } = useQuery({
    queryKey,
    queryFn: () => fetchUnregistered(days),
  });

  const skills = data ?? [];
  const anyChecked = selected.size > 0;
  const allChecked = skills.length > 0 && selected.size === skills.length;

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
      setSelected(new Set(skills.map((s) => s.name)));
    }
  }

  const registerMutation = useMutation({
    mutationFn: async (names: string[]) => {
      for (const name of names) await registerSkill(name);
    },
    onSuccess: () => {
      const count = selected.size;
      toast.success(`Registered ${count} skill${count !== 1 ? "s" : ""}`);
      setSelected(new Set());
      queryClient.invalidateQueries({ queryKey: ["analytics"] });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to register");
    },
  });

  const mergeMutation = useMutation({
    mutationFn: async ({ source, target }: { source: string; target: string }) => {
      const res = await fetch("/api/skills/merge", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ source, target }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.detail || body.title || "Failed to merge");
      }
    },
    onSuccess: () => {
      toast.success("Skills merged");
      setSelected(new Set());
      queryClient.invalidateQueries({ queryKey: ["analytics"] });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to merge");
    },
  });

  const handleMerge = async (sourceName: string) => {
    const target = window.prompt(`Merge "${sourceName}" into which skill? Enter the target skill name:`);
    if (!target) return;
    try {
      await registerSkill(sourceName);
    } catch {
      // already registered is fine
    }
    mergeMutation.mutate({ source: sourceName, target });
  };

  const dismissMutation = useMutation({
    mutationFn: async (names: string[]) => {
      for (const name of names) await dismissSkill(name);
    },
    onSuccess: () => {
      const count = selected.size;
      toast.success(`Dismissed ${count} skill${count !== 1 ? "s" : ""}`);
      setSelected(new Set());
      queryClient.invalidateQueries({ queryKey });
    },
    onError: () => {
      toast.error("Failed to dismiss");
    },
  });

  if (isLoading) {
    return (
      <div className="space-y-px">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="h-12 bg-bg-secondary animate-pulse-soft rounded mb-1" />
        ))}
      </div>
    );
  }

  if (skills.length === 0) {
    return (
      <div className="text-center py-16 text-text-secondary">
        <div className="text-sm mb-2">No unregistered skills detected</div>
        <div className="text-xs text-text-tertiary">
          All skill activations match registered skills
        </div>
      </div>
    );
  }

  return (
    <div>
      {/* Bulk actions bar */}
      {anyChecked && (
        <div className="flex items-center gap-3 mb-3 px-3.5 py-2 bg-bg-secondary border border-border rounded-lg">
          <Checkbox checked={allChecked} onCheckedChange={toggleAll} />
          <span className="text-xs text-text-secondary">{selected.size} selected</span>
          <div className="ml-auto flex items-center gap-2">
            <Button
              size="sm"
              variant="outline"
              className="h-7 text-xs"
              disabled={registerMutation.isPending}
              onClick={() => registerMutation.mutate(Array.from(selected))}
            >
              <UserPlus size={13} className="mr-1.5" />
              {registerMutation.isPending ? "Registering..." : "Register"}
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="h-7 text-xs text-text-tertiary"
              disabled={dismissMutation.isPending}
              onClick={() => dismissMutation.mutate(Array.from(selected))}
            >
              <EyeOff size={13} className="mr-1.5" />
              {dismissMutation.isPending ? "Dismissing..." : "Dismiss"}
            </Button>
          </div>
        </div>
      )}

      {/* Column headers */}
      <div
        className="grid items-center gap-4 px-3.5 py-2 text-[10px] text-text-tertiary uppercase tracking-[0.08em] border-b border-border"
        style={{ gridTemplateColumns: "28px 1fr 80px 60px 100px 100px 60px" }}
      >
        <span />
        <span>Skill</span>
        <span className="text-right">Activations</span>
        <span className="text-right">Devs</span>
        <span className="text-right">Last triggered</span>
        <span className="text-right">First seen</span>
        <span />
      </div>

      {/* Rows */}
      {skills.map((sk) => (
        <div
          key={sk.name}
          className="group grid items-center gap-4 px-3.5 py-3 border-b border-border hover:bg-bg-secondary transition-colors"
          style={{ gridTemplateColumns: "28px 1fr 80px 60px 100px 100px 60px" }}
        >
          {/* Checkbox */}
          <div
            className={cn(
              "flex items-center justify-center transition-opacity duration-150",
              anyChecked ? "opacity-100" : "opacity-0 group-hover:opacity-100"
            )}
          >
            <Checkbox
              checked={selected.has(sk.name)}
              onCheckedChange={(v) => toggleOne(sk.name, v === true)}
            />
          </div>

          {/* Name */}
          <span className="font-mono text-[13px] text-text-primary font-medium truncate">
            {sk.name}
            {sk.name.includes(":") && (
              <span className="text-[10px] text-text-tertiary ml-1">
                → {sk.name.split(":").pop()}
              </span>
            )}
          </span>

          {/* Activations */}
          <span className="text-[13px] text-text-primary text-right" style={{ fontVariantNumeric: "tabular-nums" }}>
            {sk.activations.toLocaleString()}
          </span>

          {/* Devs */}
          <span className="text-[13px] text-text-primary text-right" style={{ fontVariantNumeric: "tabular-nums" }}>
            {sk.unique_devs}
          </span>

          {/* Last triggered */}
          <span className="text-[11px] text-text-tertiary text-right">
            {formatRelativeTime(sk.last_triggered)}
          </span>

          {/* First seen */}
          <span className="text-[11px] text-text-tertiary text-right">
            {formatRelativeTime(sk.first_seen)}
          </span>

          {/* Icon actions */}
          <div className="flex items-center justify-end gap-1">
            <div className="relative group/register">
              <button
                onClick={() => registerMutation.mutate([sk.name])}
                disabled={registerMutation.isPending}
                className="p-1.5 rounded-md text-text-tertiary hover:text-accent hover:bg-accent/10 transition-colors cursor-pointer disabled:opacity-50"
              >
                <UserPlus size={14} />
              </button>
              <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1.5 px-2 py-1 text-[10px] text-text-primary bg-bg-tertiary border border-border rounded whitespace-nowrap opacity-0 pointer-events-none group-hover/register:opacity-100 transition-opacity z-10">
                Register
              </div>
            </div>
            <div className="relative group/merge">
              <button
                onClick={() => handleMerge(sk.name)}
                disabled={mergeMutation.isPending}
                className="p-1.5 rounded-md text-text-tertiary hover:text-info hover:bg-info/10 transition-colors cursor-pointer disabled:opacity-50"
              >
                <GitMerge size={14} />
              </button>
              <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1.5 px-2 py-1 text-[10px] text-text-primary bg-bg-tertiary border border-border rounded whitespace-nowrap opacity-0 pointer-events-none group-hover/merge:opacity-100 transition-opacity z-10">
                Merge into...
              </div>
            </div>
            <div className="relative group/dismiss">
              <button
                onClick={() => dismissMutation.mutate([sk.name])}
                disabled={dismissMutation.isPending}
                className="p-1.5 rounded-md text-text-tertiary hover:text-warning hover:bg-warning/10 transition-colors cursor-pointer disabled:opacity-50"
              >
                <EyeOff size={14} />
              </button>
              <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-1.5 px-2 py-1 text-[10px] text-text-primary bg-bg-tertiary border border-border rounded whitespace-nowrap opacity-0 pointer-events-none group-hover/dismiss:opacity-100 transition-opacity z-10">
                Dismiss
              </div>
            </div>
          </div>
        </div>
      ))}
    </div>
  );
}
