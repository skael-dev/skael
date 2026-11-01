export const PREVIEW_LIMIT = 512 * 1024;
const BINARY_SNIFF_BYTES = 8 * 1024;

export type FileContent =
  | { kind: "text"; text: string }
  | { kind: "binary"; size: number };

export class FileFetchError extends Error {
  status: number;

  constructor(status: number) {
    super(`file request failed with status ${status}`);
    this.status = status;
  }
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes}b`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}kb`;
  return `${(bytes / (1024 * 1024)).toFixed(1)}mb`;
}

export function fileUrl(skillName: string, version: number, path: string): string {
  const encodedPath = path.split("/").map(encodeURIComponent).join("/");
  return `/api/skills/${encodeURIComponent(skillName)}/versions/${version}/files/${encodedPath}`;
}

export function isMarkdown(path: string): boolean {
  return /\.(md|markdown)$/i.test(path);
}

export function stripFrontmatter(markdown: string): string {
  return markdown.replace(/^---\r?\n[\s\S]*?\r?\n---[ \t]*(\r?\n|$)/, "");
}

export async function fetchFileContent(
  skillName: string,
  version: number,
  path: string,
): Promise<FileContent> {
  const res = await fetch(fileUrl(skillName, version, path), { credentials: "include" });
  if (!res.ok) throw new FileFetchError(res.status);

  const bytes = new Uint8Array(await res.arrayBuffer());
  if (bytes.subarray(0, BINARY_SNIFF_BYTES).includes(0)) {
    return { kind: "binary", size: bytes.length };
  }
  return { kind: "text", text: new TextDecoder().decode(bytes) };
}
