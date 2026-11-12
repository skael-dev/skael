import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Circle, X } from "lucide-react";
import { toast } from "sonner";
import { getOnboarding, loadSampleData, removeSampleData, updateSettings } from "@/api/sdk.gen";
import type { OnboardingStatus, Step } from "@/api/types.gen";
import { isPrivileged, useAuth } from "@/app/auth-provider";
import { Button } from "@/components/ui/button";
import { throwOnApiError } from "@/lib/api-error";
import { cn } from "@/lib/utils";

export const ONBOARDING_KEY = ["onboarding"];
const HIDE_KEY = "skael.getting-started.hidden";

// Where each step is done. The sync and activation steps happen in a terminal,
// so they point at the CLI commands on the skills page.
const STEP_LINK: Record<string, string> = {
  publish: "/",
  sync: "/",
  activate: "/analytics",
  invite: "/settings/instance#section-signin",
  public_url: "/settings/instance#section-general",
  provider: "/settings/evaluation",
  worker: "/settings/evaluation",
  signin: "/settings/instance#section-signin",
  email: "/settings/instance#section-email",
};

const STEP_HINT: Record<string, string> = {
  publish: "skael publish ./my-skill, or connect a repo on Git sync.",
  sync: "skael sync on a developer machine.",
  activate: "Use the skill in Claude Code, Codex, Cursor, OpenCode or pi after skael hook install.",
  invite: "Share this server's URL so a teammate can sign up.",
};

function hidden(): boolean {
  try {
    return localStorage.getItem(HIDE_KEY) === "1";
  } catch {
    return false;
  }
}

function StepRow({ step, hint }: { step: Step; hint?: string }) {
  return (
    <li className="flex items-start gap-2.5">
      {step.done ? (
        <Check size={15} className="text-success mt-0.5 shrink-0" aria-label="done" />
      ) : (
        <Circle size={15} className="text-text-tertiary mt-0.5 shrink-0" aria-label="not done" />
      )}
      <div className="flex flex-col min-w-0">
        <Link to={STEP_LINK[step.id] ?? "/"} className={cn("text-sm hover:underline", step.done ? "text-text-tertiary line-through" : "text-text-primary")}>
          {step.title}
        </Link>
        {!step.done && hint && <span className="text-[11px] text-text-tertiary font-mono">{hint}</span>}
      </div>
    </li>
  );
}

function SetupCard({ status }: { status: OnboardingStatus }) {
  const qc = useQueryClient();
  const origin = window.location.origin;
  const confirm = useMutation({
    mutationFn: async () => {
      const res = await updateSettings({ body: { changes: [{ key: "public_url", value: origin }] } });
      throwOnApiError("save the public URL", res);
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ONBOARDING_KEY });
      toast.success(`The public URL is ${origin}.`);
    },
    onError: (err) => toast.error(err.message),
  });
  const steps = status.setup ?? [];
  return (
    <div className="bg-bg-secondary border border-border rounded-lg p-4 flex flex-col gap-3">
      <span className="text-[11px] text-text-tertiary uppercase tracking-wider">Set up · nothing here blocks the steps</span>
      {!status.public_url && (
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="text-text-secondary">Public URL:</span>
          <code className="font-mono text-text-primary">{origin}</code>
          <Button size="xs" variant="outline" className="border-border" disabled={confirm.isPending} onClick={() => confirm.mutate()}>
            Confirm
          </Button>
        </div>
      )}
      <ul className="m-0 p-0 list-none flex flex-col gap-2">
        {steps
          .filter((s) => s.id !== "public_url")
          .map((s) => (
            <StepRow key={s.id} step={s} />
          ))}
      </ul>
    </div>
  );
}

// GettingStarted is the checklist toward the first activation. It stays until
// every step is done or the viewer hides it in this browser.
export function GettingStarted() {
  const { user } = useAuth();
  const privileged = isPrivileged(user);
  const qc = useQueryClient();
  const [isHidden, setHidden] = useState(hidden);
  const { data } = useQuery({
    queryKey: ONBOARDING_KEY,
    queryFn: async () => {
      const res = await getOnboarding();
      throwOnApiError("load the checklist", res);
      return res.data!;
    },
  });
  const sample = useMutation({
    mutationFn: async (load: boolean) => {
      const res = load ? await loadSampleData() : await removeSampleData();
      throwOnApiError(load ? "load the sample data" : "remove the sample data", res);
      return res.data!;
    },
    onSuccess: (st, load) => {
      qc.setQueryData(ONBOARDING_KEY, st);
      qc.invalidateQueries();
      toast.success(load ? "Sample data loaded. Every sample skill starts with sample-." : "Sample data removed.");
    },
    onError: (err) => toast.error(err.message),
  });

  if (!data || isHidden) return null;
  const steps = data.steps ?? [];
  const done = steps.filter((s) => s.done).length;
  if (done === steps.length && !data.sample_loaded) return null;

  return (
    <div className="px-5 md:px-12 pt-6 max-w-screen-xl w-full mx-auto">
      <div className="grid grid-cols-1 md:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] gap-3">
        <div className="bg-bg-secondary border border-border rounded-lg p-4 flex flex-col gap-3">
          <div className="flex items-center justify-between gap-2">
            <span className="text-sm text-text-primary">
              Get started · {done} of {steps.length}
            </span>
            <button
              type="button"
              aria-label="Hide the checklist"
              className="text-text-tertiary hover:text-text-primary"
              onClick={() => {
                try {
                  localStorage.setItem(HIDE_KEY, "1");
                } catch {
                  // A blocked store only means the checklist shows again next time.
                }
                setHidden(true);
              }}
            >
              <X size={14} />
            </button>
          </div>
          <ol className="m-0 p-0 list-none flex flex-col gap-2.5">
            {steps.map((s) => (
              <StepRow key={s.id} step={s} hint={STEP_HINT[s.id]} />
            ))}
          </ol>
          {privileged && (
            <div className="flex flex-wrap items-center gap-2 pt-1 border-t border-border">
              <span className="text-xs text-text-tertiary pt-2">
                {data.sample_loaded ? "Sample data is loaded." : "See the dashboards with sample skills, scores and 30 days of activations."}
              </span>
              <Button
                size="xs"
                variant="outline"
                className="border-border mt-2"
                disabled={sample.isPending}
                onClick={() => sample.mutate(!data.sample_loaded)}
              >
                {sample.isPending ? "Working…" : data.sample_loaded ? "Remove sample data" : "Load sample data"}
              </Button>
            </div>
          )}
        </div>
        {privileged && <SetupCard status={data} />}
      </div>
    </div>
  );
}
