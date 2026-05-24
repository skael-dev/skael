import { useState, useCallback } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { cn } from "@/lib/utils";

type MarkdownRendererProps = {
  content: string;
  className?: string;
};

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // ignore
    }
  }, [text]);

  return (
    <button
      onClick={handleCopy}
      className={cn(
        "absolute top-2 right-2 px-2 py-0.5 text-[10px] font-mono rounded border cursor-pointer transition-colors duration-150",
        "bg-bg-tertiary border-border text-text-tertiary",
        "hover:border-border-active hover:text-text-secondary"
      )}
    >
      {copied ? "Copied" : "Copy"}
    </button>
  );
}

export function MarkdownRenderer({ content, className }: MarkdownRendererProps) {
  return (
    <div className={cn("text-text-primary text-sm leading-7", className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          // Headings
          h1: ({ children }) => (
            <h1 className="text-2xl font-semibold tracking-tight text-text-primary mt-0 mb-4">
              {children}
            </h1>
          ),
          h2: ({ children }) => (
            <h2 className="text-base font-medium tracking-tight text-text-primary mt-8 mb-3 pb-2 border-b border-border">
              {children}
            </h2>
          ),
          h3: ({ children }) => (
            <h3 className="text-[13px] font-medium text-text-primary mt-6 mb-2">
              {children}
            </h3>
          ),
          h4: ({ children }) => (
            <h4 className="text-[13px] font-medium text-text-secondary mt-4 mb-2">
              {children}
            </h4>
          ),

          // Paragraphs
          p: ({ children }) => (
            <p className="text-sm text-text-secondary mb-4 leading-7">{children}</p>
          ),

          // Code blocks
          pre: ({ children }) => {
            // Extract text content from children for copy button
            const extractText = (node: React.ReactNode): string => {
              if (typeof node === "string") return node;
              if (Array.isArray(node)) return node.map(extractText).join("");
              if (node && typeof node === "object" && "props" in (node as object)) {
                return extractText((node as React.ReactElement<{ children?: React.ReactNode }>).props.children);
              }
              return "";
            };
            const text = extractText(children);

            return (
              <div className="relative group mb-4">
                <pre className="bg-bg-secondary border border-border rounded-lg p-4 overflow-x-auto font-mono text-[12.5px] leading-7 text-text-secondary">
                  {children}
                </pre>
                <CopyButton text={text} />
              </div>
            );
          },

          // Inline code
          code: ({ children, className: cls }) => {
            // If inside a pre, don't apply inline styles (pre handles it)
            const isBlock = cls?.startsWith("language-");
            if (isBlock) {
              return <code className="font-mono text-[12.5px] text-text-secondary">{children}</code>;
            }
            return (
              <code className="font-mono text-[12px] px-1.5 py-0.5 rounded border border-border bg-bg-tertiary text-text-primary">
                {children}
              </code>
            );
          },

          // Lists
          ul: ({ children }) => (
            <ul className="text-sm text-text-secondary pl-5 mb-4 flex flex-col gap-1.5 list-disc">
              {children}
            </ul>
          ),
          ol: ({ children }) => (
            <ol className="text-sm text-text-secondary pl-5 mb-4 flex flex-col gap-1.5 list-decimal">
              {children}
            </ol>
          ),
          li: ({ children, className: cls }) => {
            // Task list items
            const isTask = cls?.includes("task-list-item");
            if (isTask) {
              return (
                <li className="flex items-start gap-2 list-none -ml-5">
                  {children}
                </li>
              );
            }
            return <li className="leading-7">{children}</li>;
          },

          // Task list checkboxes
          input: ({ type, checked }) => {
            if (type === "checkbox") {
              return (
                <span
                  className={cn(
                    "inline-flex items-center justify-center w-4 h-4 rounded border mt-1 shrink-0",
                    checked
                      ? "bg-accent border-accent"
                      : "bg-transparent border-border-active"
                  )}
                >
                  {checked && (
                    <svg
                      width="10"
                      height="10"
                      viewBox="0 0 10 10"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2"
                      className="text-bg-primary"
                    >
                      <polyline points="1.5,5 4,7.5 8.5,2.5" />
                    </svg>
                  )}
                </span>
              );
            }
            return <input type={type} checked={checked} readOnly />;
          },

          // Tables
          table: ({ children }) => (
            <div className="overflow-x-auto mb-4">
              <table className="w-full text-sm border border-border rounded-lg overflow-hidden">
                {children}
              </table>
            </div>
          ),
          thead: ({ children }) => (
            <thead className="bg-bg-secondary">{children}</thead>
          ),
          tbody: ({ children }) => <tbody>{children}</tbody>,
          tr: ({ children }) => (
            <tr className="border-b border-border last:border-0">{children}</tr>
          ),
          th: ({ children }) => (
            <th className="px-3 py-2 text-left text-[10px] uppercase tracking-widest text-text-tertiary font-medium">
              {children}
            </th>
          ),
          td: ({ children }) => (
            <td className="px-3 py-2 text-text-secondary">{children}</td>
          ),

          // Blockquotes
          blockquote: ({ children }) => (
            <blockquote className="border-l-2 border-accent-muted pl-4 mb-4 text-text-tertiary italic">
              {children}
            </blockquote>
          ),

          // Links
          a: ({ children, href }) => (
            <a
              href={href}
              target="_blank"
              rel="noopener noreferrer"
              className="text-accent hover:underline"
            >
              {children}
            </a>
          ),

          // Horizontal rule
          hr: () => <hr className="border-border my-6" />,

          // Strong / em
          strong: ({ children }) => (
            <strong className="font-semibold text-text-primary">{children}</strong>
          ),
          em: ({ children }) => (
            <em className="italic text-text-secondary">{children}</em>
          ),
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
