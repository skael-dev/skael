import { describe, it, expect } from "vitest";
import { buildFileSearch, parseFileLink } from "./file-link";

describe("parseFileLink", () => {
  it("reads the file, version and line", () => {
    expect(parseFileLink("?file=scripts/run.sh&v=2", "#L14")).toEqual({
      file: "scripts/run.sh",
      version: 2,
      line: 14,
    });
  });

  it("ignores a malformed version or line", () => {
    expect(parseFileLink("?file=a.md&v=two", "#L0")).toEqual({ file: "a.md" });
    expect(parseFileLink("?v=-1", "#heading")).toEqual({});
  });

  it("reads back what buildFileSearch writes", () => {
    const search = buildFileSearch({ file: "references/a b&c.md", version: 3 });
    expect(search).toContain("references/a");
    expect(parseFileLink(search, "")).toEqual({ file: "references/a b&c.md", version: 3 });
  });
});
