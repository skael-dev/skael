import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Check, KeyRound, Plus, Server, Trash2 } from "lucide-react";
import { NavLink } from "react-router-dom";
import { toast } from "sonner";
import { applyProviderPreset, createProvider, createWorkerKey, deleteProvider, listProviders, listWorkers, setEvalPanel, updateProvider } from "@/api/sdk.gen";
import type { Preset, Provider, ProvidersBody, Slot, Worker, WorkersKeyReply } from "@/api/types.gen";
import { isPrivileged, useAuth } from "@/app/auth-provider";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { RowsSkeleton } from "@/components/ui/rows-skeleton";
import { throwOnApiError } from "@/lib/api-error";
import { relativeTime } from "@/lib/relative-time";
import { cn } from "@/lib/utils";
import { ModelPicker } from "./model-picker";

const PROVIDERS_KEY = ["providers"];
const WORKERS_KEY = ["workers"];

const fieldClass =
  "w-full h-9 px-3 bg-bg-primary border border-border rounded-[5px] text-sm text-text-primary placeholder:text-text-tertiary outline-none focus:border-border-active transition-colors";
const labelClass = "text-[11px] font-medium text-text-secondary uppercase tracking-wider";

const TYPE_LABEL: Record<Provider["type"], string> = {
  anthropic: "Anthropic",
  openai: "OpenAI",
  openrouter: "OpenRouter",
  "openai-compatible": "OpenAI-compatible",
};

const BASE_URL_HINT: Record<Provider["type"], string> = {
  anthropic: "Leave empty for api.anthropic.com. For Claude in Azure Foundry: https://<resource>.services.ai.azure.com/anthropic",
  openai: "Leave empty for api.openai.com.",
  openrouter: "Leave empty for openrouter.ai.",
  "openai-compatible": "The URL /chat/completions is under, such as https://<resource>.openai.azure.com/openai/v1",
};

const KEY_HINT =
  "Paste the key, or enter env:NAME or file:/path to read it on the server. The server seals a pasted key and sends it to a worker only for a job.";

/** EvaluationTabs switches between the setup and the jobs. */
export function EvaluationTabs() {
  const tab = (to: string, label: string, end?: boolean) => (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        cn("px-3 py-1.5 text-sm rounded-md transition-colors", isActive ? "bg-bg-tertiary text-text-primary" : "text-text-tertiary hover:text-text-primary")
      }
    >
      {label}
    </NavLink>
  );
  return (
    <nav className="flex gap-1" aria-label="Evaluation">
      {tab("/settings/evaluation", "Setup", true)}
      {tab("/settings/evaluation/jobs", "Jobs")}
    </nav>
  );
}

function KeyField({ value, onChange, stored }: { value: string; onChange: (v: string) => void; stored?: string }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className={labelClass}>API key</span>
      <input
        className={cn(fieldClass, "font-mono")}
        type="password"
        autoComplete="off"
        placeholder={stored ? `${stored}; leave empty to keep it` : "sk-…, env:NAME or file:/path"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      <span className="text-[11px] text-text-tertiary">{KEY_HINT}</span>
    </label>
  );
}

function AuthHeaderField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className={labelClass}>Key header</span>
      <select
        className={fieldClass}
        value={value === "" ? "" : value === "api-key" ? "api-key" : "custom"}
        onChange={(e) => onChange(e.target.value === "custom" ? "x-api-key" : e.target.value)}
      >
        <option value="">Authorization: Bearer (OpenAI, Azure v1, LiteLLM, vLLM)</option>
        <option value="api-key">api-key (Azure's own header)</option>
        <option value="custom">Another header</option>
      </select>
      {value !== "" && value !== "api-key" && (
        <input className={cn(fieldClass, "font-mono")} aria-label="Header name" value={value} onChange={(e) => onChange(e.target.value)} />
      )}
    </label>
  );
}

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

function PresetDialog({ preset, hasPanel, onClose }: { preset: Preset | null; hasPanel: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const [baseURL, setBaseURL] = useState("");
  const [authHeader, setAuthHeader] = useState("");
  const [key, setKey] = useState("");
  useEffect(() => {
    setBaseURL(preset?.default_base_url ?? "");
    setAuthHeader("");
    setKey("");
  }, [preset]);
  const fills = !hasPanel && !!preset?.judge_model && (preset?.member_models?.length ?? 0) > 0;
  const apply = useMutation({
    mutationFn: async () => {
      const res = await applyProviderPreset({
        path: { id: preset!.id },
        body: {
          key,
          ...(preset!.needs_base_url ? { base_url: baseURL } : {}),
          ...(preset!.type === "openai-compatible" ? { auth_header: authHeader } : {}),
        },
      });
      throwOnApiError(`add ${preset!.title}`, res);
      return res.data!;
    },
    onSuccess: (data) => {
      qc.setQueryData(PROVIDERS_KEY, data);
      toast.success(fills ? `${preset!.title} is added, and the panel uses its default models.` : `${preset!.title} is added.`);
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
            <span className={labelClass}>Base URL</span>
            <input
              className={cn(fieldClass, "font-mono")}
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
              placeholder={preset.base_url_hint ?? "https://"}
            />
          </label>
        )}
        {preset?.type === "openai-compatible" && <AuthHeaderField value={authHeader} onChange={setAuthHeader} />}
        <KeyField value={key} onChange={setKey} />
        {preset?.model_hint && <p className="text-xs text-text-secondary m-0">{preset.model_hint}</p>}
        {fills && (
          <p className="text-xs text-text-tertiary m-0">
            The panel starts as judge <code className="font-mono">{preset!.judge_model}</code> and members{" "}
            <code className="font-mono">{(preset!.member_models ?? []).join(", ")}</code>. You can change it afterwards.
          </p>
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={apply.isPending || !key.trim()} onClick={() => apply.mutate()}>
            {apply.isPending ? "Adding…" : "Add"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Presets({ presets, compact, hasPanel }: { presets: Preset[]; compact: boolean; hasPanel: boolean }) {
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
          </button>
        ))}
      </div>
      <PresetDialog preset={chosen} hasPanel={hasPanel} onClose={() => setChosen(null)} />
    </>
  );
}

function SlotRow({
  label,
  slot,
  providers,
  onChange,
  onRemove,
}: {
  label: string;
  slot: Slot;
  providers: Provider[];
  onChange: (s: Slot) => void;
  onRemove?: () => void;
}) {
  const provider = providers.find((p) => p.id === slot.provider_id);
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
        {providers.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name}
          </option>
        ))}
      </select>
      <ModelPicker
        label={`${label} model`}
        providers={provider ? [provider] : []}
        providerId={provider?.id}
        value={slot.model}
        hint={provider?.type === "openai-compatible" ? "a deployment or model id" : undefined}
        onPick={(model) => onChange({ ...slot, model })}
      />
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

function PanelEditor({ body }: { body: ProvidersBody }) {
  const qc = useQueryClient();
  const providers = body.providers ?? [];
  const initial = useMemo(
    () => ({
      judge: body.panel?.judge ?? { provider_id: "", model: "" },
      members: body.panel?.members ?? [],
    }),
    [body.panel]
  );
  const [judge, setJudge] = useState<Slot>(initial.judge);
  const [members, setMembers] = useState<Slot[]>(initial.members);
  useEffect(() => {
    setJudge(initial.judge);
    setMembers(initial.members);
  }, [initial]);
  const dirty = JSON.stringify({ judge, members }) !== JSON.stringify(initial);

  const save = useMutation({
    mutationFn: async () => {
      const res = await setEvalPanel({ body: { judge, members } });
      throwOnApiError("save the panel", res);
      return res.data!;
    },
    onSuccess: (data) => {
      qc.setQueryData(PROVIDERS_KEY, data);
      toast.success("Panel saved. Workers use it from their next job.");
    },
    onError: (err) => toast.error(err.message),
  });

  return (
    <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
      <SlotRow label="Judge" slot={judge} providers={providers} onChange={setJudge} />
      {members.map((m, i) => (
        <SlotRow
          key={i}
          label={i === 0 ? "Primary member" : `Floor member ${i}`}
          slot={m}
          providers={providers}
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
      <p className="text-[11px] text-text-tertiary m-0 px-4 pb-3">
        The judge grades every run. Every member runs in OpenCode. The primary member's score is the lift; floor members run only at the deep tier. A change of
        provider or model starts a new score trend.
      </p>
    </div>
  );
}

type ProviderDraft = {
  id?: string;
  name: string;
  type: Provider["type"];
  base_url: string;
  auth_header: string;
  stored?: string;
};

function keyStatus(p: Provider) {
  if (p.key_unreadable) return { text: "key does not open; enter it again", warn: true };
  if (p.key_ref) return { text: p.key_ref, warn: false };
  if (p.has_key) return { text: "key stored", warn: false };
  return { text: "needs a key", warn: true };
}

function ProviderDialog({ draft, onClose }: { draft: ProviderDraft | null; onClose: () => void }) {
  const qc = useQueryClient();
  const [d, setD] = useState<ProviderDraft | null>(draft);
  const [key, setKey] = useState("");
  useEffect(() => {
    setD(draft);
    setKey("");
  }, [draft]);
  const save = useMutation({
    mutationFn: async () => {
      const body = {
        name: d!.name,
        type: d!.type,
        base_url: d!.base_url,
        auth_header: d!.type === "openai-compatible" ? d!.auth_header : "",
        ...(key ? { key } : {}),
      };
      const res = d!.id ? await updateProvider({ path: { id: d!.id }, body }) : await createProvider({ body });
      throwOnApiError("save the provider", res);
      return res.data!;
    },
    onSuccess: (data) => {
      qc.setQueryData(PROVIDERS_KEY, data);
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
          <DialogDescription>A provider is one LLM endpoint and its key. The judge and each panel member choose a provider and a model.</DialogDescription>
        </DialogHeader>
        <label className="flex flex-col gap-1.5">
          <span className={labelClass}>Name</span>
          <input className={fieldClass} value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} />
        </label>
        <label className="flex flex-col gap-1.5">
          <span className={labelClass}>Type</span>
          <select className={fieldClass} value={d.type} onChange={(e) => setD({ ...d, type: e.target.value as Provider["type"] })}>
            {(Object.keys(TYPE_LABEL) as Provider["type"][]).map((t) => (
              <option key={t} value={t}>
                {TYPE_LABEL[t]}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1.5">
          <span className={labelClass}>Base URL</span>
          <input className={cn(fieldClass, "font-mono")} placeholder="https://" value={d.base_url} onChange={(e) => setD({ ...d, base_url: e.target.value })} />
          <span className="text-[11px] text-text-tertiary">{BASE_URL_HINT[d.type]}</span>
        </label>
        {d.type === "openai-compatible" && <AuthHeaderField value={d.auth_header} onChange={(v) => setD({ ...d, auth_header: v })} />}
        <KeyField value={key} onChange={setKey} stored={d.stored} />
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={save.isPending || !d.name.trim() || (!d.id && !key.trim())} onClick={() => save.mutate()}>
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
            <span className="text-xs text-text-secondary">{TYPE_LABEL[p.type]}</span>
            {p.base_url && <span className="text-xs font-mono text-text-tertiary break-all">{p.base_url}</span>}
            <span className={cn("text-xs ml-auto inline-flex items-center gap-1.5", keyStatus(p).warn ? "text-warning" : "text-text-secondary")}>
              <KeyRound size={12} />
              {keyStatus(p).text}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() =>
                setDraft({
                  id: p.id,
                  name: p.name,
                  type: p.type,
                  base_url: p.base_url,
                  auth_header: p.auth_header,
                  stored: p.has_key ? p.key_ref || "a key is stored" : undefined,
                })
              }
            >
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
            onClick={() =>
              setDraft({
                name: "",
                type: "openai-compatible",
                base_url: "",
                auth_header: "",
              })
            }
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
  const env = (reply.env ?? []).map((e) => ({
    ...e,
    value: e.name === "SKAEL_ENDPOINT" && !e.value ? window.location.origin : e.value,
  }));
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
    `  # kubectl create secret generic skael-worker --from-literal=SKAEL_API_KEY=${val(env.find((e) => e.name === "SKAEL_API_KEY")!)}`,
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
            The key can only claim and run eval jobs. A worker needs nothing else: provider keys stay on the server and reach the worker with each job.
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
        {w.harness && <span className="text-xs text-text-tertiary">{w.harness}</span>}
        <span className="text-xs text-text-tertiary ml-auto">seen {relativeTime(w.last_seen)}</span>
      </div>
      <div className="text-[11px] text-text-tertiary font-mono break-all">{w.sandbox_image}</div>
      {problems.map((p) => (
        <div key={p} className="text-xs text-warning">
          {p}
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
  const list = workers.data?.workers ?? [];
  const problems = body?.problems ?? [];

  return (
    <div className="px-5 md:px-10 py-8 max-w-[1000px] w-full mx-auto flex flex-col gap-10">
      <div className="flex flex-col gap-4">
        <div>
          <h1 className="text-2xl font-medium tracking-tight text-text-primary m-0 mb-1">Evaluation</h1>
          <p className="text-sm text-text-secondary m-0">
            Add a provider and its key here. The server hands the judge, the panel and the keys to a worker with each job, and every session runs in OpenCode.
          </p>
        </div>
        <EvaluationTabs />
      </div>
      {problems.length > 0 && (
        <div className="flex flex-col gap-1 border border-warning/40 bg-warning/5 rounded-lg px-4 py-3">
          {problems.map((p) => (
            <p key={p} className="text-xs text-warning m-0 flex gap-2">
              <AlertTriangle size={14} className="shrink-0 mt-0.5" />
              {p}
            </p>
          ))}
        </div>
      )}
      {providers.isLoading && <RowsSkeleton rows={4} />}
      {body && (!configured || showPresets) && (
        <Section
          title={configured ? "Add a provider" : "Choose a provider"}
          desc="Paste one key to start. A provider with default models fills an empty panel, so an evaluation can run right away."
          action={
            configured && (
              <Button variant="ghost" size="sm" onClick={() => setShowPresets(false)}>
                Close
              </Button>
            )
          }
        >
          <Presets presets={body.presets ?? []} compact={configured} hasPanel={configured} />
        </Section>
      )}
      {body && configured && (
        <Section
          title="Judge and panel"
          desc="Each list comes from the provider's catalog. Any typed id works too, such as a new model or an Azure deployment name."
          action={
            !showPresets && (
              <Button variant="ghost" size="sm" onClick={() => setShowPresets(true)}>
                Add a provider
              </Button>
            )
          }
        >
          <PanelEditor body={body} />
        </Section>
      )}
      {body && (
        <Section title="Providers" desc="Each provider is one endpoint and its key, sealed on the server.">
          <ProvidersTable providers={body.providers ?? []} />
        </Section>
      )}
      <Section
        title="Workers"
        desc="Each worker checks in while it runs. A worker needs only its endpoint and its worker key."
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
