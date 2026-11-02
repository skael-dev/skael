import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { authError, postAuth } from "@/app/auth-provider";
import { providerLabel } from "@/features/auth/auth-card";
import { useSignInMethods } from "@/features/auth/use-sign-in-methods";

const inputClass = "h-8 rounded-md bg-bg-primary border border-border px-2.5 text-sm text-text-primary focus:outline-none focus:border-border-active";
const actionClass = "text-[12px] text-text-secondary hover:text-accent transition-colors cursor-pointer bg-transparent border-none font-sans";

interface Enrolment {
  secret: string;
  uri: string;
}

function useAccount() {
  return useQuery({
    queryKey: ["auth", "account"],
    queryFn: async () => {
      const res = await fetch("/api/auth/account");
      if (!res.ok) throw new Error("Failed to load the account");
      return (await res.json()) as { linked_providers: string[]; totp_enabled: boolean };
    },
  });
}

function TotpRow() {
  const [enrolment, setEnrolment] = useState<Enrolment | null>(null);
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState<string[] | null>(null);
  const { data: account } = useAccount();
  const [changed, setChanged] = useState<boolean | null>(null);
  const enabled = changed ?? account?.totp_enabled ?? false;

  async function enrol() {
    const res = await postAuth("/totp/enrol");
    if (!res.ok) {
      toast.error((await authError(res, "Could not start setup")).message);
      return;
    }
    setEnrolment((await res.json()) as Enrolment);
  }

  async function confirm() {
    const res = await postAuth("/totp/confirm", { code });
    if (!res.ok) {
      toast.error((await authError(res, "Invalid code")).message);
      return;
    }
    const data = (await res.json()) as { recoveryCodes?: string[] };
    setRecovery(data.recoveryCodes ?? []);
    setEnrolment(null);
    setChanged(true);
    setCode("");
  }

  async function disable() {
    const res = await postAuth("/totp/disable", { code });
    if (!res.ok) {
      toast.error((await authError(res, "Invalid code")).message);
      return;
    }
    setChanged(false);
    setCode("");
  }

  return (
    <div className="p-3.5 flex flex-col gap-2.5">
      <div className="text-[13px] text-text-primary font-medium">Two-factor authentication</div>
      {recovery && (
        <div>
          <p className="text-[12px] text-text-secondary mb-1.5">Store these recovery codes now. They are not shown again.</p>
          <pre className="p-2.5 bg-bg-tertiary border border-border rounded text-[12px] font-mono text-text-primary">{recovery.join("\n")}</pre>
        </div>
      )}
      {enrolment ? (
        <div className="flex flex-col gap-2">
          <p className="text-[12px] text-text-secondary">Add this secret to your authenticator app, then enter its code.</p>
          <code className="p-2 bg-bg-tertiary border border-border rounded text-[12px] font-mono text-text-primary break-all">{enrolment.secret}</code>
          <div className="flex items-center gap-2">
            <input aria-label="Authenticator code" value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" placeholder="6-digit code" className={inputClass} />
            <button onClick={confirm} className={actionClass}>Confirm</button>
          </div>
        </div>
      ) : enabled ? (
        <div className="flex items-center gap-2">
          <input aria-label="Authenticator code" value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" placeholder="6-digit code" className={inputClass} />
          <button onClick={disable} className={actionClass}>Turn off</button>
        </div>
      ) : (
        <div>
          <button onClick={enrol} className={actionClass}>Set up</button>
        </div>
      )}
    </div>
  );
}

function ProviderRows() {
  const methods = useSignInMethods();
  const queryClient = useQueryClient();
  const { data: account } = useAccount();

  async function link(id: string) {
    const res = await postAuth(`/account/link/${id}`);
    if (!res.ok) {
      toast.error((await authError(res, "Could not start the link")).message);
      return;
    }
    window.location.assign(((await res.json()) as { url: string }).url);
  }

  async function unlink(id: string) {
    const res = await postAuth(`/account/unlink/${id}`);
    if (!res.ok) {
      toast.error((await authError(res, "Could not unlink")).message);
      return;
    }
    await queryClient.invalidateQueries({ queryKey: ["auth", "account"] });
  }

  return (
    <>
      {methods?.providers.map((id) => {
        const linked = account?.linked_providers.includes(id);
        return (
          <div key={id} className="px-3.5 py-3 flex items-center justify-between border-t border-border">
            <span className="text-[13px] text-text-primary">{providerLabel(id)}</span>
            <button onClick={() => (linked ? unlink(id) : link(id))} className={actionClass}>
              {linked ? "Unlink" : "Link"}
            </button>
          </div>
        );
      })}
    </>
  );
}

export function SecuritySection() {
  return (
    <div>
      <div className="mb-3.5">
        <div className="text-base font-semibold text-text-primary mb-0.5">Security</div>
        <div className="text-[13px] text-text-secondary">Two-factor authentication and sign-in providers</div>
      </div>
      <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
        <TotpRow />
        <ProviderRows />
      </div>
    </div>
  );
}
