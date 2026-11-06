import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, ChevronDown, ChevronRight, Plus, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import {
  adoptGitSkill,
  createGitConnection,
  deleteGitConnection,
  getGitConnection,
  listGitConnections,
  listGitPulls,
  listGitRepos,
  requestGitPullContest,
  previewGitConnection,
  syncGitConnection,
  updateGitConnection,
} from "@/api/sdk.gen";
import type { Connection, Preview, PreviewItem } from "@/api/types.gen";
import { Button } from "@/components/ui/button";
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
import { cn } from "@/lib/utils";
import { throwOnApiError } from "@/lib/api-error";
import { relativeTime } from "@/lib/relative-time";

const CONNECTIONS_KEY = ["git-connections"];
const connectionKey = (id: string) => ["git-connection", id];

const fieldClass =
  "w-full h-9 px-3 bg-bg-primary border border-border rounded-[5px] text-sm text-text-primary placeholder:text-text-tertiary outline-none focus:border-border-active transition-colors";
const labelClass = "text-[11px] font-medium text-text-secondary uppercase tracking-wider";

const CONTEST_LABELS: Record<string, string> = {
  off: "Off: the PR check scans only",
  on_request: "On request: a label or a button starts a contest",
  ready: "Every PR marked ready for review",
};

function StatusDot({ status }: { status: string }) {
  return (
    <span
      className={cn(
        "size-2 rounded-full shrink-0",
        status === "ok" ? "bg-accent" : status === "error" ? "bg-destructive" : "bg-text-tertiary",
      )}
      aria-label={`Status: ${status}`}
    />
  );
}

function repoPath(c: Connection) {
  return `${c.repo_owner}/${c.repo_name}${c.root_path ? `/${c.root_path}` : ""}`;
}

/** Every connection, with the way to make one. */
export function GitConnections() {
  const query = useQuery({
    queryKey: CONNECTIONS_KEY,
    queryFn: async () => {
      const res = await listGitConnections();
      throwOnApiError("load connections", res);
      return res.data!.connections ?? [];
    },
  });
  const list = query.data ?? [];

  return (
    <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-60">
          <h1 className="text-lg font-medium text-text-primary">Git sync</h1>
          <p className="text-xs text-text-tertiary mt-1">
            A connection syncs every skill folder under a repo path. Git stays the source of truth: a merge publishes
            a new version.
          </p>
        </div>
        <Button asChild variant="outline" size="sm" className="border-border text-text-secondary">
          <Link to="/git/connections/new">
            <Plus className="size-3" />
            New connection
          </Link>
        </Button>
      </div>
      <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
        {query.isLoading ? (
          <RowsSkeleton />
        ) : query.isError ? (
          <EmptyState message={query.error.message} />
        ) : list.length === 0 ? (
          <EmptyState
            message="No connections yet."
            action={
              <Link to="/git/connections/new" className="text-[13px] text-accent hover:underline">
                Connect a repo
              </Link>
            }
          />
        ) : (
          list.map((c) => (
            <Link
              key={c.id}
              to={`/git/connections/${c.id}`}
              className="flex flex-wrap items-center gap-x-4 gap-y-1 px-3.5 py-3 border-b border-border last:border-b-0 hover:bg-bg-tertiary transition-colors"
            >
              <span className="flex items-center gap-2 flex-1 basis-full sm:basis-0 min-w-0">
                <StatusDot status={c.status} />
                <span className="font-mono text-[13px] text-text-primary [overflow-wrap:anywhere]">{repoPath(c)}</span>
              </span>
              <span className="font-mono text-[11px] text-text-secondary">{c.prefix}</span>
              <span className="text-[11px] text-text-tertiary whitespace-nowrap">
                Synced {relativeTime(c.last_sync_at)}
              </span>
            </Link>
          ))
        )}
      </div>
    </div>
  );
}

const GROUPS: { kind: string; title: string; note: string }[] = [
  { kind: "adopt", title: "Adopt", note: "Imported earlier from this repo and path. Their history stays." },
  {
    kind: "match",
    title: "Same name, other origin",
    note: "A skill with this name exists but did not come from here. Adopt one only if it is the same skill.",
  },
  { kind: "new", title: "Create", note: "New skills, published as version 1." },
  { kind: "refused", title: "Refuse", note: "The connection does not take these." },
];

function DiffBlock({ item }: { item: PreviewItem }) {
  const [open, setOpen] = useState(false);
  if (!item.diff) return null;
  const Chevron = open ? ChevronDown : ChevronRight;
  return (
    <div className="mt-1">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex items-center gap-1 text-[11px] text-text-tertiary hover:text-text-primary bg-transparent border-none p-0 cursor-pointer"
      >
        <Chevron className="size-3" />
        Diff against v{item.diff.against}
      </button>
      {open && (
        <pre className="mt-1 max-h-64 overflow-auto bg-bg-primary border border-border rounded p-2 text-[11px] font-mono text-text-secondary whitespace-pre">
          {item.diff.skill_md || "SKILL.md is the same."}
          {(item.diff.files ?? []).map((f) => `\n${f.status} ${f.path}`).join("")}
        </pre>
      )}
    </div>
  );
}

function PreviewList({
  preview,
  selected,
  toggle,
}: {
  preview: Preview;
  selected: Set<string>;
  toggle: (name: string) => void;
}) {
  const items = preview.items ?? [];
  return (
    <div className="flex flex-col gap-4">
      {GROUPS.map((g) => {
        const group = items.filter((i) => i.kind === g.kind);
        if (group.length === 0) return null;
        const selectable = g.kind === "adopt" || g.kind === "match";
        return (
          <div key={g.kind}>
            <div className="text-[11px] font-medium text-text-secondary uppercase tracking-wider">
              {g.title} · {group.length}
            </div>
            <p className="text-[11px] text-text-tertiary mt-0.5 mb-1.5">{g.note}</p>
            <div className="bg-bg-primary border border-border rounded-md">
              {group.map((item) => (
                <label
                  key={item.path}
                  className={cn(
                    "flex items-start gap-2.5 px-3 py-2 border-b border-border last:border-b-0",
                    selectable && "cursor-pointer",
                  )}
                >
                  {selectable && (
                    <input
                      type="checkbox"
                      className="mt-1 accent-[var(--color-accent)]"
                      checked={selected.has(item.name)}
                      onChange={() => toggle(item.name)}
                    />
                  )}
                  <span className="min-w-0 flex-1">
                    <span className="block font-mono text-[12px] text-text-primary [overflow-wrap:anywhere]">
                      {item.name}
                    </span>
                    <span className="block font-mono text-[11px] text-text-tertiary [overflow-wrap:anywhere]">
                      {item.path}
                    </span>
                    {item.reason && <span className="block text-[11px] text-warning">{item.reason}</span>}
                    {g.kind === "match" && (
                      <span className="block text-[11px] text-text-secondary mt-1">
                        Registry: {item.existing_description || "no description"}
                        <br />
                        Repo: {item.description || "no description"}
                      </span>
                    )}
                    {selectable && <DiffBlock item={item} />}
                  </span>
                </label>
              ))}
            </div>
          </div>
        );
      })}
    </div>
  );
}

/** The form that previews a connection and then creates it. */
export function NewGitConnection() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [repo, setRepo] = useState("");
  const [branch, setBranch] = useState("");
  const [rootPath, setRootPath] = useState("");
  const [prefix, setPrefix] = useState("");
  const [contestMode, setContestMode] = useState("on_request");
  const [preview, setPreview] = useState<Preview | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const repos = useQuery({
    queryKey: ["git-repos"],
    queryFn: async () => {
      const res = await listGitRepos();
      throwOnApiError("list repos", res);
      return res.data!.repos ?? [];
    },
  });

  const [owner, name] = repo.split("/");
  const body = useMemo(
    () => ({
      repo_owner: owner ?? "",
      repo_name: name ?? "",
      branch,
      prefix,
      contest_mode: contestMode as "off" | "on_request" | "ready",
      ...(rootPath.trim() ? { root_path: rootPath.trim() } : {}),
    }),
    [owner, name, branch, prefix, contestMode, rootPath],
  );

  const runPreview = useMutation({
    mutationFn: async () => {
      const res = await previewGitConnection({ body });
      throwOnApiError("preview the connection", res);
      return res.data!;
    },
    onSuccess: (p) => {
      setPreview(p);
      setSelected(new Set((p.items ?? []).filter((i) => i.kind === "adopt").map((i) => i.name)));
    },
    onError: (err) => toast.error(err.message),
  });

  const create = useMutation({
    mutationFn: async () => {
      const res = await createGitConnection({ body: { ...body, adopt: [...selected] } });
      throwOnApiError("create the connection", res);
      return res.data!;
    },
    onSuccess: (c) => {
      toast.success(`Connected ${repoPath(c)}. The first sync is running.`);
      queryClient.invalidateQueries({ queryKey: CONNECTIONS_KEY });
      navigate(`/git/connections/${c.id}`);
    },
    onError: (err) => toast.error(err.message),
  });

  const counts = useMemo(() => {
    const items = preview?.items ?? [];
    return {
      adopt: selected.size,
      create: items.filter((i) => i.kind === "new").length,
      refuse:
        items.filter((i) => i.kind === "refused").length +
        items.filter((i) => (i.kind === "adopt" || i.kind === "match") && !selected.has(i.name)).length,
    };
  }, [preview, selected]);

  const reset = () => setPreview(null);
  const ready = owner && name && branch.trim() && prefix.trim();

  return (
    <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto flex flex-col gap-4">
      <Link to="/git/connections" className="inline-flex items-center gap-1.5 text-[12px] text-text-secondary hover:text-text-primary">
        <ArrowLeft className="size-3.5" />
        Git sync
      </Link>
      <h1 className="text-lg font-medium text-text-primary">New connection</h1>

      <form
        className="bg-bg-secondary border border-border rounded-lg p-4 flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          runPreview.mutate();
        }}
      >
        <label className="flex flex-col gap-1.5">
          <span className={labelClass}>Repo</span>
          <input
            className={fieldClass}
            list="git-repos"
            value={repo}
            placeholder="acme/skills"
            onChange={(e) => {
              setRepo(e.target.value);
              const match = (repos.data ?? []).find((r) => `${r.owner}/${r.name}` === e.target.value);
              if (match && !branch) setBranch(match.default_branch);
              reset();
            }}
          />
          <datalist id="git-repos">
            {(repos.data ?? []).map((r) => (
              <option key={`${r.owner}/${r.name}`} value={`${r.owner}/${r.name}`} />
            ))}
          </datalist>
          {repos.isError && (
            <span className="text-[11px] text-text-tertiary">
              Could not list repos: {repos.error.message} Type the repo instead.
            </span>
          )}
        </label>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <label className="flex flex-col gap-1.5">
            <span className={labelClass}>Branch</span>
            <input className={fieldClass} value={branch} placeholder="main" onChange={(e) => (setBranch(e.target.value), reset())} />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className={labelClass}>Root path (optional)</span>
            <input className={fieldClass} value={rootPath} placeholder="skills" onChange={(e) => (setRootPath(e.target.value), reset())} />
          </label>
        </div>
        <label className="flex flex-col gap-1.5">
          <span className={labelClass}>Name prefix</span>
          <input className={fieldClass} value={prefix} placeholder="platform:*" onChange={(e) => (setPrefix(e.target.value), reset())} />
          <span className="text-[11px] text-text-tertiary">
            The connection may only publish names that match. You need to own this prefix, or be an admin.
          </span>
        </label>
        <label className="flex flex-col gap-1.5">
          <span className={labelClass}>Contests on pull requests</span>
          <select className={fieldClass} value={contestMode} onChange={(e) => setContestMode(e.target.value)}>
            {Object.entries(CONTEST_LABELS).map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </label>
        <div>
          <Button type="submit" size="sm" variant="outline" className="border-border text-text-secondary" disabled={!ready || runPreview.isPending}>
            {runPreview.isPending ? "Reading the repo..." : "Preview"}
          </Button>
        </div>
      </form>

      {preview && (
        <div className="bg-bg-secondary border border-border rounded-lg p-4 flex flex-col gap-4">
          <p className="text-xs text-text-secondary m-0">
            At <span className="font-mono">{preview.head_sha.slice(0, 7)}</span>: adopt {counts.adopt}, create{" "}
            {counts.create}, refuse {counts.refuse}. Nothing changes until you create the connection.
          </p>
          {(preview.items ?? []).length === 0 ? (
            <EmptyState message="No SKILL.md under this path." />
          ) : (
            <PreviewList
              preview={preview}
              selected={selected}
              toggle={(n) =>
                setSelected((prev) => {
                  const next = new Set(prev);
                  if (next.has(n)) next.delete(n);
                  else next.add(n);
                  return next;
                })
              }
            />
          )}
          <div>
            <Button size="sm" disabled={create.isPending} onClick={() => create.mutate()}>
              {create.isPending ? "Creating..." : "Create connection"}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

/** One connection: its skills, what it refused, and how it hears from GitHub. */
export function GitConnectionPage() {
  const { id = "" } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [deleting, setDeleting] = useState(false);
  const query = useQuery({
    queryKey: connectionKey(id),
    refetchInterval: 15_000,
    queryFn: async () => {
      const res = await getGitConnection({ path: { id } });
      throwOnApiError("load the connection", res);
      return res.data!;
    },
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: connectionKey(id) });

  const sync = useMutation({
    mutationFn: async () => throwOnApiError("sync the connection", await syncGitConnection({ path: { id } })),
    onSuccess: () => toast.success("Sync queued."),
    onError: (err) => toast.error(err.message),
  });
  const adopt = useMutation({
    mutationFn: async (name: string) => {
      throwOnApiError("adopt the skill", await adoptGitSkill({ path: { id }, body: { name } }));
      return name;
    },
    onSuccess: (name) => {
      toast.success(`Adopted ${name}. The next sync publishes it from the repo.`);
      refresh();
    },
    onError: (err) => toast.error(err.message),
  });
  const setMode = useMutation({
    mutationFn: async (mode: string) =>
      throwOnApiError(
        "change the contest setting",
        await updateGitConnection({ path: { id }, body: { contest_mode: mode as "off" | "on_request" | "ready" } }),
      ),
    onSuccess: () => refresh(),
    onError: (err) => toast.error(err.message),
  });
  const pulls = useQuery({
    queryKey: [...connectionKey(id), "pulls"],
    refetchInterval: 15_000,
    queryFn: async () => {
      const res = await listGitPulls({ path: { id } });
      throwOnApiError("load pull requests", res);
      return res.data!.pulls ?? [];
    },
  });
  const contest = useMutation({
    mutationFn: async (number: number) => {
      throwOnApiError("ask for a contest", await requestGitPullContest({ path: { id, number } }));
      return number;
    },
    onSuccess: (n) => {
      toast.success(`Contest requested for PR #${n}. It starts at the next sync.`);
      queryClient.invalidateQueries({ queryKey: [...connectionKey(id), "pulls"] });
    },
    onError: (err) => toast.error(err.message),
  });
  const remove = useMutation({
    mutationFn: async () => throwOnApiError("delete the connection", await deleteGitConnection({ path: { id } })),
    onSuccess: () => {
      toast.success("Deleted the connection. Its skills are native now and keep every version.");
      queryClient.invalidateQueries({ queryKey: CONNECTIONS_KEY });
      navigate("/git/connections");
    },
    onError: (err) => toast.error(err.message),
  });

  if (query.isLoading) return <RowsSkeleton />;
  if (query.isError || !query.data) {
    return <div className="p-8 text-sm text-destructive">Could not load the connection. Reload the page to try again.</div>;
  }
  const c = query.data;
  const bindings = c.bindings ?? [];
  const refusals = c.refusals ?? [];

  return (
    <div className="px-5 py-6 md:p-6 max-w-4xl mx-auto flex flex-col gap-4">
      <Link to="/git/connections" className="inline-flex items-center gap-1.5 text-[12px] text-text-secondary hover:text-text-primary">
        <ArrowLeft className="size-3.5" />
        Git sync
      </Link>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-60">
          <h1 className="text-lg font-mono font-medium text-text-primary [overflow-wrap:anywhere]">{repoPath(c)}</h1>
          <p className="text-xs text-text-tertiary mt-1">
            Branch <span className="font-mono">{c.branch}</span> · prefix <span className="font-mono">{c.prefix}</span> ·
            created by {c.created_by}
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" className="border-border text-text-secondary" disabled={sync.isPending} onClick={() => sync.mutate()}>
            <RefreshCw className="size-3" />
            Sync now
          </Button>
          <Button variant="outline" size="sm" className="border-border text-text-secondary" onClick={() => setDeleting(true)}>
            Delete
          </Button>
        </div>
      </div>

      <div className="bg-bg-secondary border border-border rounded-lg p-4 grid grid-cols-1 sm:grid-cols-3 gap-3 text-[12px]">
        <div>
          <div className={labelClass}>Status</div>
          <div className="flex items-center gap-2 mt-1 text-text-primary">
            <StatusDot status={c.status} />
            {c.status}
          </div>
          {c.last_error && <div className="mt-1 text-destructive [overflow-wrap:anywhere]">{c.last_error}</div>}
        </div>
        <div>
          <div className={labelClass}>Last sync</div>
          <div className="mt-1 text-text-primary">
            {relativeTime(c.last_sync_at)}
            {c.last_synced_sha && <span className="font-mono text-text-tertiary"> @ {c.last_synced_sha.slice(0, 7)}</span>}
          </div>
        </div>
        <div>
          <div className={labelClass}>Changes arrive by</div>
          <div className="mt-1 text-text-primary">
            {c.webhooks ? `Webhook, last ${relativeTime(c.last_webhook_at)}` : "A check every minute"}
          </div>
          {!c.webhooks && (
            <div className="mt-1 text-text-tertiary">GitHub has not reached this instance. A change takes up to a minute.</div>
          )}
        </div>
      </div>

      <label className="flex flex-col gap-1.5 max-w-md">
        <span className={labelClass}>Contests on pull requests</span>
        <select className={fieldClass} value={c.contest_mode} onChange={(e) => setMode.mutate(e.target.value)}>
          {Object.entries(CONTEST_LABELS).map(([v, l]) => (
            <option key={v} value={v}>
              {l}
            </option>
          ))}
        </select>
      </label>

      <div>
        <div className={cn(labelClass, "mb-1.5")}>Skills · {bindings.length}</div>
        <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
          {bindings.length === 0 ? (
            <EmptyState message="No skill synced yet." />
          ) : (
            bindings.map((b) => (
              <div key={b.skill_id} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-3.5 py-2.5 border-b border-border last:border-b-0">
                <Link to={`/skills/${b.skill_name}`} className="font-mono text-[13px] text-text-primary hover:underline flex-1 basis-full sm:basis-0 min-w-0 [overflow-wrap:anywhere]">
                  {b.skill_name}
                </Link>
                <span className="font-mono text-[11px] text-text-tertiary [overflow-wrap:anywhere]">{b.path}</span>
                {b.removed_from_git_at && <span className="text-[11px] text-warning">removed from git</span>}
              </div>
            ))
          )}
        </div>
      </div>

      {(pulls.data ?? []).length > 0 && (
        <div>
          <div className={cn(labelClass, "mb-1.5")}>Open pull requests · {(pulls.data ?? []).length}</div>
          <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
            {(pulls.data ?? []).map((p) => {
              const contestable =
                c.contest_mode !== "off" && (p.heads ?? []).some((h) => h.decision.outcome !== "block" && !h.contest_id);
              return (
                <div key={p.pr_number} className="px-3.5 py-2.5 border-b border-border last:border-b-0 flex flex-col gap-1.5">
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                    <a href={p.pr_url} target="_blank" rel="noreferrer" className="text-[13px] text-text-primary hover:underline">
                      #{p.pr_number} {p.title}
                    </a>
                    <span
                      className={cn(
                        "text-[11px]",
                        p.conclusion === "failure" ? "text-destructive" : p.conclusion === "neutral" ? "text-warning" : "text-accent",
                      )}
                    >
                      {p.conclusion === "failure" ? "blocked" : p.conclusion === "neutral" ? "held on merge" : "passes the scan"}
                    </span>
                    {p.fork && <span className="text-[11px] text-text-tertiary">from a fork</span>}
                    <span className="font-mono text-[11px] text-text-tertiary">{p.head_sha.slice(0, 7)}</span>
                    {contestable && (
                      <Button
                        size="sm"
                        variant="outline"
                        className="border-border text-text-secondary ml-auto"
                        disabled={contest.isPending || p.contest_requested}
                        onClick={() => contest.mutate(p.pr_number)}
                      >
                        {p.contest_requested ? "Contest requested" : "Run contest"}
                      </Button>
                    )}
                  </div>
                  {(p.heads ?? []).map((h) => (
                    <div key={h.id} className="flex flex-wrap items-center gap-x-3 text-[11px] text-text-secondary">
                      <span className="font-mono text-text-primary">{h.skill_name}</span>
                      <span>{h.decision.outcome === "block" ? "blocked" : h.decision.outcome === "needs_review" ? "held on merge" : "passes"}</span>
                      {h.contest_id && (
                        <Link to={`/contests/${h.contest_id}`} className="text-accent hover:underline">
                          Contest
                        </Link>
                      )}
                    </div>
                  ))}
                </div>
              );
            })}
          </div>
        </div>
      )}

      {refusals.length > 0 && (
        <div>
          <div className={cn(labelClass, "mb-1.5")}>Refused at the last sync · {refusals.length}</div>
          <div className="bg-bg-secondary border border-border rounded-lg overflow-hidden">
            {refusals.map((r) => (
              <div key={r.path} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-3.5 py-2.5 border-b border-border last:border-b-0">
                <span className="flex-1 basis-full sm:basis-0 min-w-0">
                  <span className="block font-mono text-[12px] text-text-primary [overflow-wrap:anywhere]">{r.skill_name}</span>
                  <span className="block font-mono text-[11px] text-text-tertiary [overflow-wrap:anywhere]">{r.path}</span>
                </span>
                <span className="text-[11px] text-warning basis-full sm:basis-auto">{r.reason}</span>
                {r.reason.startsWith("name taken") && (
                  <Button size="sm" variant="outline" className="border-border text-text-secondary" disabled={adopt.isPending} onClick={() => adopt.mutate(r.skill_name)}>
                    Adopt
                  </Button>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      <Dialog open={deleting} onOpenChange={setDeleting}>
        <DialogContent className="max-w-sm bg-bg-secondary border-border text-text-primary">
          <DialogHeader className="text-left">
            <DialogTitle className="text-text-primary">Delete this connection?</DialogTitle>
            <DialogDescription className="text-text-secondary">
              Its {bindings.length} skills become native and keep every version. Changes in the repo stop reaching
              them. The connection's service account is deleted.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" size="sm" className="border-border text-text-secondary" onClick={() => setDeleting(false)}>
              Cancel
            </Button>
            <Button variant="destructive" size="sm" disabled={remove.isPending} onClick={() => remove.mutate()}>
              {remove.isPending ? "Deleting..." : "Delete connection"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
