import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Download, Loader2, AlertTriangle, Check, Package } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { SecurityBadge } from "@/features/security/security-badge";
import { cn } from "@/lib/utils";

type FileEntry = { path: string; size: number };

type DiscoveredSkill = {
  name: string;
  description: string;
  path: string;
  files: FileEntry[];
  scan_status: string;
  scan_findings_count: number;
};

type ImportSource = {
  type: string;
  owner: string;
  repo: string;
  ref: string;
  path: string;
  commit_sha: string;
};

type ResolveResponse = {
  source: ImportSource;
  skills: DiscoveredSkill[];
};

type ImportResult = {
  imported: { name: string; version: number; scan_status: string }[];
  failed: { name: string; error: string }[];
};

async function resolveImport(url: string): Promise<ResolveResponse> {
  const res = await fetch("/api/import/resolve", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({ url }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.detail || body.title || `Resolve failed (${res.status})`);
  }
  return res.json();
}

async function executeImport(source: ImportSource, skills: string[]): Promise<ImportResult> {
  const res = await fetch("/api/import", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({ source, skills }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.detail || body.title || `Import failed (${res.status})`);
  }
  return res.json();
}

type ImportModalProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
};

export function ImportModal({ open, onOpenChange }: ImportModalProps) {
  const queryClient = useQueryClient();
  const [url, setUrl] = useState("");
  const [resolved, setResolved] = useState<ResolveResponse | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [result, setResult] = useState<ImportResult | null>(null);

  const resolveMutation = useMutation({
    mutationFn: (url: string) => resolveImport(url),
    onSuccess: (data) => {
      setResolved(data);
      setSelected(new Set(data.skills.map((s) => s.name)));
    },
  });

  const importMutation = useMutation({
    mutationFn: ({ source, skills }: { source: ImportSource; skills: string[] }) =>
      executeImport(source, skills),
    onSuccess: (data) => {
      setResult(data);
      queryClient.invalidateQueries({ queryKey: ["analytics"] });
    },
  });

  function handleClose() {
    setUrl("");
    setResolved(null);
    setSelected(new Set());
    setResult(null);
    resolveMutation.reset();
    importMutation.reset();
    onOpenChange(false);
  }

  function toggleSkill(name: string, checked: boolean) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (checked) next.add(name);
      else next.delete(name);
      return next;
    });
  }

  const isResolving = resolveMutation.isPending;
  const isImporting = importMutation.isPending;

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="sm:max-w-2xl bg-bg-primary border-border">
        <DialogHeader>
          <DialogTitle className="text-text-primary">Import Skills</DialogTitle>
          <DialogDescription className="text-text-tertiary">
            Import skills from a GitHub repository into the registry.
          </DialogDescription>
        </DialogHeader>

        {!resolved && !result && (
          <div className="space-y-3">
            <div className="flex gap-2">
              <input
                type="text"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && url.trim()) resolveMutation.mutate(url.trim());
                }}
                placeholder="https://github.com/owner/repo"
                className={cn(
                  "flex-1 h-9 px-3 text-sm bg-bg-secondary border border-border rounded-md",
                  "text-text-primary placeholder:text-text-tertiary",
                  "focus:outline-none focus:ring-1 focus:ring-border-active"
                )}
                disabled={isResolving}
              />
              <Button
                onClick={() => resolveMutation.mutate(url.trim())}
                disabled={!url.trim() || isResolving}
                className="h-9"
              >
                {isResolving ? <Loader2 size={14} className="animate-spin" /> : <Download size={14} />}
                <span className="ml-1.5">{isResolving ? "Resolving..." : "Resolve"}</span>
              </Button>
            </div>
            {resolveMutation.isError && (
              <p className="text-xs text-danger flex items-center gap-1">
                <AlertTriangle size={12} />
                {resolveMutation.error?.message}
              </p>
            )}
          </div>
        )}

        {resolved && !result && (
          <div className="space-y-3">
            <div className="text-xs text-text-tertiary">
              {resolved.source.owner}/{resolved.source.repo}
              {resolved.source.ref && ` · ${resolved.source.ref}`}
              {resolved.source.commit_sha && ` @ ${resolved.source.commit_sha.slice(0, 7)}`}
            </div>

            <div className="max-h-[320px] overflow-y-auto border border-border rounded-md divide-y divide-border">
              {resolved.skills.map((sk) => (
                <label
                  key={sk.name}
                  className="flex items-center gap-3 px-3 py-2.5 hover:bg-bg-secondary cursor-pointer transition-colors"
                >
                  <Checkbox
                    checked={selected.has(sk.name)}
                    onCheckedChange={(v) => toggleSkill(sk.name, v === true)}
                    disabled={isImporting}
                  />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="font-mono text-[13px] text-text-primary font-medium">
                        {sk.name}
                      </span>
                      <SecurityBadge status={sk.scan_status} />
                    </div>
                    <p className="text-xs text-text-tertiary truncate">{sk.description}</p>
                  </div>
                  <span className="text-[11px] text-text-tertiary whitespace-nowrap">
                    {(sk.files ?? []).length} files
                  </span>
                </label>
              ))}
            </div>

            {resolved.skills.length === 0 && (
              <p className="text-sm text-text-tertiary text-center py-4">
                No skills found in this repository.
              </p>
            )}

            {importMutation.isError && (
              <p className="text-xs text-danger flex items-center gap-1">
                <AlertTriangle size={12} />
                {importMutation.error?.message}
              </p>
            )}
          </div>
        )}

        {result && (
          <div className="space-y-3">
            {(result.imported ?? []).map((imp) => (
              <div key={imp.name} className="flex items-center gap-2 text-sm">
                <Check size={14} className="text-accent" />
                <span className="font-mono text-text-primary">{imp.name}</span>
                <span className="text-text-tertiary">v{imp.version}</span>
              </div>
            ))}
            {(result.failed ?? []).map((fail) => (
              <div key={fail.name} className="flex items-center gap-2 text-sm">
                <AlertTriangle size={14} className="text-danger" />
                <span className="font-mono text-text-primary">{fail.name}</span>
                <span className="text-xs text-danger">{fail.error}</span>
              </div>
            ))}
          </div>
        )}

        <DialogFooter>
          {resolved && !result && (
            <Button
              onClick={() =>
                importMutation.mutate({
                  source: resolved.source,
                  skills: Array.from(selected),
                })
              }
              disabled={selected.size === 0 || isImporting}
            >
              {isImporting ? (
                <>
                  <Loader2 size={14} className="animate-spin mr-1.5" />
                  Importing...
                </>
              ) : (
                <>
                  <Package size={14} className="mr-1.5" />
                  Import {selected.size} skill{selected.size !== 1 ? "s" : ""}
                </>
              )}
            </Button>
          )}
          {result && (
            <Button onClick={handleClose}>Done</Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
