import { useCallback, useMemo } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import type { Version } from "@/api/types.gen";
import { FileTree } from "@/features/skills/file-tree";
import { FileMessage, FileViewer, FileViewerSkeleton } from "@/features/skills/file-viewer";
import { buildFileSearch, parseFileLink } from "@/features/skills/file-link";

const GATE_LABELS: Record<string, string> = {
  released: "released",
  needs_review: "held for review",
  rejected: "rejected",
};

function defaultFile(version: Version | undefined): string | undefined {
  const manifest = version?.file_manifest ?? [];
  return manifest.find((f) => f.path === "SKILL.md")?.path ?? manifest[0]?.path;
}

type FileBrowserProps = {
  skillName: string;
  latestVersion?: number;
  versions: Version[];
  loading: boolean;
  failed: boolean;
  onRetry: () => void;
};

export function FileBrowser({ skillName, latestVersion, versions, loading, failed, onRetry }: FileBrowserProps) {
  const location = useLocation();
  const navigate = useNavigate();
  const link = useMemo(() => parseFileLink(location.search, location.hash), [location.search, location.hash]);

  const sorted = useMemo(() => [...versions].sort((a, b) => b.version - a.version), [versions]);
  // The newest version may be held, and the skill's latest is what is served.
  const selected =
    link.version != null
      ? sorted.find((v) => v.version === link.version)
      : (sorted.find((v) => v.version === latestVersion) ?? sorted[0]);

  const manifest = selected?.file_manifest ?? [];
  const path = link.file ?? defaultFile(selected);
  const entry = manifest.find((f) => f.path === path);
  const selectedVersion = selected?.version;

  const go = useCallback(
    (file: string | undefined, version: number | undefined, line?: number) => {
      const target = {
        search: buildFileSearch({ file, version }),
        hash: line != null ? `#L${line}` : "",
      };
      // A line click refines the current view, so it does not add a history step.
      navigate(target, { replace: line != null });
    },
    [navigate],
  );

  const selectLine = useCallback(
    (line: number) => go(path, selectedVersion, line),
    [go, path, selectedVersion],
  );
  const clearLine = useCallback(() => go(path, selectedVersion), [go, path, selectedVersion]);

  if (loading) {
    return (
      <div className="flex flex-col lg:flex-row gap-6 max-w-[1200px] min-h-[480px]">
        <div role="status" aria-label="Loading files" className="w-full lg:w-[240px] shrink-0 flex flex-col gap-3">
          {[70, 88, 56, 76].map((width, i) => (
            <div key={i} className="h-3 rounded bg-bg-secondary motion-safe:animate-pulse" style={{ width: `${width}%` }} />
          ))}
        </div>
        <FileViewerSkeleton />
      </div>
    );
  }

  if (failed) {
    return (
      <FileMessage
        action={
          <button
            type="button"
            onClick={onRetry}
            className="h-9 px-4 text-sm font-sans rounded-md cursor-pointer border-none bg-bg-tertiary text-text-primary hover:opacity-90"
          >
            Retry
          </button>
        }
      >
        The version list did not load. Retry. If it fails again, check the server log.
      </FileMessage>
    );
  }

  if (sorted.length === 0) {
    return (
      <div className="text-text-tertiary text-sm py-12 text-center">
        No files available. Publish a version to see the file manifest.
      </div>
    );
  }

  const heldNote =
    selected && selected.gate_state === "needs_review"
      ? `v${selected.version} is held for review. Machines cannot read it until a reviewer releases it.`
      : selected && selected.gate_state === "rejected"
        ? `v${selected.version} was rejected. Machines cannot read it.`
        : null;

  return (
    <div className="max-w-[1200px]">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 mb-5">
        <label htmlFor="file-version" className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary">
          Version
        </label>
        <select
          id="file-version"
          value={selectedVersion ?? ""}
          onChange={(event) => {
            const next = sorted.find((v) => v.version === Number(event.target.value));
            if (!next) return;
            const keep = next.file_manifest?.some((f) => f.path === path);
            go(keep ? path : defaultFile(next), next.version);
          }}
          className="h-8 max-w-full px-2 font-mono text-xs rounded-md cursor-pointer bg-bg-secondary border border-border text-text-primary focus-visible:outline-none focus-visible:border-border-active focus-visible:ring-[3px] focus-visible:ring-border-active/50"
        >
          {selectedVersion == null && <option value="">Select a version</option>}
          {sorted.map((v) => (
            <option key={v.version} value={v.version}>
              v{v.version} · {GATE_LABELS[v.gate_state] ?? v.gate_state ?? "released"}
              {v.version === latestVersion ? " · current" : ""}
            </option>
          ))}
        </select>
        {heldNote && <span className="text-xs text-warning">{heldNote}</span>}
      </div>

      <div className="flex flex-col lg:flex-row gap-6 min-h-[480px]">
        {manifest.length > 0 && <FileTree files={manifest} activeFile={path ?? ""} onSelect={(file) => go(file, selectedVersion)} />}
        {!selected ? (
          <FileMessage>v{link.version} does not exist. Select a version above.</FileMessage>
        ) : !path ? (
          <FileMessage>v{selected.version} has no file manifest. Select another version above.</FileMessage>
        ) : !entry ? (
          <FileMessage>{path} is not in v{selected.version}. Select a file from the tree.</FileMessage>
        ) : (
          <FileViewer
            key={`${selected.version}:${entry.path}`}
            skillName={skillName}
            version={selected.version}
            path={entry.path}
            size={entry.size}
            line={link.line}
            baseHref={`${location.pathname}${buildFileSearch({ file: entry.path, version: selected.version })}`}
            onSelectLine={selectLine}
            onClearLine={clearLine}
          />
        )}
      </div>
    </div>
  );
}
