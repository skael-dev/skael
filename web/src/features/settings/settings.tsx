import { useState, useRef, useEffect, useCallback } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Copy, Check, Plus, Trash2, AlertTriangle, Key, Lock, Users, RotateCcw, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { listSkills, listApiKeys, createApiKey, deleteApiKey } from "@/api/sdk.gen";
import type { ListBody, ListKeysBody, ApiKeyInfo, CreateKeyResponse } from "@/api/types.gen";
import { useAuth } from "@/app/auth-provider";
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
const ALL_SECTIONS = [
  { id: "workspace", label: "Workspace" },
  { id: "password", label: "Change Password" },
  { id: "api", label: "API & Keys" },
  { id: "team", label: "Team" },
  { id: "sync", label: "Sync Targets" },
] as const;

type SectionId = (typeof ALL_SECTIONS)[number]["id"];

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

  const { data: keysData, isLoading, isError: keysError, refetch: refetchKeys } = useQuery({
    queryKey: ["api-keys"],
    queryFn: async () => {
      const res = await listApiKeys();
      if (res.error) throw res.error;
      return res.data as ListKeysBody | undefined;
    },
  });

  const keys = keysData?.keys ?? [];

  const createMutation = useMutation({
    mutationFn: async (name: string) => {
      const res = await createApiKey({ body: { name } });
      if (res.error) throw res.error;
      return res.data as CreateKeyResponse;
    },
    onSuccess: (data) => {
      setCreatedKey(data);
      setNewKeyName("");
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await deleteApiKey({ path: { id } });
      if (res.error) throw res.error;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["api-keys"] });
      setDeleteTarget(null);
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to delete API key");
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
        ) : keysError ? (
          <div className="px-3.5 py-6 text-center">
            <div className="text-[13px] text-text-secondary mb-1">Couldn't load API keys</div>
            <button
              onClick={() => refetchKeys()}
              className="text-[11px] text-accent hover:underline"
            >
              Retry
            </button>
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

// ── Change Password section ───────────────────────────────────
function ChangePasswordSection() {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSuccess(false);
    setIsSubmitting(true);
    try {
      const res = await fetch("/api/auth/change-password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: "Failed to change password" }));
        throw new Error(err.detail || err.error || "Failed to change password");
      }
      setSuccess(true);
      setCurrentPassword("");
      setNewPassword("");
      setTimeout(() => setSuccess(false), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to change password");
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <div>
      <SectionHeader title="Change Password" desc="Update your account password" />
      <Card>
        <form onSubmit={handleSubmit} className="p-3.5 flex flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <label htmlFor="settings-current-pw" className="text-[11px] font-medium text-text-secondary uppercase tracking-wider">
              Current Password
            </label>
            <input
              id="settings-current-pw"
              type="password"
              autoComplete="current-password"
              required
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              placeholder="Enter current password"
              className="w-full h-9 rounded-md bg-bg-primary border border-border px-3 text-sm
                text-text-primary placeholder:text-text-tertiary
                focus:outline-none focus:border-border-active focus:ring-1 focus:ring-border-active
                transition-colors"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label htmlFor="settings-new-pw" className="text-[11px] font-medium text-text-secondary uppercase tracking-wider">
              New Password
            </label>
            <input
              id="settings-new-pw"
              type="password"
              autoComplete="new-password"
              required
              minLength={8}
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder="Min. 8 characters"
              className="w-full h-9 rounded-md bg-bg-primary border border-border px-3 text-sm
                text-text-primary placeholder:text-text-tertiary
                focus:outline-none focus:border-border-active focus:ring-1 focus:ring-border-active
                transition-colors"
            />
          </div>

          {error && (
            <p className="text-sm text-danger bg-danger/10 border border-danger/20 rounded-md px-3 py-2">
              {error}
            </p>
          )}

          {success && (
            <p className="text-sm text-accent bg-accent/10 border border-accent/20 rounded-md px-3 py-2">
              Password changed successfully.
            </p>
          )}

          <Button
            type="submit"
            disabled={isSubmitting}
            size="sm"
            className="w-full bg-accent text-bg-primary hover:bg-accent/90 disabled:opacity-50"
          >
            {isSubmitting ? (
              <>
                <Loader2 size={14} className="animate-spin mr-1.5" />
                Changing...
              </>
            ) : (
              <>
                <Lock size={13} className="mr-1.5" />
                Change Password
              </>
            )}
          </Button>
        </form>
      </Card>
    </div>
  );
}

// ── Team section (owner only) ────────────────────────────────
type TeamUser = {
  id: string;
  email: string;
  name: string;
  role: string;
  created_at: string;
};

function TeamSection() {
  const [resetTarget, setResetTarget] = useState<TeamUser | null>(null);
  const [tempPassword, setTempPassword] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const { data: users, isLoading, isError, refetch } = useQuery({
    queryKey: ["admin", "users"],
    queryFn: async () => {
      const res = await fetch("/api/admin/users", { credentials: "include" });
      if (!res.ok) throw new Error("Failed to fetch users");
      return res.json() as Promise<TeamUser[]>;
    },
  });

  const resetMutation = useMutation({
    mutationFn: async (userId: string) => {
      const res = await fetch("/api/admin/reset-password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ user_id: userId }),
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: "Failed to reset password" }));
        throw new Error(err.detail || err.error || "Failed to reset password");
      }
      return res.json() as Promise<{ temporary_password: string }>;
    },
    onSuccess: (data) => {
      setTempPassword(data.temporary_password);
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to reset password");
    },
  });

  const handleCopy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard denied
    }
  };

  const handleCloseReset = () => {
    setResetTarget(null);
    setTempPassword(null);
    setCopied(false);
    resetMutation.reset();
  };

  return (
    <div>
      <SectionHeader title="Team" desc="Manage team members and credentials" />
      <Card>
        {isLoading ? (
          <div className="px-3.5 py-6 text-center text-xs text-text-tertiary">Loading team members...</div>
        ) : isError ? (
          <div className="px-3.5 py-6 text-center">
            <div className="text-[13px] text-text-secondary mb-1">Couldn't load team</div>
            <button onClick={() => refetch()} className="text-[11px] text-accent hover:underline">Retry</button>
          </div>
        ) : !users || users.length === 0 ? (
          <div className="px-3.5 py-6 text-center">
            <Users className="size-5 text-text-tertiary mx-auto mb-2" />
            <div className="text-[13px] text-text-secondary">No team members yet</div>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left">
              <thead>
                <tr className="text-[10px] text-text-tertiary uppercase tracking-[0.08em] border-b border-border">
                  <th className="px-3.5 py-2.5 font-medium">Email</th>
                  <th className="px-3.5 py-2.5 font-medium">Name</th>
                  <th className="px-3.5 py-2.5 font-medium">Role</th>
                  <th className="px-3.5 py-2.5 font-medium">Created</th>
                  <th className="px-3.5 py-2.5 font-medium text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {users.map((u, i) => (
                  <tr
                    key={u.id}
                    className="text-[13px]"
                    style={{ borderBottom: i < users.length - 1 ? "1px solid var(--color-border)" : "none" }}
                  >
                    <td className="px-3.5 py-3 text-text-primary font-mono text-[12px]">{u.email}</td>
                    <td className="px-3.5 py-3 text-text-secondary">{u.name}</td>
                    <td className="px-3.5 py-3">
                      <span className="text-[10px] font-mono text-text-tertiary bg-bg-tertiary px-1.5 py-0.5 rounded uppercase tracking-wide">
                        {u.role}
                      </span>
                    </td>
                    <td className="px-3.5 py-3 text-text-tertiary text-[11px]">{relativeTime(u.created_at)}</td>
                    <td className="px-3.5 py-3 text-right">
                      <button
                        onClick={() => setResetTarget(u)}
                        className="inline-flex items-center gap-1 text-[11px] text-text-secondary hover:text-accent transition-colors cursor-pointer bg-transparent border-none font-sans"
                      >
                        <RotateCcw className="size-3" />
                        Reset Password
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Dialog open={!!resetTarget} onOpenChange={(open) => { if (!open) handleCloseReset(); }}>
        <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
          <DialogHeader>
            <DialogTitle className="text-text-primary">
              {tempPassword ? "Password Reset" : "Reset Password?"}
            </DialogTitle>
            <DialogDescription className="text-text-secondary">
              {tempPassword
                ? "Copy this temporary password now. It will not be shown again."
                : `This will generate a temporary password for ${resetTarget?.email}. They will be required to change it on next login.`}
            </DialogDescription>
          </DialogHeader>

          {tempPassword ? (
            <div>
              <div className="p-3 bg-bg-tertiary border border-border rounded-lg mb-3">
                <code className="text-[12px] font-mono text-text-primary break-all leading-relaxed">
                  {tempPassword}
                </code>
              </div>
              <button
                onClick={() => handleCopy(tempPassword)}
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
                  This password will not be shown again. Copy it now.
                </span>
              </div>
            </div>
          ) : null}

          <DialogFooter>
            {tempPassword ? (
              <Button
                size="sm"
                className="bg-accent text-bg-primary hover:bg-accent/90"
                onClick={handleCloseReset}
              >
                Done
              </Button>
            ) : (
              <>
                <Button
                  variant="outline"
                  size="sm"
                  className="border-border text-text-secondary"
                  onClick={handleCloseReset}
                >
                  Cancel
                </Button>
                <Button
                  size="sm"
                  className="bg-accent text-bg-primary hover:bg-accent/90"
                  disabled={resetMutation.isPending}
                  onClick={() => resetTarget && resetMutation.mutate(resetTarget.id)}
                >
                  {resetMutation.isPending ? "Resetting..." : "Reset Password"}
                </Button>
              </>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// ── Main page ─────────────────────────────────────────────────
export function Settings() {
  const { user } = useAuth();
  const isOwner = user?.role === "owner";
  const SECTIONS = ALL_SECTIONS.filter((s) => s.id !== "team" || isOwner);

  const [activeSection, setActiveSection] = useState<SectionId>("workspace");
  const sectionRefs = useRef<Partial<Record<SectionId, HTMLDivElement | null>>>(
    {}
  );
  const scrollRef = useRef<HTMLDivElement | null>(null);

  // Intentionally fail-soft: skillsTotal is decorative; 0 is acceptable if this call fails.
  const { data: listData } = useQuery({
    queryKey: ["skills", "list"],
    queryFn: async () => {
      const res = await listSkills();
      if (res.error) throw res.error;
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
              sectionRefs.current.password = el;
            }}
          >
            <ChangePasswordSection />
          </div>

          <div
            ref={(el) => {
              sectionRefs.current.api = el;
            }}
          >
            <ApiSection />
          </div>

          {isOwner && (
            <div
              ref={(el) => {
                sectionRefs.current.team = el;
              }}
            >
              <TeamSection />
            </div>
          )}

          <div
            ref={(el) => {
              sectionRefs.current.sync = el;
            }}
          >
            <SyncTargetsSection />
          </div>

          {/* Bottom spacer */}
          <div className="h-10" />
        </div>
      </div>
    </div>
  );
}
