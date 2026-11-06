import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { ExternalLink } from "lucide-react";
import { toast } from "sonner";
import { createGithubAppManifest, deleteGithubApp, getGithubApp } from "@/api/sdk.gen";
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

const GITHUB_KEY = ["github-app"];
const KEY_COMMAND = 'SKAEL_MASTER_KEYS="k1:$(openssl rand -base64 32)"';

const fieldClass =
  "w-full h-9 px-3 bg-bg-primary border border-border rounded-[5px] text-sm text-text-primary placeholder:text-text-tertiary outline-none focus:border-border-active transition-colors";
const labelClass = "text-[11px] font-medium text-text-secondary uppercase tracking-wider";

function NeedsKeys() {
  return (
    <div className="bg-bg-secondary border border-border rounded-lg p-4 flex flex-col gap-3">
      <p className="text-sm text-text-primary">Set a master key before you connect GitHub.</p>
      <p className="text-xs text-text-secondary">
        Skael encrypts the GitHub App's private key with <span className="font-mono">SKAEL_MASTER_KEYS</span>.
        Generate a key, set it on the server, and restart the server. Keep the key: a new key makes the
        stored App unreadable.
      </p>
      <div className="flex items-center gap-2 bg-bg-primary border border-border rounded-[5px] px-3 py-2">
        <code className="font-mono text-[12px] text-text-primary flex-1 min-w-0 break-all">{KEY_COMMAND}</code>
        <CopyButton text={KEY_COMMAND} what="the key command" />
      </div>
    </div>
  );
}

function ConnectForm({ publicURL }: { publicURL: string }) {
  const [name, setName] = useState("Skael");
  const [organization, setOrganization] = useState("");
  const [enterprise, setEnterprise] = useState("");
  const [post, setPost] = useState<{ postUrl: string; manifest: string } | null>(null);
  const formRef = useRef<HTMLFormElement>(null);

  const start = useMutation({
    mutationFn: async () => {
      const res = await createGithubAppManifest({
        body: {
          name: name.trim(),
          ...(organization.trim() ? { organization: organization.trim() } : {}),
          ...(enterprise.trim() ? { web_base_url: enterprise.trim() } : {}),
        },
      });
      throwOnApiError("start the GitHub App setup", res);
      return res.data!;
    },
    onSuccess: (data) => setPost({ postUrl: data.post_url, manifest: data.manifest }),
    onError: (err) => toast.error(err.message),
  });

  // GitHub needs a real form post from the browser, so it sees the admin's own
  // GitHub session.
  useEffect(() => {
    if (post) formRef.current?.submit();
  }, [post]);

  return (
    <>
    <form
      className="bg-bg-secondary border border-border rounded-lg p-4 flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        start.mutate();
      }}
    >
      <p className="text-xs text-text-secondary">
        Skael creates its GitHub App from a manifest. GitHub asks you to confirm, then returns here. GitHub
        sends its webhooks to <span className="font-mono text-text-primary break-all">{publicURL}/api/github/webhook</span>.
        If GitHub cannot reach that address, Skael checks each repo every minute instead.
      </p>
      <label className="flex flex-col gap-1.5">
        <span className={labelClass}>App name</span>
        <input className={fieldClass} value={name} maxLength={34} onChange={(e) => setName(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1.5">
        <span className={labelClass}>Organization (optional)</span>
        <input
          className={fieldClass}
          value={organization}
          placeholder="acme"
          onChange={(e) => setOrganization(e.target.value)}
        />
        <span className="text-[11px] text-text-tertiary">
          The organization that owns the App. Leave it empty to own the App yourself. Any organization can
          install it later.
        </span>
      </label>
      <label className="flex flex-col gap-1.5">
        <span className={labelClass}>GitHub Enterprise URL (optional)</span>
        <input
          className={fieldClass}
          value={enterprise}
          placeholder="https://ghe.example.com"
          onChange={(e) => setEnterprise(e.target.value)}
        />
      </label>
      <div>
        <Button type="submit" size="sm" disabled={start.isPending || !name.trim() || !!post}>
          {start.isPending || post ? "Opening GitHub..." : "Connect GitHub"}
        </Button>
      </div>
    </form>
    {post && (
      <form ref={formRef} method="post" action={post.postUrl} className="hidden">
        <input type="hidden" name="manifest" value={post.manifest} />
      </form>
    )}
    </>
  );
}

export function GitHubSettings() {
  const { user } = useAuth();
  const allowed = isPrivileged(user);
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const [disconnecting, setDisconnecting] = useState(false);

  const status = useQuery({
    queryKey: GITHUB_KEY,
    enabled: allowed,
    queryFn: async () => {
      const res = await getGithubApp();
      throwOnApiError("load the GitHub connection", res);
      return res.data!;
    },
  });

  useEffect(() => {
    const error = params.get("error");
    const connected = params.get("connected");
    if (error) toast.error(error);
    if (connected) toast.success(`Connected the GitHub App ${connected}. Install it on an organization next.`);
    if (error || connected) setParams({}, { replace: true });
  }, [params, setParams]);

  const remove = useMutation({
    mutationFn: async () => throwOnApiError("disconnect the GitHub App", await deleteGithubApp()),
    onSuccess: () => {
      toast.success("Disconnected the GitHub App. Delete it on GitHub too.");
      setDisconnecting(false);
      queryClient.invalidateQueries({ queryKey: GITHUB_KEY });
    },
    onError: (err) => toast.error(err.message),
  });

  if (!allowed) {
    return (
      <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto">
        <EmptyState
          message="Only an owner or an admin can connect GitHub."
          action={
            <Link to="/" className="text-[13px] text-accent hover:underline">
              Open skills
            </Link>
          }
        />
      </div>
    );
  }

  const data = status.data;
  const app = data?.app;

  return (
    <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto flex flex-col gap-4">
      <div>
        <h1 className="text-lg font-medium text-text-primary">GitHub</h1>
        <p className="text-xs text-text-tertiary mt-1">
          One GitHub App lets Skael sync skills from repos, open pull requests and post checks.
        </p>
      </div>

      {status.isLoading ? (
        <RowsSkeleton />
      ) : status.isError || !data ? (
        <EmptyState
          message={status.error?.message ?? "Could not load the GitHub connection."}
          action={
            <button onClick={() => status.refetch()} className="text-[13px] text-accent hover:underline cursor-pointer">
              Retry
            </button>
          }
        />
      ) : !data.keys_ready ? (
        <NeedsKeys />
      ) : !data.public_url ? (
        <EmptyState message="Set PUBLIC_URL on the server first. GitHub sends its redirect and its webhooks there." />
      ) : app ? (
        <div className="bg-bg-secondary border border-border rounded-lg p-4 flex flex-col gap-3">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <span className="font-mono text-[13px] font-medium text-text-primary">{app.name}</span>
            <span className="text-[11px] text-text-tertiary">
              owned by <span className="font-mono">{app.owner}</span> · connected by {app.created_by}
            </span>
          </div>
          <p className="text-xs text-text-secondary">
            Install the App on each organization whose repos hold skills. An organization owner approves the
            install.
          </p>
          <div className="flex flex-wrap gap-2">
            <Button asChild size="sm">
              <a href={app.install_url} target="_blank" rel="noreferrer">
                Install on an organization
                <ExternalLink className="size-3" />
              </a>
            </Button>
            <Button asChild variant="outline" size="sm" className="border-border text-text-secondary">
              <a href={app.html_url} target="_blank" rel="noreferrer">
                Open on GitHub
              </a>
            </Button>
            <Button asChild variant="outline" size="sm" className="border-border text-text-secondary">
              <Link to="/git/connections">Connect a repo</Link>
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="border-border text-text-secondary"
              onClick={() => setDisconnecting(true)}
            >
              Disconnect
            </Button>
          </div>
        </div>
      ) : (
        <ConnectForm publicURL={data.public_url} />
      )}

      <Dialog open={disconnecting} onOpenChange={setDisconnecting}>
        <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
          <DialogHeader className="text-left">
            <DialogTitle className="text-text-primary">Disconnect the GitHub App?</DialogTitle>
            <DialogDescription className="text-text-secondary">
              Skael forgets the App and its key. The App stays on GitHub until its owner deletes it there.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              className="border-border text-text-secondary"
              onClick={() => setDisconnecting(false)}
            >
              Cancel
            </Button>
            <Button variant="destructive" size="sm" disabled={remove.isPending} onClick={() => remove.mutate()}>
              {remove.isPending ? "Disconnecting..." : "Disconnect"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
