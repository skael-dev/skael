import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { AlertTriangle, ChevronDown, ChevronRight, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import {
  createServiceAccount,
  createServiceAccountKey,
  deleteServiceAccount,
  listServiceAccountKeys,
  listServiceAccounts,
  listSets,
  revokeServiceAccountKey,
} from "@/api/sdk.gen";
import type { ApiKeyInfo, ServiceAccount, ServiceAccountKeyCreated } from "@/api/types.gen";
import { isPrivileged, useAuth } from "@/app/auth-provider";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { RowsSkeleton } from "@/components/ui/rows-skeleton";
import { useListNavigation } from "@/features/keyboard/use-list-navigation";
import { apiErrorMessage, throwOnApiError } from "@/lib/api-error";
import { relativeTime } from "@/lib/relative-time";

const SCOPES = [
  { id: "skills:read", label: "Read skills and download their archives" },
  { id: "sets:read", label: "Resolve sets and download bundles" },
  { id: "eval:work", label: "Claim evaluation jobs and post reports" },
];

const NAME_RE = /^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$/;

const fieldClass =
  "w-full h-9 px-3 bg-bg-primary border border-border rounded-[5px] text-sm text-text-primary placeholder:text-text-tertiary outline-none focus:border-border-active transition-colors";
const labelClass = "text-[11px] font-medium text-text-secondary uppercase tracking-wider";

const ACCOUNTS_KEY = ["service-accounts"];
const keysKey = (id: string) => ["service-accounts", id, "keys"];

function NewAccountForm({
  onCreated,
  onCancel,
}: {
  onCreated: (account: ServiceAccount) => void;
  onCancel: () => void;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [fieldError, setFieldError] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: async () => {
      const res = await createServiceAccount({ body: { name } });
      throwOnApiError("create the service account", res);
      return res.data!;
    },
    onSuccess: (account) => {
      toast.success(`Created service account ${account.name}. Create a key for it next.`);
      queryClient.invalidateQueries({ queryKey: ACCOUNTS_KEY });
      onCreated(account);
    },
    onError: (err) => toast.error(err.message),
  });

  return (
    <form
      className="flex flex-col gap-3 bg-bg-secondary border border-border rounded-lg p-3.5"
      onSubmit={(e) => {
        e.preventDefault();
        if (!NAME_RE.test(name)) {
          setFieldError(
            "A name uses lowercase letters, digits and hyphens, and starts and ends with a letter or digit. Correct the name.",
          );
          return;
        }
        setFieldError(null);
        create.mutate();
      }}
    >
      <div className="flex flex-col gap-1.5">
        <label htmlFor="service-account-name" className={labelClass}>
          Name
        </label>
        <input
          id="service-account-name"
          autoFocus
          value={name}
          maxLength={64}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. factoree"
          className={`${fieldClass} font-mono`}
        />
        <p className="text-[11px] text-text-tertiary m-0">
          This name is the consumer. It appears on every set this account resolves.
        </p>
      </div>
      {fieldError && <p className="text-xs text-danger m-0">{fieldError}</p>}
      {create.isError && <p className="text-xs text-danger m-0">{create.error.message}</p>}
      <div className="flex justify-end gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="border-border text-text-secondary"
          onClick={onCancel}
        >
          Cancel
        </Button>
        <Button
          type="submit"
          size="sm"
          className="bg-accent text-bg-primary hover:bg-accent/90"
          disabled={create.isPending || !name}
        >
          {create.isPending ? "Creating..." : "Create service account"}
        </Button>
      </div>
    </form>
  );
}

function CreatedKey({
  created,
  onDone,
}: {
  created: ServiceAccountKeyCreated;
  onDone: () => void;
}) {
  return (
    <div className="flex flex-col gap-2.5 p-3 bg-bg-tertiary border border-border-active rounded-lg">
      <div className="flex items-start gap-2 text-[11px] text-warning leading-relaxed">
        <AlertTriangle className="size-3.5 shrink-0 mt-0.5" />
        Copy this key now. It is not shown again.
      </div>
      <code className="font-mono text-[12px] text-text-primary break-all leading-relaxed select-all">
        {created.key}
      </code>
      <div className="flex flex-wrap justify-end gap-2">
        <CopyButton text={created.key} what="key" />
        <Button
          size="sm"
          className="bg-accent text-bg-primary hover:bg-accent/90"
          onClick={onDone}
        >
          Done
        </Button>
      </div>
    </div>
  );
}

function NewKeyForm({
  account,
  sets,
  onCreated,
}: {
  account: ServiceAccount;
  sets: { id: string; name: string }[];
  onCreated: (created: ServiceAccountKeyCreated) => void;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>([]);
  const [setId, setSetId] = useState("");

  const create = useMutation({
    mutationFn: async () => {
      const res = await createServiceAccountKey({
        path: { id: account.id },
        body: { name, scopes, set_id: setId || undefined },
      });
      throwOnApiError("create the key", res);
      return res.data!;
    },
    onSuccess: (created) => {
      toast.success(`Created key ${created.name}`);
      queryClient.invalidateQueries({ queryKey: ACCOUNTS_KEY });
      setName("");
      setScopes([]);
      setSetId("");
      onCreated(created);
    },
    onError: (err) => toast.error(err.message),
  });

  const toggleScope = (scope: string, on: boolean) =>
    setScopes((list) => (on ? [...list, scope] : list.filter((s) => s !== scope)));

  // A set-limited key must hold sets:read, so the scope follows the choice and
  // cannot be removed while the limit stands.
  const chooseSet = (id: string) => {
    setSetId(id);
    if (id && !scopes.includes("sets:read")) setScopes((list) => [...list, "sets:read"]);
  };

  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <div className="flex flex-col gap-1.5">
        <label htmlFor={`key-name-${account.id}`} className={labelClass}>
          New key name
        </label>
        <input
          id={`key-name-${account.id}`}
          value={name}
          maxLength={64}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. production"
          className={fieldClass}
        />
      </div>

      <fieldset className="flex flex-col gap-1.5 border-0 p-0 m-0">
        <legend className={`${labelClass} mb-1.5`}>Scopes</legend>
        {SCOPES.map((scope) => {
          const locked = scope.id === "sets:read" && setId !== "";
          return (
            <label key={scope.id} className="flex items-start gap-2 text-xs text-text-secondary">
              <input
                type="checkbox"
                className="mt-0.5 accent-[var(--color-accent)]"
                checked={scopes.includes(scope.id)}
                disabled={locked}
                onChange={(e) => toggleScope(scope.id, e.target.checked)}
              />
              <span className="min-w-0">
                <span className="font-mono text-text-primary">{scope.id}</span> {scope.label}
              </span>
            </label>
          );
        })}
      </fieldset>

      <div className="flex flex-col gap-1.5">
        <label htmlFor={`key-set-${account.id}`} className={labelClass}>
          Limit to one set (optional)
        </label>
        <select
          id={`key-set-${account.id}`}
          value={setId}
          onChange={(e) => chooseSet(e.target.value)}
          className={`${fieldClass} font-mono cursor-pointer`}
        >
          <option value="">No limit</option>
          {sets.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        {setId !== "" && (
          <p className="text-[11px] text-text-tertiary m-0">
            This key can read only this set. It gets 403 on skills routes and on every other set.
          </p>
        )}
      </div>

      {create.isError && <p className="text-xs text-danger m-0">{create.error.message}</p>}
      {!create.isError && name && scopes.length === 0 && (
        <p className="text-[11px] text-text-tertiary m-0">Select at least one scope.</p>
      )}

      <div className="flex justify-end">
        <Button
          type="submit"
          variant="outline"
          size="sm"
          className="border-border text-text-secondary"
          disabled={create.isPending || !name.trim() || scopes.length === 0}
        >
          <Plus className="size-3" />
          {create.isPending ? "Creating..." : "Create key"}
        </Button>
      </div>
    </form>
  );
}

function KeyPanel({
  account,
  sets,
}: {
  account: ServiceAccount;
  sets: { id: string; name: string }[];
}) {
  const queryClient = useQueryClient();
  const [created, setCreated] = useState<ServiceAccountKeyCreated | null>(null);
  const [revoking, setRevoking] = useState<ApiKeyInfo | null>(null);
  const keyRow = useListNavigation<ApiKeyInfo>(setRevoking);

  const keys = useQuery({
    queryKey: keysKey(account.id),
    queryFn: async () => {
      const res = await listServiceAccountKeys({ path: { id: account.id } });
      if (res.error !== undefined) throw new Error(apiErrorMessage("load the keys", res));
      return res.data?.keys ?? [];
    },
  });

  const revoke = useMutation({
    mutationFn: async (keyId: string) => {
      throwOnApiError(
        "revoke the key",
        await revokeServiceAccountKey({ path: { id: account.id, keyId } }),
      );
    },
    onSuccess: () => {
      toast.success(`Revoked key ${revoking?.name}`);
      setRevoking(null);
      queryClient.invalidateQueries({ queryKey: ACCOUNTS_KEY });
    },
    onError: (err) => toast.error(err.message),
  });

  const setName = (id: string | undefined) =>
    id ? (sets.find((s) => s.id === id)?.name ?? "deleted set") : null;

  return (
    <div className="flex flex-col gap-4 px-3.5 py-3.5 bg-bg-primary border-b border-border last:border-b-0">
      {created && <CreatedKey created={created} onDone={() => setCreated(null)} />}

      <div className="border border-border rounded-lg overflow-hidden">
        {keys.isLoading ? (
          <RowsSkeleton rows={2} />
        ) : keys.isError ? (
          <EmptyState
            message={keys.error.message}
            action={
              <button
                onClick={() => keys.refetch()}
                className="text-[13px] text-accent hover:underline cursor-pointer"
              >
                Retry
              </button>
            }
          />
        ) : (keys.data ?? []).length === 0 ? (
          <EmptyState message="No keys yet. Create one below so this account can authenticate." />
        ) : (
          (keys.data ?? []).map((key) => {
            const limit = setName(key.set_id);
            return (
              <div
                key={key.id}
                {...keyRow(key)}
                className="flex items-center gap-3 px-3 py-2.5 border-b border-border last:border-b-0"
              >
                <div className="flex-1 min-w-0">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="text-[13px] text-text-primary font-medium truncate">
                      {key.name}
                    </span>
                    <code className="text-[11px] font-mono text-text-tertiary bg-bg-tertiary px-1.5 py-0.5 rounded border border-border">
                      {key.prefix}...
                    </code>
                  </div>
                  <div className="flex flex-wrap gap-1.5 mt-1.5">
                    {(key.scopes ?? []).map((s) => (
                      <span
                        key={s}
                        className="text-[11px] font-mono text-text-secondary bg-bg-tertiary px-1.5 py-0.5 rounded"
                      >
                        {s}
                      </span>
                    ))}
                    {limit && (
                      <span className="text-[11px] font-mono text-accent bg-accent-surface px-1.5 py-0.5 rounded">
                        only {limit}
                      </span>
                    )}
                  </div>
                  <div className="text-[11px] text-text-tertiary mt-1">
                    Last used: {relativeTime(key.last_used_at)}
                  </div>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  className="border-border text-text-secondary hover:text-destructive"
                  onClick={() => setRevoking(key)}
                >
                  Revoke
                </Button>
              </div>
            );
          })
        )}
      </div>

      <NewKeyForm
        account={account}
        sets={sets}
        onCreated={(key) => {
          setCreated(key);
          queryClient.invalidateQueries({ queryKey: keysKey(account.id) });
        }}
      />

      <Dialog open={revoking !== null} onOpenChange={(open) => !open && setRevoking(null)}>
        <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
          <DialogHeader className="text-left">
            <DialogTitle className="text-text-primary">Revoke this key?</DialogTitle>
            <DialogDescription className="text-text-secondary">
              A machine that uses <span className="text-text-primary">{revoking?.name}</span> gets
              401 on its next call.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              className="border-border text-text-secondary"
              onClick={() => setRevoking(null)}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              disabled={revoke.isPending}
              onClick={() => revoking && revoke.mutate(revoking.id)}
            >
              {revoke.isPending ? "Revoking..." : "Revoke key"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

export function ServiceAccounts() {
  const { user } = useAuth();
  const allowed = isPrivileged(user);
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const creating = params.get("new") === "1";
  const [expanded, setExpanded] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<ServiceAccount | null>(null);

  const toggle = (id: string) => setExpanded((cur) => (cur === id ? null : id));
  const accountRow = useListNavigation<string>(toggle);

  const accounts = useQuery({
    queryKey: ACCOUNTS_KEY,
    enabled: allowed,
    queryFn: async () => {
      const res = await listServiceAccounts();
      if (res.error !== undefined) {
        throw new Error(apiErrorMessage("load service accounts", res));
      }
      return res.data?.service_accounts ?? [];
    },
  });

  // Set names label a key's limit. Without them the panel still works.
  const sets = useQuery({
    queryKey: ["sets"],
    enabled: allowed,
    queryFn: async () => {
      const res = await listSets();
      if (res.error !== undefined) throw new Error(apiErrorMessage("load sets", res));
      return res.data?.sets ?? [];
    },
  });

  const remove = useMutation({
    mutationFn: async (account: ServiceAccount) => {
      throwOnApiError(
        "delete the service account",
        await deleteServiceAccount({ path: { id: account.id } }),
      );
      return account;
    },
    onSuccess: (account) => {
      toast.success(`Deleted service account ${account.name} and revoked its keys`);
      setDeleting(null);
      queryClient.invalidateQueries({ queryKey: ACCOUNTS_KEY });
    },
    onError: (err) => toast.error(err.message),
  });

  if (!allowed) {
    return (
      <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto">
        <EmptyState
          message="Only an owner or an admin can manage service accounts."
          action={
            <Link to="/" className="text-[13px] text-accent hover:underline">
              Open skills
            </Link>
          }
        />
      </div>
    );
  }

  const list = accounts.data ?? [];

  return (
    <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-60">
          <h1 className="text-lg font-medium text-text-primary">Service accounts</h1>
          <p className="text-xs text-text-tertiary mt-1">
            A service account is a machine identity. It has no password and acts only through
            scoped keys.
          </p>
        </div>
        {!creating && (
          <Button
            variant="outline"
            size="sm"
            className="border-border text-text-secondary"
            onClick={() => setParams({ new: "1" })}
          >
            <Plus className="size-3" />
            New service account
          </Button>
        )}
      </div>

      {creating && (
        <NewAccountForm
          onCreated={(account) => {
            setParams({});
            setExpanded(account.id);
          }}
          onCancel={() => setParams({})}
        />
      )}

      <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
        {accounts.isLoading ? (
          <RowsSkeleton />
        ) : accounts.isError ? (
          <EmptyState
            message={accounts.error.message}
            action={
              <button
                onClick={() => accounts.refetch()}
                className="text-[13px] text-accent hover:underline cursor-pointer"
              >
                Retry
              </button>
            }
          />
        ) : list.length === 0 ? (
          <EmptyState
            message="No service accounts yet."
            action={
              !creating && (
                <button
                  onClick={() => setParams({ new: "1" })}
                  className="text-[13px] text-accent hover:underline cursor-pointer"
                >
                  Create the first one
                </button>
              )
            }
          />
        ) : (
          list.map((account) => {
            const open = expanded === account.id;
            const Chevron = open ? ChevronDown : ChevronRight;
            return (
              <div key={account.id}>
                <div className="flex items-center border-b border-border">
                  <button
                    type="button"
                    {...accountRow(account.id)}
                    tabIndex={0}
                    aria-expanded={open}
                    onClick={() => toggle(account.id)}
                    className="flex-1 min-w-0 flex flex-wrap items-center gap-x-4 gap-y-1 px-3.5 py-3 text-left cursor-pointer bg-transparent border-none hover:bg-bg-tertiary transition-colors"
                  >
                    <span className="flex items-center gap-2 flex-1 basis-full sm:basis-0 min-w-0">
                      <Chevron className="size-3.5 text-text-tertiary shrink-0" />
                      <span className="font-mono text-[13px] font-medium text-text-primary truncate">
                        {account.name}
                      </span>
                    </span>
                    <span className="font-mono text-[11px] text-text-secondary whitespace-nowrap">
                      {account.key_count} {account.key_count === 1 ? "key" : "keys"}
                    </span>
                    <span className="text-[11px] text-text-tertiary whitespace-nowrap">
                      Last used: {relativeTime(account.last_used_at)}
                    </span>
                  </button>
                  <button
                    type="button"
                    aria-label={`Delete ${account.name}`}
                    onClick={() => setDeleting(account)}
                    className="flex items-center justify-center size-8 mr-2 shrink-0 text-text-tertiary hover:text-destructive rounded-[5px] cursor-pointer bg-transparent border-none transition-colors"
                  >
                    <Trash2 className="size-3.5" />
                  </button>
                </div>
                {open && <KeyPanel account={account} sets={sets.data ?? []} />}
              </div>
            );
          })
        )}
      </div>

      <Dialog open={deleting !== null} onOpenChange={(open) => !open && setDeleting(null)}>
        <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
          <DialogHeader className="text-left">
            <DialogTitle className="text-text-primary">Delete this service account?</DialogTitle>
            <DialogDescription className="text-text-secondary">
              This revokes every key of{" "}
              <span className="font-mono text-text-primary">{deleting?.name}</span>. A machine that
              uses one gets 401 on its next call.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              className="border-border text-text-secondary"
              onClick={() => setDeleting(null)}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              disabled={remove.isPending}
              onClick={() => deleting && remove.mutate(deleting)}
            >
              {remove.isPending ? "Deleting..." : "Delete service account"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
