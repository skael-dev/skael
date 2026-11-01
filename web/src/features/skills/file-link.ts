export type FileLink = {
  file?: string;
  version?: number;
  line?: number;
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
