import { useState, useMemo } from "react";
import { File, Folder, FolderOpen } from "lucide-react";
import type { FileEntry } from "@/api/types.gen";
import { cn } from "@/lib/utils";

// ── Helpers ──────────────────────────────────────────────────────

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes}b`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}kb`;
  return `${(bytes / (1024 * 1024)).toFixed(1)}mb`;
}

type TreeNode = {
  name: string;
  path: string;
  isDir: boolean;
  size?: number;
  children: TreeNode[];
  depth: number;
};

function buildTree(files: FileEntry[]): TreeNode[] {
  const root: TreeNode[] = [];

  for (const file of files) {
    const parts = file.path.split("/");
    let current = root;

    for (let i = 0; i < parts.length; i++) {
      const part = parts[i];
      if (!part) continue;
      const isLast = i === parts.length - 1;
      const existingPath = parts.slice(0, i + 1).join("/");

      let existing = current.find((n) => n.name === part && n.isDir === !isLast);
      if (!existing) {
        existing = {
          name: part,
          path: isLast ? file.path : existingPath + "/",
          isDir: !isLast,
          size: isLast ? file.size : undefined,
          children: [],
          depth: i,
        };
        current.push(existing);
      }
      current = existing.children;
    }
  }

  // Sort: directories first, then alphabetically
  function sortNodes(nodes: TreeNode[]) {
    nodes.sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
    for (const node of nodes) {
      if (node.children.length > 0) sortNodes(node.children);
    }
  }

  sortNodes(root);
  return root;
}

// ── TreeItem ─────────────────────────────────────────────────────

function TreeItem({
  node,
  activeFile,
  onSelect,
  expandedDirs,
  toggleDir,
}: {
  node: TreeNode;
  activeFile: string;
  onSelect: (path: string) => void;
  expandedDirs: Set<string>;
  toggleDir: (path: string) => void;
}) {
  const isActive = !node.isDir && activeFile === node.path;
  const isExpanded = node.isDir && expandedDirs.has(node.path);

  return (
    <>
      <div
        onClick={() => {
          if (node.isDir) {
            toggleDir(node.path);
          } else {
            onSelect(node.path);
          }
        }}
        className={cn(
          "flex items-center gap-1.5 py-[5px] px-2 font-mono text-xs cursor-pointer transition-colors duration-100",
          isActive
            ? "text-text-primary bg-bg-tertiary border-l-2 border-accent"
            : "text-text-secondary border-l-2 border-transparent hover:bg-bg-secondary",
          node.isDir && "cursor-pointer"
        )}
        style={{ paddingLeft: node.depth * 14 + 8 }}
      >
        {node.isDir ? (
          isExpanded ? (
            <FolderOpen size={12} className="text-accent opacity-80 shrink-0" />
          ) : (
            <Folder size={12} className="text-accent opacity-80 shrink-0" />
          )
        ) : (
          <File size={12} className="text-text-tertiary opacity-80 shrink-0" />
        )}
        <span className="flex-1 truncate">
          {node.name}
          {node.isDir && "/"}
        </span>
        {!node.isDir && node.size != null && (
          <span className="text-[10px] text-text-tertiary shrink-0">
            {formatSize(node.size)}
          </span>
        )}
      </div>
      {node.isDir && isExpanded &&
        node.children.map((child) => (
          <TreeItem
            key={child.path}
            node={child}
            activeFile={activeFile}
            onSelect={onSelect}
            expandedDirs={expandedDirs}
            toggleDir={toggleDir}
          />
        ))}
    </>
  );
}

// ── FileTree ─────────────────────────────────────────────────────

type FileTreeProps = {
  files: FileEntry[];
  activeFile: string;
  onSelect: (path: string) => void;
};

export function FileTree({ files, activeFile, onSelect }: FileTreeProps) {
  const tree = useMemo(() => buildTree(files), [files]);
  const fileCount = files.length;

  // Start with all directories expanded
  const [expandedDirs, setExpandedDirs] = useState<Set<string>>(() => {
    const dirs = new Set<string>();
    function collectDirs(nodes: TreeNode[]) {
      for (const node of nodes) {
        if (node.isDir) {
          dirs.add(node.path);
          collectDirs(node.children);
        }
      }
    }
    collectDirs(tree);
    return dirs;
  });

  function toggleDir(path: string) {
    setExpandedDirs((prev) => {
      const next = new Set(prev);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
      }
      return next;
    });
  }

  return (
    <div className="w-[240px] shrink-0">
      <div className="flex items-center justify-between mb-3">
        <span className="text-[10px] uppercase tracking-[0.08em] text-text-tertiary">
          {fileCount} file{fileCount !== 1 ? "s" : ""}
        </span>
      </div>
      <div>
        {tree.map((node) => (
          <TreeItem
            key={node.path}
            node={node}
            activeFile={activeFile}
            onSelect={onSelect}
            expandedDirs={expandedDirs}
            toggleDir={toggleDir}
          />
        ))}
      </div>
    </div>
  );
}
