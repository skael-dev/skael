import { useMemo } from "react";
import { File, Copy, Check } from "lucide-react";
import { useState } from "react";
import { cn } from "@/lib/utils";

// ── Token types ──────────────────────────────────────────────────

type Token = {
  text: string;
  color: string;
  italic?: boolean;
  bold?: boolean;
};

function tokenizeLine(line: string, filename: string): Token[] {
  // Shell files
  if (filename.endsWith(".sh")) {
    if (line.trimStart().startsWith("#")) {
      return [{ text: line, color: "text-text-tertiary", italic: true }];
    }
    return [{ text: line, color: "text-text-primary" }];
  }

  // Markdown files
  if (filename.endsWith(".md")) {
    if (line.startsWith("# ") || line.startsWith("## ") || line.startsWith("### ")) {
      return [{ text: line, color: "text-text-primary", bold: true }];
    }
    if (line === "---") {
      return [{ text: line, color: "text-accent" }];
    }
    // YAML-style key: value in frontmatter
    const m = line.match(/^([a-z_]+):\s*(.*)$/i);
    if (m && m[1] && m[2] !== undefined) {
      return [
        { text: m[1], color: "text-accent" },
        { text: ": ", color: "text-text-secondary" },
        { text: m[2], color: "text-text-primary" },
      ];
    }
    return [{ text: line, color: "text-text-secondary" }];
  }

  // Default
  return [{ text: line, color: "text-text-primary" }];
}

// ── FileViewer ───────────────────────────────────────────────────

type FileViewerProps = {
  content: string;
  filename: string;
  skillName?: string;
};

export function FileViewer({ content, filename, skillName }: FileViewerProps) {
  const [copied, setCopied] = useState(false);
  const lines = useMemo(() => content.split("\n"), [content]);

  // Build breadcrumb from filename
  const breadcrumb = useMemo(() => {
    const parts = [];
    if (skillName) parts.push(skillName);
    parts.push(...filename.split("/"));
    return parts;
  }, [filename, skillName]);

  function handleCopy() {
    navigator.clipboard?.writeText(content);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }

  return (
    <div className="flex-1 flex flex-col bg-bg-secondary border border-border rounded-lg overflow-hidden min-w-0">
      {/* Header bar */}
      <div className="flex items-center gap-2.5 px-3.5 py-2.5 border-b border-border bg-bg-secondary">
        <File size={12} className="text-text-tertiary shrink-0" />
        <div className="flex items-center gap-1 font-mono text-[11px] text-text-tertiary flex-1 min-w-0 truncate">
          {breadcrumb.map((part, i) => (
            <span key={i} className="inline-flex items-center gap-1">
              {i > 0 && <span className="text-text-tertiary opacity-50">/</span>}
              <span className={i === breadcrumb.length - 1 ? "text-text-secondary" : ""}>
                {part}
              </span>
            </span>
          ))}
        </div>
        <button
          onClick={handleCopy}
          className={cn(
            "flex items-center gap-1.5 h-6 px-2 text-[11px] font-sans cursor-pointer rounded",
            "bg-bg-tertiary border border-border transition-colors duration-150",
            copied
              ? "text-accent border-accent"
              : "text-text-secondary hover:border-border-active"
          )}
        >
          {copied ? (
            <>
              <Check size={11} />
              Copied
            </>
          ) : (
            <>
              <Copy size={11} />
              Copy
            </>
          )}
        </button>
      </div>

      {/* Code content */}
      <div className="flex flex-1 overflow-auto font-mono text-[12.5px] leading-[1.7]">
        {/* Line numbers */}
        <div
          className="py-4 text-right select-none bg-bg-secondary shrink-0 border-r border-border"
          style={{ minWidth: 40 }}
        >
          {lines.map((_, i) => (
            <div
              key={i}
              className="px-2.5 text-[11px] text-text-tertiary opacity-60"
            >
              {i + 1}
            </div>
          ))}
        </div>

        {/* Code */}
        <div className="flex-1 py-4 px-4 overflow-x-auto">
          {lines.map((line, i) => {
            const tokens = tokenizeLine(line, filename);
            return (
              <div key={i} className="whitespace-pre" style={{ minHeight: "calc(12.5px * 1.7)" }}>
                {tokens.map((tok, j) => (
                  <span
                    key={j}
                    className={cn(
                      tok.color,
                      tok.italic && "italic",
                      tok.bold && "font-medium"
                    )}
                  >
                    {tok.text}
                  </span>
                ))}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

// ── Fallback for files without content ───────────────────────────

export function FileViewerFallback({ filename }: { filename: string }) {
  return (
    <div className="flex-1 flex flex-col items-center justify-center bg-bg-secondary border border-border rounded-lg min-h-[300px]">
      <File size={24} className="text-text-tertiary mb-3" />
      <div className="text-sm text-text-secondary mb-1">{filename}</div>
      <div className="text-xs text-text-tertiary text-center max-w-[280px]">
        File preview not available — download the archive to view this file.
      </div>
    </div>
  );
}
