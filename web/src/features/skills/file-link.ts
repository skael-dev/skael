export type FileLink = {
  file?: string;
  version?: number;
  line?: number;
};

export const GATE_LABELS: Record<string, string> = {
  released: "released",
  needs_review: "held for review",
  rejected: "rejected",
};

export function parseFileLink(search: string, hash: string): FileLink {
  const params = new URLSearchParams(search);
  const link: FileLink = {};

  const file = params.get("file");
  if (file) link.file = file;

  const version = params.get("v");
  if (version && /^[1-9]\d*$/.test(version)) link.version = Number(version);

  const line = /^#L([1-9]\d*)$/.exec(hash);
  if (line?.[1]) link.line = Number(line[1]);

  return link;
}

export function buildFileSearch({ file, version }: Pick<FileLink, "file" | "version">): string {
  const params = new URLSearchParams();
  if (file) params.set("file", file);
  if (version != null) params.set("v", String(version));
  // A slash is legal in a query, and a link with real slashes stays readable.
  return `?${params.toString().replace(/%2F/g, "/")}`;
}

// A Markdown link inside a skill, resolved against the file that holds it. It
// returns null for a link that leaves the skill or stays on the page. A path that
// climbs above the skill keeps its "..", so the viewer reports it as missing.
export function resolveSkillLink(fromFile: string, href: string): { file: string; hash: string } | null {
  if (href.startsWith("#") || href.startsWith("//") || /^[a-z][a-z0-9+.-]*:/i.test(href)) return null;

  const hashAt = href.indexOf("#");
  const hash = hashAt >= 0 ? href.slice(hashAt) : "";
  const pathPart = (hashAt >= 0 ? href.slice(0, hashAt) : href).split("?")[0] ?? "";
  if (!pathPart) return null;

  let target = pathPart;
  try {
    target = decodeURIComponent(pathPart);
  } catch {
    // A malformed escape stays as written, and the viewer reports it as missing.
  }

  const base = target.startsWith("/") ? [] : fromFile.split("/").slice(0, -1);
  const parts = [...base];
  for (const part of target.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === ".." && parts.length > 0 && parts[parts.length - 1] !== "..") parts.pop();
    else parts.push(part);
  }
  return parts.length > 0 ? { file: parts.join("/"), hash } : null;
}
