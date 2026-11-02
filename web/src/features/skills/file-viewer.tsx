import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent, MouseEvent, ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Check, Copy, Download, File, Link2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { buildFileSearch, resolveSkillLink } from "@/features/skills/file-link";
import { MarkdownRenderer } from "@/features/skills/markdown-renderer";
import {
  FileFetchError,
  PREVIEW_LIMIT,
  fetchFileContent,
  fileUrl,
  formatSize,
  isMarkdown,
  stripFrontmatter,
} from "@/features/skills/file-content";
import { highlightLines } from "@/features/skills/file-highlight";
import type { HighlightToken } from "@/features/skills/file-highlight";

// ── Shared panels ────────────────────────────────────────────────

const ACTION_CLASS =
  "flex items-center gap-1.5 h-6 px-2 text-[11px] font-sans cursor-pointer rounded no-underline bg-bg-tertiary border border-border text-text-secondary transition-colors duration-150 hover:border-border-active focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-active";

export function FileMessage({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <div
      role="alert"
      className="flex-1 min-w-0 flex flex-col items-center justify-center gap-3 p-6 bg-bg-secondary border border-border rounded-lg min-h-[300px]"
    >
      <p className="m-0 text-sm text-text-secondary text-center max-w-[420px]">{children}</p>
      {action}
    </div>
  );
}

export function FileViewerSkeleton() {
  return (
    <div
      role="status"
      aria-label="Loading file"
      className="flex-1 min-w-0 p-4 bg-bg-secondary border border-border rounded-lg min-h-[300px] flex flex-col gap-2.5"
    >
      {[92, 68, 80, 44, 72, 56, 86, 38, 64].map((width, i) => (
        <div
          key={i}
          className="h-3 rounded bg-bg-tertiary motion-safe:animate-pulse"
          style={{ width: `${width}%` }}
        />
      ))}
    </div>
  );
}

function fetchErrorMessage(error: unknown, path: string, version: number): string {
  const status = error instanceof FileFetchError ? error.status : 0;
  if (status === 404) return `${path} is not available in v${version}. Select another file or version.`;
  if (status === 401) return "Your session has ended. Sign in again, then reload this page.";
  if (status === 403) return "Your account cannot read this file. Ask an owner for access.";
  return "The file did not load. Retry. If it fails again, check the server log.";
}

function DownloadPanel({ detail, path, href }: { detail: string; path: string; href: string }) {
  return (
    <div className="flex-1 flex flex-col items-center gap-3 px-6 py-12">
      <File size={24} className="text-text-tertiary" />
      <div className="font-mono text-[13px] text-text-primary break-all text-center">{path}</div>
      <div className="font-mono text-xs text-text-secondary text-center">{detail}</div>
      <a
        href={href}
        download
        className="flex items-center gap-2 h-9 px-4 text-sm font-sans rounded-md no-underline bg-text-primary text-bg-primary hover:opacity-90 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-border-active/50"
      >
        <Download size={14} />
        Download
      </a>
    </div>
  );
}

// ── Source view ──────────────────────────────────────────────────

type CodeLineProps = {
  line: number;
  tokens: HighlightToken[] | undefined;
  plain: string;
  selected: boolean;
  tabbable: boolean;
  href: string;
  onClick: (event: MouseEvent<HTMLAnchorElement>, line: number) => void;
};

function moveLineFocus(event: KeyboardEvent<HTMLAnchorElement>) {
  const row = event.currentTarget.parentElement;
  const target =
    event.key === "ArrowDown" ? row?.nextElementSibling : event.key === "ArrowUp" ? row?.previousElementSibling : null;
  const link = target?.querySelector("a");
  if (link) {
    event.preventDefault();
    link.focus();
  }
}

const CodeLine = memo(function CodeLine({ line, tokens, plain, selected, tabbable, href, onClick }: CodeLineProps) {
  return (
    <div data-line={line} className={cn("flex w-max min-w-full", selected && "bg-accent-surface")}>
      <a
        href={href}
        tabIndex={tabbable ? 0 : -1}
        aria-label={`Line ${line}`}
        aria-current={selected ? "location" : undefined}
        onClick={(event) => onClick(event, line)}
        onKeyDown={moveLineFocus}
        className={cn(
          "sticky left-0 shrink-0 w-12 pr-2.5 text-right text-[11px] leading-[inherit] no-underline select-none border-r border-border",
          "hover:text-text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-border-active",
          selected ? "bg-bg-tertiary text-accent" : "bg-bg-secondary text-text-secondary",
        )}
      >
        {line}
      </a>
      <code className="px-4 whitespace-pre text-text-primary">
        {tokens
          ? tokens.map((token, i) => (
              <span key={i} className={token.className}>
                {token.text}
              </span>
            ))
          : plain}
        {/* An empty line still needs its height. */}
        {!plain && "​"}
      </code>
    </div>
  );
});

function SourceView({
  text,
  path,
  baseHref,
  selectedLine,
  onSelectLine,
}: {
  text: string;
  path: string;
  baseHref: string;
  selectedLine?: number;
  onSelectLine: (line: number) => void;
}) {
  const lines = useMemo(() => {
    const split = text.split(/\r?\n/);
    if (split.length > 1 && split[split.length - 1] === "") split.pop();
    return split;
  }, [text]);

  const [highlighted, setHighlighted] = useState<HighlightToken[][] | null>(null);
  useEffect(() => {
    let current = true;
    setHighlighted(null);
    // A failed chunk load leaves the plain text, which is still correct.
    highlightLines(lines.join("\n"), path)
      .then((result) => {
        if (current) setHighlighted(result);
      })
      .catch(() => undefined);
    return () => {
      current = false;
    };
  }, [lines, path]);

  const containerRef = useRef<HTMLDivElement>(null);
  const clickedLine = useRef(false);

  const handleLineClick = useCallback(
    (event: MouseEvent<HTMLAnchorElement>, line: number) => {
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      event.preventDefault();
      clickedLine.current = true;
      onSelectLine(line);
    },
    [onSelectLine],
  );

  // A line the reader just clicked is already on screen, so only a line from
  // the URL scrolls.
  useEffect(() => {
    if (clickedLine.current) {
      clickedLine.current = false;
      return;
    }
    if (!selectedLine) return;
    containerRef.current
      ?.querySelector(`[data-line="${selectedLine}"]`)
      ?.scrollIntoView({ block: "center", inline: "nearest" });
  }, [selectedLine, lines]);

  const hasSelection = selectedLine != null && selectedLine <= lines.length;

  return (
    <div ref={containerRef} className="flex-1 overflow-x-auto py-3 font-mono text-[12.5px] leading-[21px]">
      {lines.map((plain, i) => {
        const line = i + 1;
        const selected = line === selectedLine;
        return (
          <CodeLine
            key={line}
            line={line}
            plain={plain}
            tokens={highlighted?.[i]}
            selected={selected}
            tabbable={hasSelection ? selected : line === 1}
            href={`${baseHref}#L${line}`}
            onClick={handleLineClick}
          />
        );
      })}
    </div>
  );
}

// ── FileViewer ───────────────────────────────────────────────────

type ViewMode = "rendered" | "source";

type FileViewerProps = {
  skillName: string;
  version: number;
  path: string;
  size: number;
  line?: number;
  /** The file's address with no line, as a same-origin href. */
  baseHref: string;
  onSelectLine: (line: number) => void;
  onClearLine: () => void;
};

export function FileViewer({
  skillName,
  version,
  path,
  size,
  line,
  baseHref,
  onSelectLine,
  onClearLine,
}: FileViewerProps) {
  const oversized = size > PREVIEW_LIMIT;
  const href = fileUrl(skillName, version, path);
  const markdown = isMarkdown(path);

  const query = useQuery({
    queryKey: ["skill-file", skillName, version, path],
    queryFn: () => fetchFileContent(skillName, version, path),
    enabled: !oversized,
    retry: false,
    // A version's archive is immutable.
    staleTime: Infinity,
  });

  const [mode, setMode] = useState<ViewMode>("rendered");
  const view: ViewMode = !markdown || line != null ? "source" : mode;
  const text = query.data?.kind === "text" ? query.data.text : null;

  const [copied, setCopied] = useState<"file" | "link" | null>(null);

  async function copy(kind: "file" | "link", value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(kind);
      setTimeout(() => setCopied(null), 1500);
    } catch {
      toast.error("The browser blocked the clipboard. Copy the address from the location bar.");
    }
  }

  function chooseMode(next: ViewMode) {
    setMode(next);
    if (next === "rendered" && line != null) onClearLine();
  }

  const crumbs = [skillName, ...path.split("/")];

  let body: ReactNode;
  if (oversized) {
    body = (
      <DownloadPanel
        path={path}
        href={href}
        detail={`${formatSize(size)} · over the ${formatSize(PREVIEW_LIMIT)} preview limit`}
      />
    );
  } else if (query.isPending) {
    return <FileViewerSkeleton />;
  } else if (query.isError) {
    return (
      <FileMessage
        action={
          <button type="button" onClick={() => query.refetch()} className={ACTION_CLASS}>
            Retry
          </button>
        }
      >
        {fetchErrorMessage(query.error, path, version)}
      </FileMessage>
    );
  } else if (query.data.kind === "binary") {
    body = <DownloadPanel path={path} href={href} detail={`${formatSize(query.data.size)} · binary file`} />;
  } else if (view === "rendered") {
    body = (
      <div className="p-4 sm:p-6 max-w-[760px] min-w-0">
        <MarkdownRenderer
          content={stripFrontmatter(query.data.text)}
          resolveLink={(link) => {
            const target = resolveSkillLink(path, link);
            return target && { search: buildFileSearch({ file: target.file, version }), hash: target.hash };
          }}
        />
      </div>
    );
  } else {
    body = (
      <SourceView
        text={query.data.text}
        path={path}
        baseHref={baseHref}
        selectedLine={line}
        onSelectLine={onSelectLine}
      />
    );
  }

  return (
    <div className="flex-1 flex flex-col bg-bg-secondary border border-border rounded-lg overflow-hidden min-w-0">
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-2 px-3.5 py-2.5 border-b border-border">
        <div className="flex items-center gap-2 basis-full sm:basis-0 sm:flex-1 min-w-0">
          <File size={12} className="text-text-tertiary shrink-0" />
          <div className="font-mono text-[11px] text-text-secondary min-w-0 truncate" title={path}>
            {crumbs.map((part, i) => (
              <span key={i}>
                {i > 0 && <span className="opacity-50 mx-1">/</span>}
                <span className={i === crumbs.length - 1 ? "text-text-primary" : ""}>{part}</span>
              </span>
            ))}
          </div>
        </div>

        {markdown && text != null && (
          <div role="tablist" aria-label="Markdown view" className="flex rounded border border-border overflow-hidden">
            {(["rendered", "source"] as const).map((option) => (
              <button
                key={option}
                type="button"
                role="tab"
                aria-selected={view === option}
                onClick={() => chooseMode(option)}
                className={cn(
                  "h-6 px-2 text-[11px] font-sans capitalize cursor-pointer border-none focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-border-active",
                  view === option ? "bg-bg-tertiary text-text-primary" : "bg-transparent text-text-secondary hover:text-text-primary",
                )}
              >
                {option}
              </button>
            ))}
          </div>
        )}

        <button
          type="button"
          onClick={() => copy("link", `${window.location.origin}${baseHref}${line != null ? `#L${line}` : ""}`)}
          className={cn(ACTION_CLASS, copied === "link" && "text-accent border-accent")}
        >
          {copied === "link" ? <Check size={11} /> : <Link2 size={11} />}
          {copied === "link" ? "Copied" : "Copy link"}
        </button>
        {text != null && (
          <>
            <button
              type="button"
              onClick={() => copy("file", text)}
              className={cn(ACTION_CLASS, copied === "file" && "text-accent border-accent")}
            >
              {copied === "file" ? <Check size={11} /> : <Copy size={11} />}
              {copied === "file" ? "Copied" : "Copy"}
            </button>
            <a href={href} download className={ACTION_CLASS}>
              <Download size={11} />
              Download
            </a>
          </>
        )}
      </div>
      {body}
    </div>
  );
}
