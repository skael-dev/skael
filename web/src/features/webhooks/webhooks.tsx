import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { AlertTriangle, ChevronDown, ChevronRight, ExternalLink, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import {
  createWebhookEndpoint,
  deleteWebhookEndpoint,
  listSets,
  listWebhookDeliveries,
  listWebhookEndpoints,
  redeliverWebhook,
  rotateWebhookSecret,
  testWebhookEndpoint,
  updateWebhookEndpoint,
} from "@/api/sdk.gen";
import type { Delivery, Endpoint } from "@/api/types.gen";
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
import { apiErrorMessage } from "@/lib/api-error";
import { relativeTime } from "@/lib/relative-time";

const EVENT_TYPES = [
  { id: "skill.version.published", label: "A skill version is released" },
  { id: "skill.version.approved", label: "A review releases a held version" },
  { id: "set.changed", label: "A set's resolution changes" },
  { id: "submission.decided", label: "A reviewer decides a submission" },
];
const SET_CHANGED = "set.changed";

const RIVER_UI_PATH = "/api/admin/river";
const POLL_MS = 3000;
const LOG_LIMIT = 50;
const MAX_WINDOW_HOURS = 168;

const fieldClass =
  "w-full h-9 px-3 bg-bg-primary border border-border rounded-[5px] text-sm text-text-primary placeholder:text-text-tertiary outline-none focus:border-border-active transition-colors";
const labelClass = "text-[11px] font-medium text-text-secondary uppercase tracking-wider";
const outlineClass = "border-border text-text-secondary";
const primaryClass = "bg-accent text-bg-primary hover:bg-accent/90";
const retryClass = "text-[13px] text-accent hover:underline cursor-pointer";

const ENDPOINTS_KEY = ["webhook-endpoints"];
const deliveriesKey = (id: string) => ["webhook-endpoints", id, "deliveries"];

type SetRef = { id: string; name: string };
type SecretNotice = { endpoint: string; secret: string; rotated: boolean };

const formatTime = (iso: string) => new Date(iso).toLocaleString();

// A refused address has two ways out, and the generic 422 step names neither.
function saveError(action: string, res: { error?: unknown; response?: Response }): Error {
  const detail = (res.error as { detail?: string } | undefined)?.detail ?? "";
  const steps = detail.startsWith("refused address")
    ? {
        422: "Use a public https URL, or set WEBHOOK_ALLOW_PRIVATE=true for an address on the install's own network.",
      }
    : undefined;
  return new Error(apiErrorMessage(action, res, steps));
}

function EndpointForm({
  endpoint,
  sets,
  setsFailed,
  onRetrySets,
  onSaved,
  onCancel,
}: {
  endpoint?: Endpoint;
  sets: SetRef[];
  setsFailed: boolean;
  onRetrySets: () => void;
  onSaved: (secret: SecretNotice | null) => void;
  onCancel: () => void;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(endpoint?.name ?? "");
  const [url, setUrl] = useState(endpoint?.url ?? "");
  const [types, setTypes] = useState<string[]>(endpoint?.event_types ?? []);
  const [setIds, setSetIds] = useState<string[]>(endpoint?.set_ids ?? []);
  const [enabled, setEnabled] = useState(endpoint?.enabled ?? true);
  const prefix = endpoint?.id ?? "new";
  const wantsSets = types.includes(SET_CHANGED);

  const save = useMutation({
    mutationFn: async (): Promise<SecretNotice | null> => {
      // A set that no longer exists is refused by the server, and the form does
      // not show it. Without a loaded list the ids go back as they came.
      const known = setsFailed ? setIds : setIds.filter((id) => sets.some((s) => s.id === id));
      const body = { name, url, event_types: types, set_ids: wantsSets ? known : [] };
      if (endpoint) {
        const res = await updateWebhookEndpoint({
          path: { id: endpoint.id },
          body: { ...body, enabled },
        });
        if (res.error !== undefined) throw saveError("save the endpoint", res);
        return null;
      }
      const res = await createWebhookEndpoint({ body });
      if (res.error !== undefined) throw saveError("create the endpoint", res);
      return { endpoint: res.data!.name, secret: res.data!.secret, rotated: false };
    },
    onSuccess: (secret) => {
      toast.success(endpoint ? `Saved endpoint ${name}` : `Created endpoint ${name}`);
      queryClient.invalidateQueries({ queryKey: ENDPOINTS_KEY });
      onSaved(secret);
    },
  });

  const toggle = (list: string[], id: string, on: boolean) =>
    on ? [...list, id] : list.filter((x) => x !== id);

  return (
    <form
      className="flex flex-col gap-3 bg-bg-secondary border border-border rounded-lg p-3.5"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="flex flex-col gap-1.5">
        <label htmlFor={`webhook-name-${prefix}`} className={labelClass}>
          Name
        </label>
        <input
          id={`webhook-name-${prefix}`}
          autoFocus
          value={name}
          maxLength={128}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. factoree-cache"
          className={fieldClass}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <label htmlFor={`webhook-url-${prefix}`} className={labelClass}>
          URL
        </label>
        <input
          id={`webhook-url-${prefix}`}
          value={url}
          maxLength={2048}
          inputMode="url"
          autoComplete="off"
          spellCheck={false}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="https://example.com/hooks/skael"
          className={`${fieldClass} font-mono`}
        />
      </div>

      <fieldset className="flex flex-col gap-1.5 border-0 p-0 m-0">
        <legend className={`${labelClass} mb-1.5`}>Event types</legend>
        {EVENT_TYPES.map((type) => (
          <label key={type.id} className="flex items-start gap-2 text-xs text-text-secondary">
            <input
              type="checkbox"
              className="mt-0.5 accent-[var(--color-accent)]"
              checked={types.includes(type.id)}
              onChange={(e) => setTypes((list) => toggle(list, type.id, e.target.checked))}
            />
            <span className="min-w-0">
              <span className="font-mono text-text-primary break-all">{type.id}</span> {type.label}
            </span>
          </label>
        ))}
      </fieldset>

      {wantsSets && (
        <fieldset className="flex flex-col gap-1.5 border-0 p-0 m-0">
          <legend className={`${labelClass} mb-1.5`}>Limit set.changed to these sets</legend>
          {setsFailed ? (
            <p className="text-xs text-danger m-0">
              Could not load sets.{" "}
              <button type="button" onClick={onRetrySets} className={retryClass}>
                Retry
              </button>
            </p>
          ) : (
            <div className="flex flex-col gap-1.5 max-h-40 overflow-y-auto">
              {sets.map((s) => (
                <label key={s.id} className="flex items-center gap-2 text-xs text-text-secondary">
                  <input
                    type="checkbox"
                    className="accent-[var(--color-accent)]"
                    checked={setIds.includes(s.id)}
                    onChange={(e) => setSetIds((list) => toggle(list, s.id, e.target.checked))}
                  />
                  <span className="font-mono text-text-primary truncate">{s.name}</span>
                </label>
              ))}
            </div>
          )}
          <p className="text-[11px] text-text-tertiary m-0">
            {setIds.some((id) => sets.some((s) => s.id === id))
              ? "This endpoint receives set.changed only for the selected sets."
              : "No set is selected, so this endpoint receives set.changed for all sets."}
          </p>
        </fieldset>
      )}

      {endpoint && (
        <label className="flex items-center gap-2 text-xs text-text-secondary">
          <input
            type="checkbox"
            className="accent-[var(--color-accent)]"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          Enabled. A disabled endpoint receives nothing.
        </label>
      )}

      {save.isError && <p className="text-xs text-danger m-0 break-words">{save.error.message}</p>}
      {!save.isError && name && url && types.length === 0 && (
        <p className="text-[11px] text-text-tertiary m-0">Select at least one event type.</p>
      )}

      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" className={outlineClass} onClick={onCancel}>
          Cancel
        </Button>
        <Button
          type="submit"
          size="sm"
          className={primaryClass}
          disabled={save.isPending || !name.trim() || !url.trim() || types.length === 0}
        >
          {save.isPending ? "Saving..." : endpoint ? "Save endpoint" : "Create endpoint"}
        </Button>
      </div>
    </form>
  );
}

function SecretPanel({ notice, onDone }: { notice: SecretNotice; onDone: () => void }) {
  return (
    <div className="flex flex-col gap-2.5 p-3 bg-bg-tertiary border border-border-active rounded-lg">
      <div className="flex items-start gap-2 text-[11px] text-warning leading-relaxed">
        <AlertTriangle className="size-3.5 shrink-0 mt-0.5" />
        <span>
          Copy the {notice.rotated ? "new " : ""}signing secret of{" "}
          <span className="font-mono">{notice.endpoint}</span> now. It is not shown again.
        </span>
      </div>
      <code className="font-mono text-[12px] text-text-primary break-all leading-relaxed select-all">
        {notice.secret}
      </code>
      <div className="flex flex-wrap justify-end gap-2">
        <CopyButton text={notice.secret} what="secret" />
        <Button size="sm" className={primaryClass} onClick={onDone}>
          Done
        </Button>
      </div>
    </div>
  );
}

function RotateForm({
  endpoint,
  onRotated,
  onCancel,
}: {
  endpoint: Endpoint;
  onRotated: (notice: SecretNotice) => void;
  onCancel: () => void;
}) {
  const queryClient = useQueryClient();
  const [hours, setHours] = useState("24");
  const parsed = Number(hours);
  const valid =
    hours.trim() !== "" && Number.isInteger(parsed) && parsed >= 0 && parsed <= MAX_WINDOW_HOURS;

  const rotate = useMutation({
    mutationFn: async () => {
      const res = await rotateWebhookSecret({
        path: { id: endpoint.id },
        body: { window_hours: parsed },
      });
      if (res.error !== undefined) throw new Error(apiErrorMessage("rotate the secret", res));
      return res.data!;
    },
    onSuccess: (data) => {
      toast.success(`Rotated the secret of ${endpoint.name}`);
      queryClient.invalidateQueries({ queryKey: ENDPOINTS_KEY });
      onRotated({ endpoint: endpoint.name, secret: data.secret, rotated: true });
    },
    onError: (err) => toast.error(err.message),
  });

  return (
    <form
      className="flex flex-col gap-2.5 p-3 border border-border rounded-lg"
      onSubmit={(e) => {
        e.preventDefault();
        if (valid) rotate.mutate();
      }}
    >
      <div className="flex flex-col gap-1.5">
        <label htmlFor={`rotate-${endpoint.id}`} className={labelClass}>
          Old secret stays valid for (hours)
        </label>
        <input
          id={`rotate-${endpoint.id}`}
          autoFocus
          type="number"
          min={0}
          max={MAX_WINDOW_HOURS}
          step={1}
          value={hours}
          onChange={(e) => setHours(e.target.value)}
          className={`${fieldClass} font-mono max-w-32`}
        />
        <p className={`text-[11px] m-0 ${valid ? "text-text-tertiary" : "text-danger"}`}>
          {valid
            ? "Enter 0 to stop the old secret at once."
            : `Enter a whole number from 0 to ${MAX_WINDOW_HOURS}.`}
        </p>
      </div>
      {rotate.isError && <p className="text-xs text-danger m-0">{rotate.error.message}</p>}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" className={outlineClass} onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" size="sm" className={primaryClass} disabled={!valid || rotate.isPending}>
          {rotate.isPending ? "Rotating..." : "Rotate secret"}
        </Button>
      </div>
    </form>
  );
}

const STATE_CLASS: Record<string, string> = {
  delivered: "text-text-secondary",
  pending: "text-accent",
  failed: "text-danger",
};

function DeliveryRow({ delivery, endpoint }: { delivery: Delivery; endpoint: Endpoint }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const toggle = () => setOpen((o) => !o);
  const row = useListNavigation<null>(toggle);
  const attempts = delivery.attempts ?? [];
  const last = attempts[attempts.length - 1];
  const Chevron = open ? ChevronDown : ChevronRight;

  const redeliver = useMutation({
    mutationFn: async () => {
      const res = await redeliverWebhook({ path: { id: delivery.id } });
      if (res.error !== undefined) throw new Error(apiErrorMessage("redeliver the event", res));
    },
    onSuccess: () => {
      toast.success(`Queued ${delivery.event_type} again with the same event id`);
      setOpen(true);
      queryClient.invalidateQueries({ queryKey: deliveriesKey(endpoint.id) });
    },
    onError: (err) => toast.error(err.message),
  });

  return (
    <div className="border-b border-border last:border-b-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3 py-2.5">
        <button
          type="button"
          {...row(null)}
          tabIndex={0}
          aria-expanded={open}
          onClick={toggle}
          className="flex-1 basis-full sm:basis-0 min-w-0 flex flex-wrap items-center gap-x-3 gap-y-1 text-left cursor-pointer bg-transparent border-none p-0"
        >
          <span className="flex items-center gap-2 min-w-0">
            <Chevron className="size-3.5 text-text-tertiary shrink-0" />
            <span className="font-mono text-[12px] text-text-primary truncate">
              {delivery.event_type}
            </span>
          </span>
          <span
            className={`font-mono text-[11px] ${STATE_CLASS[delivery.state] ?? "text-text-secondary"}`}
          >
            {delivery.state}
          </span>
          <span className="font-mono text-[11px] text-text-tertiary whitespace-nowrap">
            {attempts.length} {attempts.length === 1 ? "attempt" : "attempts"}
            {last ? `, last ${relativeTime(last.created_at)}` : ""}
          </span>
        </button>
        <div className="flex items-center gap-2 ml-auto">
          <CopyButton text={delivery.event_id} what="event id" label="Event id" />
          {delivery.state !== "pending" && (
            <Button
              variant="outline"
              size="sm"
              className={outlineClass}
              disabled={redeliver.isPending || !endpoint.enabled}
              onClick={() => redeliver.mutate()}
            >
              {redeliver.isPending ? "Queuing..." : "Redeliver"}
            </Button>
          )}
        </div>
      </div>
      {open && (
        <div className="px-3 pb-3 flex flex-col gap-2">
          <div className="text-[11px] text-text-tertiary">
            Event id <code className="font-mono text-text-secondary break-all">{delivery.event_id}</code>
          </div>
          {attempts.length === 0 ? (
            <p className="text-[11px] text-text-tertiary m-0">
              No attempt yet. The server sends this delivery next.
            </p>
          ) : (
            <ol className="flex flex-col m-0 p-0 list-none border border-border rounded-[5px]">
              {attempts.map((a, i) => (
                <li
                  key={`${a.created_at}-${i}`}
                  className="flex flex-wrap gap-x-3 gap-y-0.5 px-2.5 py-1.5 border-b border-border last:border-b-0 font-mono text-[11px]"
                >
                  <span className="text-text-tertiary">{i + 1}</span>
                  <span className="text-text-secondary whitespace-nowrap">
                    {formatTime(a.created_at)}
                  </span>
                  <span className={a.succeeded ? "text-text-primary" : "text-danger"}>
                    {a.response_code ?? "no response"}
                  </span>
                  {a.error && (
                    <span className="basis-full text-text-tertiary break-all">{a.error}</span>
                  )}
                </li>
              ))}
            </ol>
          )}
        </div>
      )}
    </div>
  );
}

function DeliveryLog({ endpoint, onSendTest }: { endpoint: Endpoint; onSendTest: () => void }) {
  const deliveries = useQuery({
    queryKey: deliveriesKey(endpoint.id),
    queryFn: async () => {
      const res = await listWebhookDeliveries({
        path: { id: endpoint.id },
        query: { limit: LOG_LIMIT },
      });
      if (res.error !== undefined) {
        throw new Error(apiErrorMessage("load the delivery log", res));
      }
      return res.data?.deliveries ?? [];
    },
    // The worker skips a disabled endpoint, so its pending rows do not change.
    refetchInterval: (query) =>
      endpoint.enabled && query.state.data?.some((d) => d.state === "pending") ? POLL_MS : false,
  });
  const list = deliveries.data ?? [];

  return (
    <div className="flex flex-col gap-1.5">
      <h2 className={labelClass}>Delivery log</h2>
      <div className="border border-border rounded-lg overflow-hidden bg-bg-secondary">
        {deliveries.isLoading ? (
          <RowsSkeleton rows={2} />
        ) : deliveries.isError ? (
          <EmptyState
            message={deliveries.error.message}
            action={
              <button onClick={() => deliveries.refetch()} className={retryClass}>
                Retry
              </button>
            }
          />
        ) : list.length === 0 ? (
          <EmptyState
            message="No deliveries yet."
            action={
              endpoint.enabled && (
                <button onClick={onSendTest} className={retryClass}>
                  Send a test event
                </button>
              )
            }
          />
        ) : (
          list.map((d) => <DeliveryRow key={d.id} delivery={d} endpoint={endpoint} />)
        )}
      </div>
      {list.length === LOG_LIMIT && (
        <p className="text-[11px] text-text-tertiary m-0">Shows the newest {LOG_LIMIT} deliveries.</p>
      )}
    </div>
  );
}

function EndpointPanel({
  endpoint,
  sets,
  setsFailed,
  onRetrySets,
  onSecret,
  onDelete,
}: {
  endpoint: Endpoint;
  sets: SetRef[];
  setsFailed: boolean;
  onRetrySets: () => void;
  onSecret: (notice: SecretNotice) => void;
  onDelete: () => void;
}) {
  const queryClient = useQueryClient();
  const [mode, setMode] = useState<"view" | "edit" | "rotate">("view");
  const expiry = endpoint.previous_secret_expires_at;
  const oldSecretLive = !!expiry && new Date(expiry).getTime() > Date.now();

  const sendTest = useMutation({
    mutationFn: async () => {
      const res = await testWebhookEndpoint({ path: { id: endpoint.id } });
      if (res.error !== undefined) throw new Error(apiErrorMessage("send the test event", res));
    },
    onSuccess: () => {
      toast.success(`Queued a test event for ${endpoint.name}`);
      queryClient.invalidateQueries({ queryKey: deliveriesKey(endpoint.id) });
    },
    onError: (err) => toast.error(err.message),
  });

  return (
    <div className="flex flex-col gap-4 px-3.5 py-3.5 bg-bg-primary border-b border-border last:border-b-0">
      {mode === "edit" ? (
        <EndpointForm
          endpoint={endpoint}
          sets={sets}
          setsFailed={setsFailed}
          onRetrySets={onRetrySets}
          onSaved={() => setMode("view")}
          onCancel={() => setMode("view")}
        />
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            className={outlineClass}
            disabled={sendTest.isPending || !endpoint.enabled}
            onClick={() => sendTest.mutate()}
          >
            {sendTest.isPending ? "Sending..." : "Send test event"}
          </Button>
          <Button variant="outline" size="sm" className={outlineClass} onClick={() => setMode("edit")}>
            Edit
          </Button>
          <Button
            variant="outline"
            size="sm"
            className={outlineClass}
            onClick={() => setMode(mode === "rotate" ? "view" : "rotate")}
          >
            Rotate secret
          </Button>
          <Button
            variant="outline"
            size="sm"
            className={`${outlineClass} hover:text-destructive sm:ml-auto`}
            onClick={onDelete}
          >
            <Trash2 className="size-3" />
            Delete
          </Button>
        </div>
      )}

      {!endpoint.enabled && (
        <p className="text-[11px] text-text-tertiary m-0">
          This endpoint is disabled and receives nothing. Edit it and select Enabled to send events.
        </p>
      )}
      {oldSecretLive && (
        <p className="text-[11px] text-text-secondary m-0">
          The old secret stays valid until{" "}
          <span className="font-mono text-text-primary">{formatTime(expiry)}</span>.
        </p>
      )}

      {mode === "rotate" && (
        <RotateForm
          endpoint={endpoint}
          onRotated={(notice) => {
            setMode("view");
            onSecret(notice);
          }}
          onCancel={() => setMode("view")}
        />
      )}

      <DeliveryLog endpoint={endpoint} onSendTest={() => sendTest.mutate()} />
    </div>
  );
}

export function Webhooks() {
  const { user } = useAuth();
  const allowed = isPrivileged(user);
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const creating = params.get("new") === "1";
  const [expanded, setExpanded] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<Endpoint | null>(null);
  const [secret, setSecret] = useState<SecretNotice | null>(null);

  const toggle = (id: string) => setExpanded((cur) => (cur === id ? null : id));
  const endpointRow = useListNavigation<string>(toggle);

  const endpoints = useQuery({
    queryKey: ENDPOINTS_KEY,
    enabled: allowed,
    queryFn: async () => {
      const res = await listWebhookEndpoints();
      if (res.error !== undefined) throw new Error(apiErrorMessage("load endpoints", res));
      return res.data?.endpoints ?? [];
    },
  });

  const sets = useQuery({
    queryKey: ["sets"],
    enabled: allowed,
    queryFn: async () => {
      const res = await listSets();
      if (res.error !== undefined) throw new Error(apiErrorMessage("load sets", res));
      return res.data?.sets ?? [];
    },
  });
  const setList: SetRef[] = sets.data ?? [];

  const remove = useMutation({
    mutationFn: async (endpoint: Endpoint) => {
      const res = await deleteWebhookEndpoint({ path: { id: endpoint.id } });
      if (res.error !== undefined) throw new Error(apiErrorMessage("delete the endpoint", res));
      return endpoint;
    },
    onSuccess: (endpoint) => {
      toast.success(`Deleted endpoint ${endpoint.name} and its delivery log`);
      setDeleting(null);
      setExpanded((cur) => (cur === endpoint.id ? null : cur));
      queryClient.invalidateQueries({ queryKey: ENDPOINTS_KEY });
    },
    onError: (err) => toast.error(err.message),
  });

  if (!allowed) {
    return (
      <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto">
        <EmptyState
          message="Only an owner or an admin can manage webhooks."
          action={
            <Link to="/" className="text-[13px] text-accent hover:underline">
              Open skills
            </Link>
          }
        />
      </div>
    );
  }

  const list = endpoints.data ?? [];

  const setLimit = (e: Endpoint) => {
    if (!(e.event_types ?? []).includes(SET_CHANGED)) return null;
    const ids = e.set_ids ?? [];
    if (ids.length === 0) return "all sets";
    return ids.map((id) => setList.find((s) => s.id === id)?.name ?? "deleted set").join(", ");
  };

  return (
    <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-60">
          <h1 className="text-lg font-medium text-text-primary">Webhooks</h1>
          <p className="text-xs text-text-tertiary mt-1">
            An endpoint receives signed events when skills, sets and submissions change.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <a
            href={RIVER_UI_PATH}
            className="inline-flex items-center gap-1.5 h-8 px-2 text-xs text-text-secondary hover:text-text-primary transition-colors"
          >
            <ExternalLink className="size-3" />
            River UI
          </a>
          {!creating && (
            <Button
              variant="outline"
              size="sm"
              className={outlineClass}
              onClick={() => setParams({ new: "1" })}
            >
              <Plus className="size-3" />
              New endpoint
            </Button>
          )}
        </div>
      </div>

      {secret && <SecretPanel notice={secret} onDone={() => setSecret(null)} />}

      {creating && (
        <EndpointForm
          sets={setList}
          setsFailed={sets.isError}
          onRetrySets={() => sets.refetch()}
          onSaved={(notice) => {
            setParams({});
            setSecret(notice);
          }}
          onCancel={() => setParams({})}
        />
      )}

      <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
        {endpoints.isLoading ? (
          <RowsSkeleton />
        ) : endpoints.isError ? (
          <EmptyState
            message={endpoints.error.message}
            action={
              <button onClick={() => endpoints.refetch()} className={retryClass}>
                Retry
              </button>
            }
          />
        ) : list.length === 0 ? (
          <EmptyState
            message="No endpoints yet."
            action={
              !creating && (
                <button onClick={() => setParams({ new: "1" })} className={retryClass}>
                  Create the first one
                </button>
              )
            }
          />
        ) : (
          list.map((endpoint) => {
            const open = expanded === endpoint.id;
            const Chevron = open ? ChevronDown : ChevronRight;
            const limit = setLimit(endpoint);
            return (
              <div key={endpoint.id}>
                <button
                  type="button"
                  {...endpointRow(endpoint.id)}
                  tabIndex={0}
                  aria-expanded={open}
                  onClick={() => toggle(endpoint.id)}
                  className="w-full flex items-start gap-2 px-3.5 py-3 text-left cursor-pointer bg-transparent border-0 border-b border-solid border-border hover:bg-bg-tertiary transition-colors"
                >
                  <Chevron className="size-3.5 text-text-tertiary shrink-0 mt-0.5" />
                  <span className="flex-1 min-w-0 flex flex-col gap-1.5">
                    <span className="flex flex-wrap items-center justify-between gap-x-3 gap-y-0.5">
                      <span className="text-[13px] font-medium text-text-primary truncate">
                        {endpoint.name}
                      </span>
                      <span
                        className={`font-mono text-[11px] ${endpoint.enabled ? "text-text-secondary" : "text-text-tertiary"}`}
                      >
                        {endpoint.enabled ? "enabled" : "disabled"}
                      </span>
                    </span>
                    <span className="font-mono text-[11px] text-text-secondary break-all">
                      {endpoint.url}
                    </span>
                    <span className="flex flex-wrap gap-1.5">
                      {(endpoint.event_types ?? []).map((t) => (
                        <span
                          key={t}
                          className="text-[11px] font-mono text-text-secondary bg-bg-tertiary px-1.5 py-0.5 rounded"
                        >
                          {t}
                        </span>
                      ))}
                      {limit && (
                        <span className="text-[11px] font-mono text-accent bg-accent-surface px-1.5 py-0.5 rounded break-all">
                          {limit}
                        </span>
                      )}
                    </span>
                  </span>
                </button>
                {open && (
                  <EndpointPanel
                    endpoint={endpoint}
                    sets={setList}
                    setsFailed={sets.isError}
                    onRetrySets={() => sets.refetch()}
                    onSecret={setSecret}
                    onDelete={() => setDeleting(endpoint)}
                  />
                )}
              </div>
            );
          })
        )}
      </div>

      <Dialog open={deleting !== null} onOpenChange={(o) => !o && setDeleting(null)}>
        <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
          <DialogHeader className="text-left">
            <DialogTitle className="text-text-primary">Delete this endpoint?</DialogTitle>
            <DialogDescription className="text-text-secondary">
              This deletes <span className="text-text-primary">{deleting?.name}</span> and its
              delivery log. Its receiver gets no further events.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              className={outlineClass}
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
              {remove.isPending ? "Deleting..." : "Delete endpoint"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
