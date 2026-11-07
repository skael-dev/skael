import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Check, Plus, Server, Trash2 } from "lucide-react";
import { toast } from "sonner";
import {
  applyProviderPreset,
  createProvider,
  createWorkerKey,
  deleteProvider,
  listProviders,
  listWorkers,
  setEvalPanel,
  updateProvider,
} from "@/api/sdk.gen";
import type { Preset, Provider, ProvidersBody, Slot, Worker, WorkersKeyReply } from "@/api/types.gen";
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
import { throwOnApiError } from "@/lib/api-error";
import { relativeTime } from "@/lib/relative-time";
import { cn } from "@/lib/utils";

const PROVIDERS_KEY = ["providers"];
const WORKERS_KEY = ["workers"];

const fieldClass =
  "w-full h-9 px-3 bg-bg-primary border border-border rounded-[5px] text-sm text-text-primary placeholder:text-text-tertiary outline-none focus:border-border-active transition-colors";
const labelClass = "text-[11px] font-medium text-text-secondary uppercase tracking-wider";

const SUBSCRIPTION_WARNING =
  "Anthropic's terms limit a Claude subscription to ordinary individual use, and headless Claude Code draws from a separate usage pool. A team worker on one person's subscription is outside those terms. Use a subscription panel only on your own worker.";

const ADAPTER_LABEL: Record<Provider["adapter"], string> = {
  anthropic: "Anthropic API",
  gateway: "Compatible gateway",
  subscription: "Claude subscription",
};

// Common model ids per adapter, for a provider no worker has listed yet.
const KNOWN_MODELS: Record<Provider["adapter"], string[]> = {
  anthropic: ["claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5", "opus", "sonnet", "haiku"],
  gateway: ["anthropic/claude-opus-5", "anthropic/claude-sonnet-5", "anthropic/claude-haiku-4.5"],
  subscription: ["opus", "sonnet", "haiku"],
};

function Section({ title, desc, action, children }: { title: string; desc: string; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-end justify-between gap-3">
        <div>
          <h2 className="text-base font-medium text-text-primary m-0 tracking-tight">{title}</h2>
          <p className="text-xs text-text-tertiary m-0 mt-1 max-w-[640px]">{desc}</p>
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

function PresetDialog({ preset, onClose }: { preset: Preset | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [baseURL, setBaseURL] = useState("");
  useEffect(() => setBaseURL(preset?.default_base_url ?? ""), [preset]);
  const apply = useMutation({
    mutationFn: async () => {
      const res = await applyProviderPreset({ path: { id: preset!.id }, body: preset!.needs_base_url ? { base_url: baseURL } : {} });
      throwOnApiError(`set up ${preset!.title}`, res);
      return res.data!;
    },
    onSuccess: (data) => {
      qc.setQueryData(PROVIDERS_KEY, data);
      qc.invalidateQueries({ queryKey: WORKERS_KEY });
      toast.success(`${preset!.title} is set up. Workers pick it up on their next job.`);
      onClose();
    },
    onError: (err) => toast.error(err.message),
  });
  return (
    <Dialog open={!!preset} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{preset?.title}</DialogTitle>
          <DialogDescription>{preset?.description}</DialogDescription>
        </DialogHeader>
        {preset?.needs_base_url && (
          <label className="flex flex-col gap-1.5">
            <span className={labelClass}>Gateway base URL</span>
            <input className={cn(fieldClass, "font-mono")} value={baseURL} onChange={(e) => setBaseURL(e.target.value)} placeholder="https://" />
            <span className="text-[11px] text-text-tertiary">Without /v1. skael posts to the base URL plus /v1/messages.</span>
          </label>
        )}
        <div className="flex flex-col gap-1.5">
          <span className={labelClass}>Each worker needs</span>
          {(preset?.worker_env ?? []).map((n) => (
            <code key={n} className="font-mono text-xs text-text-primary">
              {n}
            </code>
          ))}
        </div>
        {preset?.subscription && (
          <p className="text-xs text-warning flex gap-2 m-0">
            <AlertTriangle size={14} className="shrink-0 mt-0.5" />
            {SUBSCRIPTION_WARNING}
          </p>
        )}
        <p className="text-xs text-text-tertiary m-0">
          This replaces the judge and the panel. A changed panel starts a new score trend for every skill.
        </p>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={apply.isPending} onClick={() => apply.mutate()}>
            {apply.isPending ? "Setting up…" : "Set up"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Presets({ presets, compact }: { presets: Preset[]; compact: boolean }) {
  const [chosen, setChosen] = useState<Preset | null>(null);
  return (
    <>
      <div className={cn("grid gap-3", compact ? "grid-cols-1 sm:grid-cols-2 lg:grid-cols-3" : "grid-cols-1 sm:grid-cols-2")}>
        {presets.map((p) => (
          <button
            key={p.id}
            type="button"
            onClick={() => setChosen(p)}
            className="text-left bg-bg-secondary border border-border rounded-lg p-4 flex flex-col gap-1.5 hover:border-border-active transition-colors"
          >
            <span className="text-sm text-text-primary">{p.title}</span>
            <span className="text-xs text-text-tertiary leading-relaxed">{p.description}</span>
            <span className="text-[11px] font-mono text-text-secondary mt-1">{(p.worker_env ?? []).join(" + ")}</span>
          </button>
        ))}
      </div>
      <PresetDialog preset={chosen} onClose={() => setChosen(null)} />
    </>
  );
}

function ModelInput({ value, onChange, options, label }: { value: string; onChange: (v: string) => void; options: string[]; label: string }) {
  const id = useMemo(() => `models-${Math.random().toString(36).slice(2)}`, []);
  return (
    <>
      <input className={cn(fieldClass, "font-mono")} aria-label={label} list={id} value={value} onChange={(e) => onChange(e.target.value)} />
      <datalist id={id}>
        {options.map((m) => (
          <option key={m} value={m} />
        ))}
      </datalist>
    </>
  );
}

function SlotRow({
  label,
  slot,
  providers,
  models,
  onChange,
  onRemove,
  judge,
}: {
  label: string;
  slot: Slot;
  providers: Provider[];
  models: Record<string, string[]>;
  onChange: (s: Slot) => void;
  onRemove?: () => void;
  judge?: boolean;
}) {
  const provider = providers.find((p) => p.id === slot.provider_id);
  const options = provider ? (models[provider.name]?.length ? models[provider.name]! : KNOWN_MODELS[provider.adapter]) : [];
  return (
    <div className="grid grid-cols-1 sm:grid-cols-[140px_minmax(0,1fr)_minmax(0,1.3fr)_32px] gap-2 items-center px-4 py-3 border-t border-border first:border-t-0">
      <span className="text-sm text-text-primary">{label}</span>
      <select
        className={fieldClass}
        aria-label={`${label} provider`}
        value={slot.provider_id}
        onChange={(e) => onChange({ ...slot, provider_id: e.target.value })}
      >
        <option value="">Choose a provider</option>
        {providers
          .filter((p) => !judge || p.adapter !== "subscription")
          .map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
      </select>
      <ModelInput label={`${label} model`} value={slot.model} options={options} onChange={(model) => onChange({ ...slot, model })} />
      {onRemove ? (
        <button type="button" aria-label={`Remove ${label}`} onClick={onRemove} className="text-text-tertiary hover:text-text-primary justify-self-center">
          <Trash2 size={14} />
        </button>
      ) : (
        <span />
      )}
    </div>
  );
}

function PanelEditor({ body, models }: { body: ProvidersBody; models: Record<string, string[]> }) {
  const qc = useQueryClient();
  const providers = body.providers ?? [];
  const initial = useMemo(
    () => ({ judge: body.panel?.judge ?? { provider_id: "", model: "" }, members: body.panel?.members ?? [] }),
    [body.panel],
  );
  const [judge, setJudge] = useState<Slot>(initial.judge);
  const [members, setMembers] = useState<Slot[]>(initial.members);
  useEffect(() => {
    setJudge(initial.judge);
    setMembers(initial.members);
  }, [initial]);
  const dirty = JSON.stringify({ judge, members }) !== JSON.stringify(initial);
  const subscription = members.some((m) => providers.find((p) => p.id === m.provider_id)?.adapter === "subscription");

  const save = useMutation({
    mutationFn: async () => {
      const res = await setEvalPanel({ body: { judge, members } });
      throwOnApiError("save the panel", res);
      return res.data!;
    },
    onSuccess: (data) => {
      qc.setQueryData(PROVIDERS_KEY, data);
      qc.invalidateQueries({ queryKey: WORKERS_KEY });
      toast.success("Panel saved. Workers use it from their next job.");
    },
    onError: (err) => toast.error(err.message),
  });

  return (
    <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
      <SlotRow label="Judge" judge slot={judge} providers={providers} models={models} onChange={setJudge} />
      {members.map((m, i) => (
        <SlotRow
          key={i}
          label={i === 0 ? "Primary member" : `Floor member ${i}`}
          slot={m}
          providers={providers}
          models={models}
          onChange={(s) => setMembers(members.map((x, j) => (j === i ? s : x)))}
          onRemove={members.length > 1 ? () => setMembers(members.filter((_, j) => j !== i)) : undefined}
        />
      ))}
      <div className="flex flex-wrap items-center justify-between gap-2 px-4 py-3 border-t border-border">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={members.length >= 4}
          onClick={() => setMembers([...members, { provider_id: members[0]?.provider_id ?? "", model: "" }])}
        >
          <Plus size={14} /> Add a floor member
        </Button>
        {dirty && (
          <div className="flex gap-2">
            <Button type="button" variant="ghost" size="sm" onClick={() => (setJudge(initial.judge), setMembers(initial.members))}>
              Discard
            </Button>
            <Button type="button" size="sm" disabled={save.isPending} onClick={() => save.mutate()}>
              Save panel
            </Button>
          </div>
        )}
      </div>
      {subscription && (
        <p className="text-xs text-warning flex gap-2 m-0 px-4 pb-3">
          <AlertTriangle size={14} className="shrink-0 mt-0.5" />
          {SUBSCRIPTION_WARNING}
        </p>
      )}
      <p className="text-[11px] text-text-tertiary m-0 px-4 pb-3">
        The judge grades every run. The primary member's score is the lift; floor members run only at the deep tier. A change of provider or model starts a new score trend.
      </p>
    </div>
  );
}

type ProviderDraft = { id?: string; name: string; adapter: Provider["adapter"]; base_url: string; auth_style: Provider["auth_style"]; credential_env: string };

function ProviderDialog({ draft, onClose }: { draft: ProviderDraft | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [d, setD] = useState<ProviderDraft | null>(draft);
  useEffect(() => setD(draft), [draft]);
  const save = useMutation({
    mutationFn: async () => {
      const body = { name: d!.name, adapter: d!.adapter, base_url: d!.base_url, auth_style: d!.auth_style, credential_env: d!.credential_env };
      const res = d!.id ? await updateProvider({ path: { id: d!.id }, body }) : await createProvider({ body });
      throwOnApiError("save the provider", res);
      return res.data!;
    },
    onSuccess: (data) => {
      qc.setQueryData(PROVIDERS_KEY, data);
      qc.invalidateQueries({ queryKey: WORKERS_KEY });
      onClose();
    },
    onError: (err) => toast.error(err.message),
  });
  if (!d) return null;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{d.id ? `Edit ${d.name}` : "Add a provider"}</DialogTitle>
          <DialogDescription>A provider names a backend and the worker variable that holds its key. The key stays on each worker.</DialogDescription>
        </DialogHeader>
        <label className="flex flex-col gap-1.5">
          <span className={labelClass}>Name</span>
          <input className={fieldClass} value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} />
        </label>
        <label className="flex flex-col gap-1.5">
          <span className={labelClass}>Type</span>
          <select className={fieldClass} value={d.adapter} onChange={(e) => setD({ ...d, adapter: e.target.value as Provider["adapter"] })}>
            {(Object.keys(ADAPTER_LABEL) as Provider["adapter"][]).map((a) => (
              <option key={a} value={a}>
                {ADAPTER_LABEL[a]}
              </option>
            ))}
          </select>
        </label>
        {d.adapter === "gateway" && (
          <>
            <label className="flex flex-col gap-1.5">
              <span className={labelClass}>Base URL</span>
              <input className={cn(fieldClass, "font-mono")} placeholder="https://openrouter.ai/api" value={d.base_url} onChange={(e) => setD({ ...d, base_url: e.target.value })} />
            </label>
            <label className="flex flex-col gap-1.5">
              <span className={labelClass}>Key header</span>
              <select className={fieldClass} value={d.auth_style} onChange={(e) => setD({ ...d, auth_style: e.target.value as Provider["auth_style"] })}>
                <option value="bearer">Authorization: Bearer (OpenRouter, LiteLLM, most gateways)</option>
                <option value="x-api-key">x-api-key (Anthropic-style proxies)</option>
              </select>
            </label>
          </>
        )}
        <label className="flex flex-col gap-1.5">
          <span className={labelClass}>Worker variable with the key</span>
          <input className={cn(fieldClass, "font-mono")} placeholder="OPENROUTER_API_KEY" value={d.credential_env} onChange={(e) => setD({ ...d, credential_env: e.target.value.toUpperCase() })} />
        </label>
        {d.adapter === "subscription" && (
          <p className="text-xs text-warning flex gap-2 m-0">
            <AlertTriangle size={14} className="shrink-0 mt-0.5" />A subscription serves panel members only. {SUBSCRIPTION_WARNING}
          </p>
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={save.isPending} onClick={() => save.mutate()}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ProvidersTable({ providers }: { providers: Provider[] }) {
  const qc = useQueryClient();
  const [draft, setDraft] = useState<ProviderDraft | null>(null);
  const remove = useMutation({
    mutationFn: async (id: string) => {
      const res = await deleteProvider({ path: { id } });
      throwOnApiError("remove the provider", res);
      return res.data!;
    },
    onSuccess: (data) => qc.setQueryData(PROVIDERS_KEY, data),
    onError: (err) => toast.error(err.message),
  });
  return (
    <>
      <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
        {providers.map((p) => (
          <div key={p.id} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 border-t border-border first:border-t-0">
            <span className="text-sm text-text-primary min-w-[140px]">{p.name}</span>
            <span className="text-xs text-text-secondary">{ADAPTER_LABEL[p.adapter]}</span>
            {p.base_url && <span className="text-xs font-mono text-text-tertiary">{p.base_url}</span>}
            <span className="text-xs font-mono text-text-secondary ml-auto">{p.credential_env}</span>
            <Button type="button" variant="ghost" size="sm" onClick={() => setDraft({ ...p })}>
              Edit
            </Button>
            <button type="button" aria-label={`Remove ${p.name}`} onClick={() => remove.mutate(p.id)} className="text-text-tertiary hover:text-text-primary">
              <Trash2 size={14} />
            </button>
          </div>
        ))}
        <div className="px-4 py-2 border-t border-border first:border-t-0">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setDraft({ name: "", adapter: "gateway", base_url: "", auth_style: "bearer", credential_env: "" })}
          >
            <Plus size={14} /> Add a provider
          </Button>
        </div>
      </div>
      <ProviderDialog draft={draft} onClose={() => setDraft(null)} />
    </>
  );
}

function snippets(reply: WorkersKeyReply, workerID: string) {
  const env = (reply.env ?? []).map((e) => ({ ...e, value: e.name === "SKAEL_ENDPOINT" && !e.value ? window.location.origin : e.value }));
  const val = (e: { name: string; value: string }) => e.value || `<your ${e.name}>`;
  const compose = [
    "services:",
    "  skael-worker:",
    "    image: ghcr.io/skael-dev/skael-worker:latest",
    "    restart: unless-stopped",
    "    environment:",
    `      WORKER_ID: ${workerID}`,
    ...env.map((e) => `      ${e.name}: ${val(e)}`),
    "      WORKER_RUN_ROOT: /var/lib/skael/run",
    "      WORKER_WORK_ROOT: /var/lib/skael/work",
    "    volumes:",
    "      - /var/run/docker.sock:/var/run/docker.sock",
    "      - /var/lib/skael/run:/var/lib/skael/run",
    "      - /var/lib/skael/work:/var/lib/skael/work",
  ].join("\n");
  const run = [
    "docker run -d --name skael-worker --restart unless-stopped \\",
    `  -e WORKER_ID=${workerID} \\`,
    ...env.map((e) => `  -e ${e.name}=${val(e)} \\`),
    "  -e WORKER_RUN_ROOT=/var/lib/skael/run -e WORKER_WORK_ROOT=/var/lib/skael/work \\",
    "  -v /var/run/docker.sock:/var/run/docker.sock \\",
    "  -v /var/lib/skael/run:/var/lib/skael/run -v /var/lib/skael/work:/var/lib/skael/work \\",
    "  ghcr.io/skael-dev/skael-worker:latest",
  ].join("\n");
  const helm = [
    "# helm upgrade --install skael deploy/helm/skael -f values.yaml",
    "worker:",
    "  enabled: true",
    `  endpoint: ${val(env.find((e) => e.name === "SKAEL_ENDPOINT")!)}`,
    "  # kubectl create secret generic skael-worker \\",
    ...env.filter((e) => e.secret).map((e) => `  #   --from-literal=${e.name}=${val(e)} \\`),
    "  existingSecret: skael-worker",
  ].join("\n");
  return { compose, run, helm };
}

function AddWorkerDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [name, setName] = useState("worker-1");
  const [reply, setReply] = useState<WorkersKeyReply | null>(null);
  const [tab, setTab] = useState<"compose" | "run" | "helm">("compose");
  const create = useMutation({
    mutationFn: async () => {
      const res = await createWorkerKey({ body: { name } });
      throwOnApiError("create the worker key", res);
      return res.data!;
    },
    onSuccess: setReply,
    onError: (err) => toast.error(err.message),
  });
  const close = () => {
    setReply(null);
    onClose();
  };
  const s = reply ? snippets(reply, name) : null;
  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="sm:max-w-[680px]">
        <DialogHeader>
          <DialogTitle>Add a worker</DialogTitle>
          <DialogDescription>
            The key can only claim and run eval jobs. Fill in each provider key on the worker's host; the server never sees them.
          </DialogDescription>
        </DialogHeader>
        {!reply ? (
          <label className="flex flex-col gap-1.5">
            <span className={labelClass}>Worker name</span>
            <input className={fieldClass} value={name} onChange={(e) => setName(e.target.value)} />
          </label>
        ) : (
          <div className="flex flex-col gap-3 min-w-0">
            <p className="text-xs text-warning m-0">The key is shown once. Copy the snippet now.</p>
            <div className="flex text-xs border border-border rounded overflow-hidden self-start" role="tablist">
              {(["compose", "run", "helm"] as const).map((t) => (
                <button
                  key={t}
                  type="button"
                  role="tab"
                  aria-selected={tab === t}
                  onClick={() => setTab(t)}
                  className={cn("px-3 py-1", tab === t ? "bg-bg-tertiary text-text-primary" : "text-text-tertiary")}
                >
                  {t === "compose" ? "Compose" : t === "run" ? "docker run" : "Helm"}
                </button>
              ))}
            </div>
            <div className="relative bg-bg-primary border border-border rounded-[5px] p-3 overflow-x-auto">
              <pre className="font-mono text-[11px] text-text-primary m-0 whitespace-pre">{s![tab]}</pre>
            </div>
            <CopyButton text={s![tab]} what="the worker snippet" />
          </div>
        )}
        <DialogFooter>
          {!reply ? (
            <Button disabled={create.isPending || !name.trim()} onClick={() => create.mutate()}>
              Create key
            </Button>
          ) : (
            <Button onClick={close}>Done</Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function WorkerRow({ w }: { w: Worker }) {
  const problems = w.problems ?? [];
  const ready = problems.length === 0;
  return (
    <div className="flex flex-col gap-1.5 px-4 py-3 border-t border-border first:border-t-0">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <span className={cn("inline-flex items-center gap-1.5 text-xs", ready ? "text-success" : "text-warning")}>
          {ready ? <Check size={13} /> : <AlertTriangle size={13} />}
          {ready ? "ready" : "not ready"}
        </span>
        <span className="text-sm text-text-primary font-mono">{w.worker_id}</span>
        <span className="text-xs text-text-tertiary">{w.version}</span>
        <span className="text-xs text-text-secondary">{w.driver}</span>
        {w.pinned && <span className="text-[11px] px-1.5 py-0.5 rounded border border-border text-text-secondary">pinned by env</span>}
        <span className="text-xs text-text-tertiary ml-auto">seen {relativeTime(w.last_seen)}</span>
      </div>
      <div className="text-[11px] text-text-tertiary font-mono break-all">
        {w.sandbox_image} · keys: {(w.credentials ?? []).join(", ") || "none"}
      </div>
      {w.pinned && <div className="text-[11px] text-text-tertiary">{w.pinned}</div>}
      {problems.map((p) => (
        <div key={p} className="text-xs text-warning">
          {p}
        </div>
      ))}
      {(w.verifications ?? []).filter((v) => v.ok).map((v) => (
        <div key={v.provider} className="text-[11px] text-text-tertiary">
          {v.provider}: {v.detail}
        </div>
      ))}
    </div>
  );
}

export function Evaluation() {
  const { user } = useAuth();
  const allowed = isPrivileged(user);
  const [adding, setAdding] = useState(false);
  const [showPresets, setShowPresets] = useState(false);
  const providers = useQuery({
    queryKey: PROVIDERS_KEY,
    enabled: allowed,
    queryFn: async () => {
      const res = await listProviders();
      throwOnApiError("load the providers", res);
      return res.data!;
    },
  });
  const workers = useQuery({
    queryKey: WORKERS_KEY,
    enabled: allowed,
    refetchInterval: 15_000,
    queryFn: async () => {
      const res = await listWorkers();
      throwOnApiError("load the workers", res);
      return res.data!;
    },
  });

  if (!allowed) {
    return (
      <div className="px-5 md:px-10 py-8">
        <EmptyState message="Only an owner or an admin can set up evaluation." />
      </div>
    );
  }
  const body = providers.data;
  const configured = !!body?.panel;
  const models = (workers.data?.models ?? {}) as Record<string, string[]>;
  const list = workers.data?.workers ?? [];

  return (
    <div className="px-5 md:px-10 py-8 max-w-[1000px] w-full mx-auto flex flex-col gap-10">
      <div>
        <h1 className="text-2xl font-medium tracking-tight text-text-primary m-0 mb-1">Evaluation</h1>
        <p className="text-sm text-text-secondary m-0">
          The server chooses the judge and the panel. Workers hold the keys and run the sessions.
        </p>
      </div>
      {providers.isLoading && <RowsSkeleton rows={4} />}
      {body && (!configured || showPresets) && (
        <Section
          title={configured ? "Change the setup" : "Choose a setup"}
          desc="Each setup creates its providers, judge and panel in one step. You can change any part afterwards."
          action={
            configured && (
              <Button variant="ghost" size="sm" onClick={() => setShowPresets(false)}>
                Close
              </Button>
            )
          }
        >
          <Presets presets={body.presets ?? []} compact={configured} />
        </Section>
      )}
      {body && configured && (
        <Section
          title="Judge and panel"
          desc="Model names come from what your workers' providers list. A name a worker has not listed still works if the provider serves it."
          action={
            !showPresets && (
              <Button variant="ghost" size="sm" onClick={() => setShowPresets(true)}>
                Use a preset
              </Button>
            )
          }
        >
          <PanelEditor body={body} models={models} />
        </Section>
      )}
      {body && (
        <Section title="Providers" desc="A provider names a backend and the variable on each worker that holds its key.">
          <ProvidersTable providers={body.providers ?? []} />
        </Section>
      )}
      <Section
        title="Workers"
        desc="Each worker checks in while it runs. A worker shows what it lacks for the judge and panel above."
        action={
          <Button size="sm" onClick={() => setAdding(true)}>
            <Server size={14} /> Add a worker
          </Button>
        }
      >
        {workers.isLoading && <RowsSkeleton rows={2} />}
        {workers.data && list.length === 0 && (
          <div className="bg-bg-secondary border border-border rounded-lg">
            <EmptyState message="No worker has checked in in the last day." />
          </div>
        )}
        {list.length > 0 && (
          <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
            {list.map((w) => (
              <WorkerRow key={w.worker_id} w={w} />
            ))}
          </div>
        )}
      </Section>
      <AddWorkerDialog open={adding} onClose={() => setAdding(false)} />
    </div>
  );
}
