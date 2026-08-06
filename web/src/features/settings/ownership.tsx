import { useEffect, useState } from "react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Plus, ShieldOff, Trash2, X } from "lucide-react";
import {
  createOwnershipRule,
  deleteOwnershipRule,
  getSkillOwners,
  listOwnershipRules,
  listSkills,
  updateOwnershipRule,
} from "@/api/sdk.gen";
import type { PublicUser, RuleBody } from "@/api/types.gen";
import { useAuth } from "@/app/auth-provider";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { UserPicker } from "./user-picker";

// ── CanManage, mirrored from internal/ownership/manage.go ───────────────
//
// A pure client-side projection of the same three clauses the server
// enforces, so the disabled state on "Manage" always agrees with what the
// server would actually allow — never optimistic, never stale. The server
// remains the source of truth (a 403 here still shows the server's message);
// this only decides what's worth letting someone click.
function isPrefix(p: string): boolean {
  return p.endsWith("*");
}

function scope(p: string): string {
  return isPrefix(p) ? p.slice(0, -1) : p;
}

// strictlyContains reports whether every name in inner's scope also falls in
// outer's scope, and the two patterns differ — the delegation clause that
// lets a namespace owner reclaim a sub-pattern they carved out to someone
// else.
function strictlyContains(outer: string, inner: string): boolean {
  if (outer === inner) return false;
  if (!isPrefix(outer)) return false;
  const os = scope(outer);
  const is = scope(inner);
  if (is.length < os.length) return false;
  return is.slice(0, os.length) === os;
}

export type Actor = { userId: string; privileged: boolean };

export function canManage(actor: Actor | null, pattern: string, rules: RuleBody[]): boolean {
  if (!actor) return false;
  if (actor.privileged) return true;
  for (const r of rules) {
    const members = r.members ?? [];
    if (!members.includes(actor.userId)) continue;
    if (r.pattern === pattern) return true; // member of the pattern's own rule
    if (strictlyContains(r.pattern, pattern)) return true; // member of an enclosing rule
  }
  return false;
}

export const CANNOT_MANAGE_REASON =
  "Only an owner of this namespace, or an instance admin, can manage this rule.";

function actorFromUser(user: { id: string; role: string } | null): Actor | null {
  if (!user) return null;
  return { userId: user.id, privileged: user.role === "owner" || user.role === "admin" };
}

// ── Unowned backlog ───────────────────────────────────────────────────
//
// There is no server-side "unowned" filter (§9 exposes ownership per-skill,
// not as a list query), so this resolves it the same way any other reader
// would: list skills, then ask each one who owns it. Fine at registry scale;
// a dedicated count endpoint is the obvious follow-up if this page gets slow.
function useUnownedCount() {
  const skillsQuery = useQuery({
    queryKey: ["skills", "list", "ownership-backlog"],
    queryFn: async () => {
      const res = await listSkills({ query: { limit: 1000 } });
      if (res.error) throw res.error;
      return res.data?.skills ?? [];
    },
  });

  const names = (skillsQuery.data ?? []).map((s) => s.name);

  const ownerQueries = useQueries({
    queries: names.map((name) => ({
      queryKey: ["skill-owners", name],
      queryFn: async () => {
        const res = await getSkillOwners({ path: { name } });
        if (res.error) throw res.error;
        return res.data;
      },
      enabled: skillsQuery.isSuccess,
    })),
  });

  const settled = ownerQueries.length === 0 || ownerQueries.every((q) => q.isSuccess || q.isError);
  const loading = skillsQuery.isLoading || !settled;
  const unownedCount = ownerQueries.filter((q) => q.data?.unowned).length;

  return { unownedCount, loading };
}

// ── Rule row ──────────────────────────────────────────────────────────
function RuleRow({
  rule,
  canManageThis,
  onManage,
}: {
  rule: RuleBody;
  canManageThis: boolean;
  onManage: () => void;
}) {
  const members = rule.members ?? [];
  return (
    <div className="flex items-center gap-3 px-3.5 py-3 border-b border-border last:border-b-0">
      <div className="flex-1 min-w-0">
        <div className="text-[13px] font-mono text-text-primary">{rule.pattern}</div>
        <div className="flex flex-wrap gap-1.5 mt-1.5">
          {members.length === 0 ? (
            <span className="text-[11px] text-text-tertiary italic">No members</span>
          ) : (
            members.map((m) => (
              <span
                key={m}
                title={m}
                className="text-[11px] font-mono text-text-secondary bg-bg-tertiary px-1.5 py-0.5 rounded"
              >
                {m.slice(0, 8)}
              </span>
            ))
          )}
        </div>
      </div>
      <div className="flex flex-col items-end gap-1 shrink-0">
        <Button
          variant="outline"
          size="sm"
          disabled={!canManageThis}
          title={!canManageThis ? CANNOT_MANAGE_REASON : undefined}
          onClick={onManage}
        >
          Manage
        </Button>
        {!canManageThis && (
          <span className="text-[11px] text-text-tertiary max-w-[240px] text-right leading-snug">
            {CANNOT_MANAGE_REASON}
          </span>
        )}
      </div>
    </div>
  );
}

// ── Manage rule dialog (edit members / delete) ───────────────────────
function ManageRuleDialog({
  rule,
  open,
  onOpenChange,
}: {
  rule: RuleBody | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const qc = useQueryClient();
  const [members, setMembers] = useState<string[]>([]);
  const [labels, setLabels] = useState<Record<string, string>>({});

  useEffect(() => {
    setMembers(rule?.members ?? []);
    setLabels({});
  }, [rule]);

  const update = useMutation({
    mutationFn: async () => {
      if (!rule) return undefined;
      const res = await updateOwnershipRule({ path: { id: rule.id }, body: { members } });
      if (res.error) throw new Error(res.error.detail ?? "Failed to update rule");
      return res.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["ownership", "rules"] });
      onOpenChange(false);
    },
  });

  const del = useMutation({
    mutationFn: async () => {
      if (!rule) return undefined;
      const res = await deleteOwnershipRule({ path: { id: rule.id } });
      if (res.error) throw new Error(res.error.detail ?? "Failed to delete rule");
      return res.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["ownership", "rules"] });
      onOpenChange(false);
    },
  });

  const error = update.isError
    ? (update.error as Error).message
    : del.isError
      ? (del.error as Error).message
      : null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
        <DialogHeader>
          <DialogTitle className="text-text-primary font-mono">{rule?.pattern}</DialogTitle>
          <DialogDescription className="text-text-secondary">
            Add or remove members for this ownership rule.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          {members.length === 0 ? (
            <div className="text-xs text-text-tertiary italic">No members yet</div>
          ) : (
            members.map((m) => (
              <div
                key={m}
                className="flex items-center justify-between gap-2 text-xs bg-bg-tertiary border border-border rounded px-2.5 py-1.5"
              >
                <span className="font-mono text-text-primary truncate">{labels[m] ?? m}</span>
                <button
                  type="button"
                  aria-label={`Remove ${labels[m] ?? m}`}
                  onClick={() => setMembers((ms) => ms.filter((x) => x !== m))}
                  className="text-text-tertiary hover:text-destructive cursor-pointer bg-transparent border-none"
                >
                  <X className="size-3.5" />
                </button>
              </div>
            ))
          )}
          <UserPicker
            excludeIds={members}
            onSelect={(u: PublicUser) => {
              setMembers((ms) => [...ms, u.id]);
              setLabels((l) => ({ ...l, [u.id]: `${u.name} <${u.email}>` }));
            }}
          />
        </div>

        {error && <p className="text-xs text-danger">{error}</p>}

        <DialogFooter className="justify-between sm:justify-between">
          <Button
            variant="destructive"
            size="sm"
            onClick={() => del.mutate()}
            disabled={del.isPending || update.isPending}
          >
            <Trash2 className="size-3 mr-1.5" />
            Delete rule
          </Button>
          <Button
            size="sm"
            className="bg-accent text-bg-primary hover:bg-accent/90"
            onClick={() => update.mutate()}
            disabled={update.isPending || del.isPending || members.length === 0}
          >
            {update.isPending ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── Create rule dialog ────────────────────────────────────────────────
function CreateRuleDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const qc = useQueryClient();
  const [pattern, setPattern] = useState("");
  const [members, setMembers] = useState<string[]>([]);
  const [labels, setLabels] = useState<Record<string, string>>({});

  const reset = () => {
    setPattern("");
    setMembers([]);
    setLabels({});
  };

  const create = useMutation({
    mutationFn: async () => {
      const res = await createOwnershipRule({ body: { pattern, members } });
      if (res.error) throw new Error(res.error.detail ?? "Failed to create rule");
      return res.data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["ownership", "rules"] });
      reset();
      onOpenChange(false);
    },
  });

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) {
          reset();
          create.reset();
        }
        onOpenChange(o);
      }}
    >
      <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
        <DialogHeader>
          <DialogTitle className="text-text-primary">Add ownership rule</DialogTitle>
          <DialogDescription className="text-text-secondary">
            An exact skill name, or a namespace prefix ending in{" "}
            <code className="font-mono text-text-primary bg-bg-tertiary px-1 py-0.5 rounded text-[11px]">
              :*
            </code>
            .
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <input
            type="text"
            value={pattern}
            onChange={(e) => setPattern(e.target.value)}
            placeholder="e.g. payments:*"
            aria-label="Pattern"
            className="w-full h-9 px-3 bg-bg-primary border border-border rounded-[5px] text-sm font-mono text-text-primary placeholder:text-text-tertiary outline-none focus:border-border-active transition-colors"
          />

          {members.length > 0 && (
            <div className="flex flex-col gap-2">
              {members.map((m) => (
                <div
                  key={m}
                  className="flex items-center justify-between gap-2 text-xs bg-bg-tertiary border border-border rounded px-2.5 py-1.5"
                >
                  <span className="font-mono text-text-primary truncate">{labels[m] ?? m}</span>
                  <button
                    type="button"
                    aria-label={`Remove ${labels[m] ?? m}`}
                    onClick={() => setMembers((ms) => ms.filter((x) => x !== m))}
                    className="text-text-tertiary hover:text-destructive cursor-pointer bg-transparent border-none"
                  >
                    <X className="size-3.5" />
                  </button>
                </div>
              ))}
            </div>
          )}

          <UserPicker
            excludeIds={members}
            onSelect={(u: PublicUser) => {
              setMembers((ms) => [...ms, u.id]);
              setLabels((l) => ({ ...l, [u.id]: `${u.name} <${u.email}>` }));
            }}
          />
        </div>

        {create.isError && (
          <p className="text-xs text-danger">{(create.error as Error).message}</p>
        )}

        <DialogFooter>
          <Button
            size="sm"
            className="bg-accent text-bg-primary hover:bg-accent/90"
            disabled={!pattern.trim() || members.length === 0 || create.isPending}
            onClick={() => create.mutate()}
          >
            {create.isPending ? "Creating…" : "Create rule"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── Page ──────────────────────────────────────────────────────────────
export function Ownership() {
  const { user } = useAuth();
  const actor = actorFromUser(user);
  const [managingRule, setManagingRule] = useState<RuleBody | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const { unownedCount, loading: unownedLoading } = useUnownedCount();

  const rulesQuery = useQuery({
    queryKey: ["ownership", "rules"],
    queryFn: async () => {
      const res = await listOwnershipRules();
      if (res.error) throw res.error;
      return res.data?.rules ?? [];
    },
  });

  const rules = rulesQuery.data ?? [];

  return (
    <div className="p-6 max-w-4xl mx-auto flex flex-col gap-6">
      <div>
        <h1 className="text-lg font-medium text-text-primary">Ownership</h1>
        <p className="text-xs text-text-tertiary mt-1">
          Rules assign who reviews a publish to a skill name or namespace. A name with no
          matching rule is unowned — any member may publish over it.
        </p>
      </div>

      {/* Unowned backlog */}
      <div className="flex items-center justify-between gap-3 bg-bg-secondary border border-border rounded-lg px-4 py-3.5">
        <div className="flex items-center gap-3">
          <ShieldOff className="size-4 text-text-tertiary shrink-0" />
          <div>
            <div className="text-[13px] text-text-secondary">Unowned skills</div>
            <div className="text-xl font-semibold text-text-primary">
              {unownedLoading ? "…" : unownedCount}
            </div>
          </div>
        </div>
        <Link to="/?unowned=true" className="text-xs text-accent hover:underline shrink-0">
          View unowned skills
        </Link>
      </div>

      {/* Rules list */}
      <div>
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-sm font-medium text-text-primary">Rules</h2>
          <Button
            variant="outline"
            size="sm"
            className="border-border text-text-secondary"
            onClick={() => setCreateOpen(true)}
          >
            <Plus className="size-3 mr-1.5" />
            Add rule
          </Button>
        </div>
        <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
          {rulesQuery.isLoading ? (
            <div className="px-3.5 py-6 text-center text-xs text-text-tertiary">
              Loading rules…
            </div>
          ) : rules.length === 0 ? (
            <div className="px-3.5 py-6 text-center text-xs text-text-tertiary">
              No ownership rules yet — every skill is unowned.
            </div>
          ) : (
            rules.map((r) => (
              <RuleRow
                key={r.id}
                rule={r}
                canManageThis={canManage(actor, r.pattern, rules)}
                onManage={() => setManagingRule(r)}
              />
            ))
          )}
        </div>
      </div>

      <ManageRuleDialog
        rule={managingRule}
        open={managingRule != null}
        onOpenChange={(open) => {
          if (!open) setManagingRule(null);
        }}
      />
      <CreateRuleDialog open={createOpen} onOpenChange={setCreateOpen} />
    </div>
  );
}
