import { describe, it, expect } from "vitest";
import { buildFileSearch, parseFileLink, resolveSkillLink } from "./file-link";

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

describe("resolveSkillLink", () => {
  it("resolves a link against the file that holds it", () => {
    expect(resolveSkillLink("docs/guide.md", "api.md")).toEqual({ file: "docs/api.md", hash: "" });
    expect(resolveSkillLink("docs/guide.md", "./sub/a%20b.md#L4")).toEqual({ file: "docs/sub/a b.md", hash: "#L4" });
    expect(resolveSkillLink("docs/guide.md", "../SKILL.md?x=1")).toEqual({ file: "SKILL.md", hash: "" });
    expect(resolveSkillLink("docs/guide.md", "/scripts/run.sh")).toEqual({ file: "scripts/run.sh", hash: "" });
  });

  it("keeps a path above the skill, so the viewer reports it as missing", () => {
    expect(resolveSkillLink("SKILL.md", "../secret")).toEqual({ file: "../secret", hash: "" });
  });

  it("leaves external and same-page links alone", () => {
    for (const href of ["https://example.com/a.md", "mailto:a@b.c", "//example.com/a", "#heading"]) {
      expect(resolveSkillLink("SKILL.md", href)).toBeNull();
    }
  });
});
