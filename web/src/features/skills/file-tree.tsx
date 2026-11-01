import { useMemo, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { File, Folder, FolderOpen } from "lucide-react";
import type { FileEntry } from "@/api/types.gen";
import { formatSize } from "@/features/skills/file-content";
import { cn } from "@/lib/utils";

type TreeNode = {
  name: string;
  path: string;
  isDir: boolean;
  size?: number;
  children: TreeNode[];
};

type VisibleNode = {
  node: TreeNode;
  depth: number;
  parent?: string;
  position: number;
  siblings: number;
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

      let existing = current.find((n) => n.name === part && n.isDir === !isLast);
      if (!existing) {
        existing = {
          name: part,
          path: isLast ? file.path : parts.slice(0, i + 1).join("/") + "/",
          isDir: !isLast,
          size: isLast ? file.size : undefined,
          children: [],
        };
        current.push(existing);
      }
      current = existing.children;
    }
  }

  function sortNodes(nodes: TreeNode[]) {
    nodes.sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
    nodes.forEach((node) => sortNodes(node.children));
  }

  sortNodes(root);
  return root;
}

function flatten(nodes: TreeNode[], collapsed: Set<string>, depth = 0, parent?: string): VisibleNode[] {
  return nodes.flatMap((node, i) => [
    { node, depth, parent, position: i + 1, siblings: nodes.length },
    ...(node.isDir && !collapsed.has(node.path) ? flatten(node.children, collapsed, depth + 1, node.path) : []),
  ]);
}

type FileTreeProps = {
  files: FileEntry[];
  activeFile: string;
  onSelect: (path: string) => void;
};

export function FileTree({ files, activeFile, onSelect }: FileTreeProps) {
  const tree = useMemo(() => buildTree(files), [files]);
  // Tracks closed folders, so a folder that first appears in another version
  // starts open.
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const [focused, setFocused] = useState<string | null>(null);
  const items = useRef(new Map<string, HTMLDivElement>());

  const visible = useMemo(() => flatten(tree, collapsed), [tree, collapsed]);

  const tabStop =
    visible.find((v) => v.node.path === focused)?.node.path ??
    visible.find((v) => v.node.path === activeFile)?.node.path ??
    visible[0]?.node.path;

  function moveTo(path: string | undefined) {
    if (!path) return;
    setFocused(path);
    items.current.get(path)?.focus();
  }

  function setOpen(path: string, open: boolean) {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (open) next.delete(path);
      else next.add(path);
      return next;
    });
  }

  function activate(node: TreeNode) {
    setFocused(node.path);
    if (node.isDir) setOpen(node.path, collapsed.has(node.path));
    else onSelect(node.path);
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>, index: number) {
    const item = visible[index];
    if (!item) return;
    const { node, parent } = item;
    const open = node.isDir && !collapsed.has(node.path);

    switch (event.key) {
      case "ArrowDown":
        moveTo(visible[index + 1]?.node.path);
        break;
      case "ArrowUp":
        moveTo(visible[index - 1]?.node.path);
        break;
      case "ArrowRight":
        if (node.isDir && !open) setOpen(node.path, true);
        else if (open) moveTo(visible[index + 1]?.node.path);
        break;
      case "ArrowLeft":
        if (open) setOpen(node.path, false);
        else moveTo(parent);
        break;
      case "Home":
        moveTo(visible[0]?.node.path);
        break;
      case "End":
        moveTo(visible[visible.length - 1]?.node.path);
        break;
      case "Enter":
      case " ":
        activate(node);
        break;
      default:
        return;
    }
    event.preventDefault();
    event.stopPropagation();
  }

  return (
    <div className="w-full lg:w-[240px] shrink-0">
      <div className="mb-3 text-[10px] uppercase tracking-[0.08em] text-text-tertiary">
        {files.length} file{files.length !== 1 ? "s" : ""}
      </div>
      {/* Stacked above the viewer, a full tree would push the file off a phone screen. */}
      <div role="tree" aria-label="Skill files" className="max-h-[240px] overflow-y-auto lg:max-h-none lg:overflow-visible">
        {visible.map(({ node, depth, position, siblings }, index) => {
          const isActive = !node.isDir && activeFile === node.path;
          const isOpen = node.isDir && !collapsed.has(node.path);
          return (
            <div
              key={node.path}
              ref={(el) => {
                if (el) items.current.set(node.path, el);
                else items.current.delete(node.path);
              }}
              role="treeitem"
              aria-level={depth + 1}
              aria-setsize={siblings}
              aria-posinset={position}
              aria-expanded={node.isDir ? isOpen : undefined}
              aria-selected={isActive}
              tabIndex={node.path === tabStop ? 0 : -1}
              onClick={() => activate(node)}
              onKeyDown={(event) => handleKeyDown(event, index)}
              onFocus={() => setFocused(node.path)}
              className={cn(
                "flex items-center gap-1.5 py-2 lg:py-[5px] pr-2 rounded font-mono text-xs cursor-pointer transition-colors duration-100",
                "focus-visible:outline-none focus-visible:bg-bg-secondary focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-border-active",
                isActive ? "text-text-primary bg-bg-tertiary" : "text-text-secondary hover:bg-bg-secondary",
              )}
              style={{ paddingLeft: depth * 14 + 8 }}
            >
              {node.isDir ? (
                isOpen ? (
                  <FolderOpen size={12} className="text-accent opacity-80 shrink-0" />
                ) : (
                  <Folder size={12} className="text-accent opacity-80 shrink-0" />
                )
              ) : (
                <File size={12} className={cn("shrink-0", isActive ? "text-accent" : "text-text-tertiary opacity-80")} />
              )}
              <span className="flex-1 truncate">
                {node.name}
                {node.isDir && "/"}
              </span>
              {node.size != null && (
                <span className="text-[10px] text-text-tertiary shrink-0">{formatSize(node.size)}</span>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
