import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { UserPlus, EyeOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";

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

  const { data, isLoading } = useQuery({
    queryKey,
    queryFn: () => fetchUnregistered(days),
  });

  const skills = data ?? [];

  const registerMutation = useMutation({
    mutationFn: registerSkill,
    onSuccess: (_data, name) => {
      toast.success(`Registered — ${name} is now in the registry`);
      queryClient.invalidateQueries({ queryKey: ["analytics"] });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to register");
    },
  });

  const dismissMutation = useMutation({
    mutationFn: dismissSkill,
    onSuccess: (_data, name) => {
      toast.success(`Dismissed — ${name} hidden from unregistered`);
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
      <div
        className="grid gap-4 px-3.5 py-2 text-[10px] text-text-tertiary uppercase tracking-[0.08em] border-b border-border"
        style={{ gridTemplateColumns: "1fr 80px 80px 100px 100px 140px" }}
      >
        <span>Skill</span>
        <span className="text-right">Activations</span>
        <span className="text-right">Devs</span>
        <span className="text-right">Last triggered</span>
        <span className="text-right">First seen</span>
        <span className="text-right">Actions</span>
      </div>

      {skills.map((sk) => (
        <div
          key={sk.name}
          className="grid gap-4 items-center px-3.5 py-3 border-b border-border hover:bg-bg-secondary transition-colors"
          style={{ gridTemplateColumns: "1fr 80px 80px 100px 100px 140px" }}
        >
          <span className="font-mono text-[13px] text-text-primary font-medium truncate">
            {sk.name}
          </span>
          <span className="text-[13px] text-text-primary text-right" style={{ fontVariantNumeric: "tabular-nums" }}>
            {sk.activations.toLocaleString()}
          </span>
          <span className="text-[13px] text-text-primary text-right" style={{ fontVariantNumeric: "tabular-nums" }}>
            {sk.unique_devs}
          </span>
          <span className="text-[11px] text-text-tertiary text-right">
            {formatRelativeTime(sk.last_triggered)}
          </span>
          <span className="text-[11px] text-text-tertiary text-right">
            {formatRelativeTime(sk.first_seen)}
          </span>
          <div className="flex items-center justify-end gap-1.5">
            <Button
              size="sm"
              variant="outline"
              className="h-7 text-[11px] px-2"
              disabled={registerMutation.isPending}
              onClick={() => registerMutation.mutate(sk.name)}
            >
              <UserPlus size={12} className="mr-1" />
              Register
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="h-7 text-[11px] px-2 text-text-tertiary"
              disabled={dismissMutation.isPending}
              onClick={() => dismissMutation.mutate(sk.name)}
            >
              <EyeOff size={12} className="mr-1" />
              Dismiss
            </Button>
          </div>
        </div>
      ))}
    </div>
  );
}
