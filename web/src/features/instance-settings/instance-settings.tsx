import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Dialog as DialogPrimitive } from "radix-ui";
import { AlertTriangle, BookOpen, Check, FlaskConical, Lock, RotateCcw, X } from "lucide-react";
import { toast } from "sonner";
import { getMasterKey, getSettings, resetMasterKey, testSettings, updateSettings } from "@/api/sdk.gen";
import type { KeyStatus, SettingsBody, SettingsChange, SettingsField, SettingsSection } from "@/api/types.gen";
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
import { MarkdownRenderer } from "@/features/skills/markdown-renderer";
import { throwOnApiError } from "@/lib/api-error";
import { relativeTime } from "@/lib/relative-time";
import { cn } from "@/lib/utils";

export const SETTINGS_KEY = ["instance-settings"];
export const MASTER_KEY_KEY = ["master-key"];
const PLACEHOLDER_URL = "https://skael.example.com";

const fieldClass =
  "w-full h-9 px-3 bg-bg-primary border border-border rounded-[5px] text-sm text-text-primary placeholder:text-text-tertiary outline-none focus:border-border-active transition-colors disabled:opacity-60 disabled:cursor-not-allowed";

// A draft is what the owner typed and has not saved. A secret draft holds a
// new value or a reference; clear resets the setting to its default.
type Draft = { value?: string; reference?: string; clear?: boolean };

export function publicURLOf(body: SettingsBody | undefined): string {
  const field = body?.sections?.flatMap((s) => s.fields ?? []).find((f) => f.key === "public_url");
  return (field?.value || window.location.origin).replace(/\/$/, "");
}

function SourceBadge({ field }: { field: SettingsField }) {
  if (field.locked_by) {
    return (
      <span className="inline-flex items-center gap-1 text-[11px] text-text-tertiary font-mono">
        <Lock size={11} /> set by {field.locked_by}
      </span>
    );
  }
  if (field.source === "ui") {
    return (
      <span className="text-[11px] text-text-tertiary">
        saved{field.updated_by ? ` by ${field.updated_by}` : ""}
        {field.updated_at ? ` ${relativeTime(field.updated_at)}` : ""}
      </span>
    );
  }
  return null;
}

function SecretControl({
  field,
  draft,
  onChange,
  disabled,
}: {
  field: SettingsField;
  draft: Draft | undefined;
  onChange: (d: Draft | undefined) => void;
  disabled: boolean;
}) {
  const [mode, setMode] = useState<"value" | "reference">(field.reference ? "reference" : "value");
  const status = field.is_set
    ? field.reference
      ? `Read from ${field.reference}`
      : field.locked_by
        ? `Set by ${field.locked_by}`
        : "Set"
    : "Not set";
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <span
          className={cn(
            "text-[11px] px-1.5 py-0.5 rounded border font-mono",
            field.is_set ? "border-success/40 text-success" : "border-border text-text-tertiary",
          )}
        >
          {status}
        </span>
        {!disabled && (
          <div className="flex text-[11px] border border-border rounded overflow-hidden" role="group" aria-label={`${field.label} input`}>
            {(["value", "reference"] as const).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => {
                  setMode(m);
                  onChange(undefined);
                }}
                className={cn(
                  "px-2 py-0.5",
                  mode === m ? "bg-bg-tertiary text-text-primary" : "text-text-tertiary hover:text-text-secondary",
                )}
              >
                {m === "value" ? "Paste value" : "Reference"}
              </button>
            ))}
          </div>
        )}
      </div>
      {!disabled && mode === "value" && (
        <input
          type="password"
          autoComplete="new-password"
          className={fieldClass}
          aria-label={field.label}
          placeholder={field.is_set ? "Enter a new value to replace it" : "Paste the value"}
          value={draft?.value ?? ""}
          onChange={(e) => onChange(e.target.value ? { value: e.target.value } : undefined)}
        />
      )}
      {!disabled && mode === "reference" && (
        <input
          className={cn(fieldClass, "font-mono")}
          aria-label={`${field.label} reference`}
          placeholder="env:NAME or file:/run/secrets/name"
          value={draft?.reference ?? field.reference ?? ""}
          onChange={(e) => onChange(e.target.value && e.target.value !== field.reference ? { reference: e.target.value } : undefined)}
        />
      )}
    </div>
  );
}

function ValueControl({
  field,
  draft,
  onChange,
  disabled,
}: {
  field: SettingsField;
  draft: Draft | undefined;
  onChange: (d: Draft | undefined) => void;
  disabled: boolean;
}) {
  const current = draft?.clear ? field.default : (draft?.value ?? field.value);
  const set = (v: string) => onChange(v === field.value ? undefined : v === "" ? { clear: true } : { value: v });
  const options = field.options ?? [];
  const suggestions = field.suggestions ?? [];

  if (field.kind === "bool") {
    const on = current === "true";
    return (
      <button
        type="button"
        role="switch"
        aria-checked={on}
        aria-label={field.label}
        disabled={disabled}
        onClick={() => set(on ? "false" : "true")}
        className={cn(
          "relative w-9 h-5 rounded-full transition-colors disabled:opacity-60",
          on ? "bg-accent" : "bg-bg-tertiary border border-border",
        )}
      >
        <span className={cn("absolute top-0.5 w-4 h-4 rounded-full bg-text-primary transition-all", on ? "left-[18px]" : "left-0.5")} />
      </button>
    );
  }
  if (field.kind === "enum") {
    return (
      <select className={fieldClass} aria-label={field.label} disabled={disabled} value={current} onChange={(e) => set(e.target.value)}>
        {!field.default && <option value="">Not set</option>}
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    );
  }
  const numeric = field.kind === "int" || field.kind === "float";
  return (
    <div className="flex flex-col gap-1.5">
      <input
        className={cn(fieldClass, (field.kind === "duration" || suggestions.length > 0) && "font-mono")}
        aria-label={field.label}
        type={numeric ? "number" : field.kind === "url" ? "url" : "text"}
        inputMode={numeric ? "decimal" : undefined}
        min={field.min ?? undefined}
        max={field.max ?? undefined}
        step={field.kind === "float" ? "any" : undefined}
        placeholder={field.default || (field.kind === "url" ? "https://" : "")}
        disabled={disabled}
        value={current}
        onChange={(e) => set(e.target.value)}
      />
      {suggestions.length > 0 && !disabled && (
        <div className="flex flex-wrap gap-1.5">
          {suggestions.map((s) => (
            <button
              key={s.value}
              type="button"
              onClick={() => set(s.value)}
              className={cn(
                "text-[11px] px-2 py-0.5 rounded border transition-colors",
                current === s.value
                  ? "border-accent text-accent"
                  : "border-border text-text-secondary hover:text-text-primary hover:border-border-active",
              )}
            >
              {s.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function FieldRow({
  field,
  draft,
  onChange,
}: {
  field: SettingsField;
  draft: Draft | undefined;
  onChange: (d: Draft | undefined) => void;
}) {
  const disabled = field.env_only || !!field.locked_by;
  const canReset = !disabled && field.source === "ui" && !draft?.clear;
  return (
    <div className="grid grid-cols-1 md:grid-cols-[minmax(0,2fr)_minmax(0,3fr)] gap-x-6 gap-y-2 px-4 py-4 border-t border-border first:border-t-0">
      <div className="flex flex-col gap-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="text-sm text-text-primary">{field.label}</span>
          {draft && <span className="w-1.5 h-1.5 rounded-full bg-accent" aria-label="unsaved" />}
        </div>
        <p className="text-xs text-text-tertiary m-0 leading-relaxed">{field.help}</p>
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-[11px] text-text-tertiary font-mono">{field.env}</span>
          <SourceBadge field={field} />
        </div>
      </div>
      <div className="flex flex-col gap-1.5 min-w-0">
        {field.kind === "secret" ? (
          <SecretControl field={field} draft={draft} onChange={onChange} disabled={disabled} />
        ) : (
          <ValueControl field={field} draft={draft} onChange={onChange} disabled={disabled} />
        )}
        {field.problem && (
          <p className="text-xs text-warning m-0 flex gap-1.5 items-start">
            <AlertTriangle size={13} className="mt-0.5 shrink-0" />
            {field.problem}
          </p>
        )}
        {canReset && (
          <button
            type="button"
            onClick={() => onChange({ clear: true })}
            className="self-start text-[11px] text-text-tertiary hover:text-text-secondary inline-flex items-center gap-1"
          >
            <RotateCcw size={11} /> Reset to default{field.default ? ` (${field.default})` : ""}
          </button>
        )}
        {draft?.clear && <p className="text-[11px] text-text-tertiary m-0">Resets to the default when you save.</p>}
      </div>
    </div>
  );
}

function GuideDrawer({
  section,
  publicURL,
  open,
  onOpenChange,
}: {
  section: SettingsSection;
  publicURL: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const guide = section.guide.split(PLACEHOLDER_URL).join(publicURL);
  const urls: [string, string][] =
    section.id === "signin"
      ? [
          ["Google redirect URI", `${publicURL}/api/auth/oauth/google/callback`],
          ["GitHub callback URL", `${publicURL}/api/auth/oauth/github/callback`],
          ["OIDC redirect URI", `${publicURL}/api/auth/oauth/oidc/callback`],
        ]
      : [];
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange} modal={false}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Content
          onInteractOutside={(e) => e.preventDefault()}
          className="fixed right-0 top-0 bottom-0 z-50 w-full sm:w-[440px] bg-bg-secondary border-l border-border shadow-[-8px_0_24px_rgba(0,0,0,0.25)] flex flex-col data-[state=open]:animate-in data-[state=open]:slide-in-from-right"
        >
          <div className="flex items-center justify-between px-5 py-4 border-b border-border">
            <DialogPrimitive.Title className="text-sm font-medium text-text-primary m-0">{section.title}</DialogPrimitive.Title>
            <DialogPrimitive.Close asChild>
              <button type="button" aria-label="Close the guide" className="text-text-tertiary hover:text-text-primary">
                <X size={16} />
              </button>
            </DialogPrimitive.Close>
          </div>
          <DialogPrimitive.Description className="sr-only">How to set up {section.title}</DialogPrimitive.Description>
          <div className="flex-1 overflow-y-auto px-5 py-4">
            {urls.length > 0 && (
              <div className="flex flex-col gap-2 mb-4">
                {urls.map(([label, url]) => (
                  <div key={label} className="flex flex-col gap-1">
                    <span className="text-[11px] text-text-tertiary uppercase tracking-wider">{label}</span>
                    <div className="flex items-center gap-2 bg-bg-primary border border-border rounded-[5px] px-3 py-1.5">
                      <code className="font-mono text-[12px] text-text-primary flex-1 min-w-0 break-all">{url}</code>
                      <CopyButton text={url} what={label} />
                    </div>
                  </div>
                ))}
              </div>
            )}
            <MarkdownRenderer content={guide} className="text-[13px] leading-6" />
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

function toChanges(drafts: Record<string, Draft>): SettingsChange[] {
  return Object.entries(drafts).map(([key, d]) => {
    if (d.clear) return { key, clear: true };
    if (d.reference !== undefined) return { key, reference: d.reference };
    return { key, value: d.value };
  });
}

function SectionCard({ section, publicURL }: { section: SettingsSection; publicURL: string }) {
  const qc = useQueryClient();
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const [guideOpen, setGuideOpen] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null);
  const fields = section.fields ?? [];
  const dirty = Object.keys(drafts).length > 0;
  const editable = fields.some((f) => !f.env_only);

  const save = useMutation({
    mutationFn: async () => {
      const res = await updateSettings({ body: { changes: toChanges(drafts) } });
      throwOnApiError(`save ${section.title}`, res);
      return res.data!;
    },
    onSuccess: (data) => {
      qc.setQueryData(SETTINGS_KEY, data);
      qc.invalidateQueries({ queryKey: MASTER_KEY_KEY });
      setDrafts({});
      setResult(null);
      toast.success(`${section.title} saved. Every replica applies it now.`);
    },
    onError: (err) => toast.error(err.message),
  });

  const test = useMutation({
    mutationFn: async () => {
      const res = await testSettings({ path: { section: section.id } });
      throwOnApiError(`test ${section.title}`, res);
      return res.data!;
    },
    onSuccess: (data) => setResult(data),
    onError: (err) => toast.error(err.message),
  });

  return (
    <section id={`section-${section.id}`} className="scroll-mt-6" aria-labelledby={`title-${section.id}`}>
      <div className="flex items-end justify-between gap-3 mb-3">
        <div>
          <h2 id={`title-${section.id}`} className="text-base font-medium text-text-primary m-0 tracking-tight">
            {section.title}
          </h2>
          {!editable && <p className="text-xs text-text-tertiary m-0 mt-1">Set in the environment. Restart the server after a change.</p>}
        </div>
        <div className="flex items-center gap-2">
          {section.guide && (
            <Button type="button" variant="ghost" size="sm" className="text-text-secondary" onClick={() => setGuideOpen(true)}>
              <BookOpen size={14} /> Info
            </Button>
          )}
          {section.test && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="border-border text-text-secondary"
              disabled={dirty || test.isPending}
              title={dirty ? "Save first; the test uses the saved settings" : undefined}
              onClick={() => test.mutate()}
            >
              <FlaskConical size={14} /> {test.isPending ? "Testing…" : "Test"}
            </Button>
          )}
        </div>
      </div>
      <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
        {fields.map((f) => (
          <FieldRow
            key={f.key}
            field={f}
            draft={drafts[f.key]}
            onChange={(d) =>
              setDrafts((prev) => {
                const next = { ...prev };
                if (d) next[f.key] = d;
                else delete next[f.key];
                return next;
              })
            }
          />
        ))}
        {result && (
          <div
            role="status"
            className={cn(
              "px-4 py-3 border-t border-border text-xs whitespace-pre-line flex gap-2",
              result.ok ? "text-success" : "text-warning",
            )}
          >
            {result.ok ? <Check size={14} className="shrink-0" /> : <AlertTriangle size={14} className="shrink-0" />}
            {result.message}
          </div>
        )}
        {dirty && (
          <div className="flex items-center justify-end gap-2 px-4 py-3 border-t border-border bg-bg-primary/40">
            <Button type="button" variant="ghost" size="sm" onClick={() => setDrafts({})}>
              Discard
            </Button>
            <Button type="button" size="sm" disabled={save.isPending} onClick={() => save.mutate()}>
              {save.isPending ? "Saving…" : "Save"}
            </Button>
          </div>
        )}
      </div>
      {section.guide && <GuideDrawer section={section} publicURL={publicURL} open={guideOpen} onOpenChange={setGuideOpen} />}
    </section>
  );
}

export function MasterKeyBanner({ status }: { status: KeyStatus }) {
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const reset = useMutation({
    mutationFn: async () => {
      const res = await resetMasterKey();
      throwOnApiError("start a new master key", res);
      return res.data!;
    },
    onSuccess: (data) => {
      qc.setQueryData(MASTER_KEY_KEY, data);
      qc.invalidateQueries({ queryKey: SETTINGS_KEY });
      setConfirm(false);
      toast.success("The new master key is in use. Enter the listed secrets again.");
    },
    onError: (err) => toast.error(err.message),
  });
  const missing = status.missing ?? [];
  const unreadable = status.unreadable ?? [];
  if (missing.length === 0 && unreadable.length === 0) return null;
  return (
    <div role="alert" className="border border-warning/40 bg-warning/5 rounded-lg p-4 flex flex-col gap-3 mb-6">
      <div className="flex gap-2 items-start">
        <AlertTriangle size={16} className="text-warning shrink-0 mt-0.5" />
        <div className="flex flex-col gap-1">
          <p className="text-sm text-text-primary m-0">
            {missing.length > 0
              ? `The master key ${missing.join(", ")} is missing. The secrets it sealed do not open.`
              : "Some saved secrets do not open with the current master key."}
          </p>
          <p className="text-xs text-text-secondary m-0">
            No skill, version, user or score is lost. Restore the old key file or SKAEL_MASTER_KEYS and restart, or start a new key and enter these secrets again:
          </p>
        </div>
      </div>
      {unreadable.length > 0 && (
        <ul className="m-0 pl-7 text-xs text-text-secondary flex flex-col gap-0.5">
          {unreadable.map((u) => (
            <li key={u.name}>
              <span className="text-text-primary">{u.name}</span> — {u.where}
            </li>
          ))}
        </ul>
      )}
      <div className="pl-6">
        <Button type="button" size="sm" variant="outline" className="border-warning/40" onClick={() => setConfirm(true)}>
          Start a new key
        </Button>
      </div>
      <Dialog open={confirm} onOpenChange={setConfirm}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Start a new master key?</DialogTitle>
            <DialogDescription>
              This deletes every listed secret. A GitHub App in the list is disconnected, and you connect it again. Do this only if the old key cannot be restored.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setConfirm(false)}>
              Cancel
            </Button>
            <Button variant="destructive" disabled={reset.isPending} onClick={() => reset.mutate()}>
              {reset.isPending ? "Starting…" : "Delete the secrets and start"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function MasterKeyPanel({ status }: { status: KeyStatus }) {
  return (
    <div className="bg-bg-secondary border border-border rounded-lg px-4 py-3 mb-3 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1.5 text-xs">
      <span className="text-text-tertiary">Master key</span>
      <span className="text-text-primary">
        {status.source === "file" ? `Generated file ${status.path}` : "SKAEL_MASTER_KEYS"}
      </span>
      <span className="text-text-tertiary">Key ID</span>
      <span className="font-mono text-text-primary">{status.key_id || "none"}</span>
      <span className="text-text-tertiary">Fingerprint</span>
      <span className="font-mono text-text-primary">{status.fingerprint || "none"}</span>
      {status.source === "file" && (
        <>
          <span />
          <span className="text-text-tertiary">Back up this file with Postgres. It is the only copy of the key.</span>
        </>
      )}
    </div>
  );
}

export function InstanceSettings() {
  const { user } = useAuth();
  const allowed = isPrivileged(user);
  const settings = useQuery({
    queryKey: SETTINGS_KEY,
    enabled: allowed,
    queryFn: async () => {
      const res = await getSettings();
      throwOnApiError("load the settings", res);
      return res.data!;
    },
  });
  const key = useQuery({
    queryKey: MASTER_KEY_KEY,
    enabled: allowed,
    queryFn: async () => {
      const res = await getMasterKey();
      throwOnApiError("load the master key", res);
      return res.data!;
    },
  });

  const sections = useMemo(() => (settings.data?.sections ?? []).filter((s) => s.binary === "server"), [settings.data]);
  const editable = sections.filter((s) => (s.fields ?? []).some((f) => !f.env_only));
  const envOnly = sections.filter((s) => !(s.fields ?? []).some((f) => !f.env_only));
  const publicURL = publicURLOf(settings.data);

  if (!allowed) {
    return (
      <div className="px-5 md:px-10 py-8">
        <EmptyState message="Only an owner or an admin can change the instance settings." />
      </div>
    );
  }

  return (
    <div className="px-5 md:px-10 py-8 max-w-[1100px] w-full mx-auto">
      <h1 className="text-2xl font-medium tracking-tight text-text-primary m-0 mb-1">Instance settings</h1>
      <p className="text-sm text-text-secondary m-0 mb-6">
        A saved change applies on every replica without a restart. An environment variable wins over this page and locks its field.
      </p>
      {key.data && <MasterKeyBanner status={key.data} />}
      {settings.isLoading && <RowsSkeleton rows={6} />}
      {settings.error && <p className="text-sm text-warning">{settings.error.message}</p>}
      {settings.data && (
        <div className="grid grid-cols-1 lg:grid-cols-[180px_minmax(0,1fr)] gap-8">
          <nav aria-label="Settings sections" className="hidden lg:flex flex-col gap-0.5 sticky top-6 self-start text-sm">
            {editable.map((s) => (
              <a key={s.id} href={`#section-${s.id}`} className="px-2 py-1 rounded text-text-secondary hover:text-text-primary hover:bg-bg-tertiary">
                {s.title}
              </a>
            ))}
            <span className="px-2 pt-4 pb-1 text-[11px] uppercase tracking-wider text-text-tertiary">Environment only</span>
            {envOnly.map((s) => (
              <a key={s.id} href={`#section-${s.id}`} className="px-2 py-1 rounded text-text-secondary hover:text-text-primary hover:bg-bg-tertiary">
                {s.title}
              </a>
            ))}
          </nav>
          <div className="flex flex-col gap-10 min-w-0">
            {editable.map((s) => (
              <SectionCard key={s.id} section={s} publicURL={publicURL} />
            ))}
            {envOnly.map((s) => (
              <div key={s.id}>
                {s.id === "deployment" && key.data && <MasterKeyPanel status={key.data} />}
                {s.id === "storage" && (
                  <div className="bg-bg-secondary border border-border rounded-lg px-4 py-3 mb-3 text-xs flex gap-6">
                    <span className="text-text-tertiary">Archives are in</span>
                    <span className="font-mono text-text-primary">
                      {settings.data.archive_store === "postgres" ? "Postgres (the archives table)" : settings.data.archive_store}
                    </span>
                  </div>
                )}
                <SectionCard section={s} publicURL={publicURL} />
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
