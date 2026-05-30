import { describe, it, expect } from "vitest";

describe("test infrastructure", () => {
  it("MSW intercepts API calls", async () => {
    const res = await fetch("/api/auth/me");
    const data = await res.json();
    expect(data.email).toBe("admin@test.com");
  });

  it("MSW returns fixture data for skills", async () => {
    const res = await fetch("/api/analytics/skills");
    const data = await res.json();
    expect(data.skills).toHaveLength(3);
    expect(data.total).toBe(3);
    expect(data.skills[0].name).toBe("code-review");
  });
});
