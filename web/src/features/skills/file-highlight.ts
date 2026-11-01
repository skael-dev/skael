import type { LanguageFn } from "highlight.js";

export type HighlightToken = { text: string; className?: string };

type LanguageLoader = () => Promise<{ default: LanguageFn }>;

const LANGUAGE_LOADERS: Record<string, LanguageLoader> = {
  bash: () => import("highlight.js/lib/languages/bash"),
  python: () => import("highlight.js/lib/languages/python"),
  javascript: () => import("highlight.js/lib/languages/javascript"),
  typescript: () => import("highlight.js/lib/languages/typescript"),
  json: () => import("highlight.js/lib/languages/json"),
  yaml: () => import("highlight.js/lib/languages/yaml"),
  markdown: () => import("highlight.js/lib/languages/markdown"),
  go: () => import("highlight.js/lib/languages/go"),
  // highlight.js has no separate TOML grammar. Its ini grammar covers TOML.
  ini: () => import("highlight.js/lib/languages/ini"),
};

const EXTENSION_LANGUAGES: Record<string, string> = {
  sh: "bash",
  bash: "bash",
  zsh: "bash",
  py: "python",
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  jsx: "javascript",
  ts: "typescript",
  mts: "typescript",
  cts: "typescript",
  tsx: "typescript",
  json: "json",
  yaml: "yaml",
  yml: "yaml",
  md: "markdown",
  markdown: "markdown",
  go: "go",
  toml: "ini",
};

const SCOPE_CLASSES: Record<string, string> = {
  comment: "text-text-secondary italic",
  quote: "text-text-secondary italic",
  meta: "text-text-secondary",
  keyword: "text-accent",
  "selector-tag": "text-accent",
  doctag: "text-accent",
  bullet: "text-accent",
  built_in: "text-warning",
  type: "text-warning",
  string: "text-info",
  regexp: "text-info",
  symbol: "text-info",
  code: "text-info",
  link: "text-info underline",
  number: "text-chart-4",
  literal: "text-chart-4",
  title: "font-medium",
  attr: "font-medium",
  section: "font-semibold",
  strong: "font-semibold",
  emphasis: "italic",
};

export function languageForPath(path: string): string | undefined {
  const extension = path.split("/").pop()?.split(".").pop()?.toLowerCase();
  return extension ? EXTENSION_LANGUAGES[extension] : undefined;
}

function scopeClass(el: Element): string | undefined {
  for (const name of Array.from(el.classList)) {
    if (name.startsWith("hljs-")) {
      const mapped = SCOPE_CLASSES[name.slice("hljs-".length)];
      if (mapped) return mapped;
    }
  }
  return undefined;
}

// A highlight span can cross line breaks, in a block comment for example, so
// the markup is walked into per-line tokens. React then renders them as text,
// which keeps untrusted file content out of dangerouslySetInnerHTML.
export function tokensByLine(html: string): HighlightToken[][] {
  const template = document.createElement("template");
  template.innerHTML = html;

  const lines: HighlightToken[][] = [[]];
  const walk = (node: Node, inherited: string | undefined) => {
    for (const child of Array.from(node.childNodes)) {
      if (child.nodeType === Node.TEXT_NODE) {
        (child.textContent ?? "").split("\n").forEach((text, i) => {
          if (i > 0) lines.push([]);
          if (text) lines[lines.length - 1]!.push({ text, className: inherited });
        });
      } else if (child instanceof Element) {
        walk(child, scopeClass(child) ?? inherited);
      }
    }
  };
  walk(template.content, undefined);
  return lines;
}

/** Returns null for a language with no grammar, so the caller keeps plain text. */
export async function highlightLines(code: string, path: string): Promise<HighlightToken[][] | null> {
  const language = languageForPath(path);
  const loader = language ? LANGUAGE_LOADERS[language] : undefined;
  if (!language || !loader) return null;

  const [{ default: hljs }, { default: grammar }] = await Promise.all([
    import("highlight.js/lib/core"),
    loader(),
  ]);
  if (!hljs.getLanguage(language)) hljs.registerLanguage(language, grammar);
  return tokensByLine(hljs.highlight(code, { language, ignoreIllegals: true }).value);
}
