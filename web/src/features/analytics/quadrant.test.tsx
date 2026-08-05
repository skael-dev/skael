import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthProvider } from "@/app/auth-provider";
import { server } from "@/test/handlers";
import { Quadrant, median } from "./quadrant";
import type { SkillAnalytics, QualitySummary } from "@/api/types.gen";

function withQuery(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={qc}>
      <AuthProvider>{ui}</AuthProvider>
    </QueryClientProvider>
  );
}

function q(score: number): QualitySummary {
  return {
    version: 1,
    headline_score: score,
    verified: true,
    panel_complete: true,
    scored_at: "2026-08-01T00:00:00Z",
  };
}

function mockAnalyticsSkills(skills: Partial<SkillAnalytics>[]) {
  server.use(
    http.get("/api/analytics/skills", () => {
      return HttpResponse.json({ skills, total: skills.length });
    }),
  );
}

describe("median", () => {
  it("returns the middle value for an odd count", () => {
    expect(median([10, 30, 20])).toBe(20);
  });

  it("averages the two central values for an even count", () => {
    expect(median([10, 20, 30, 40])).toBe(25);
  });

  it("returns the single value for one element, never NaN", () => {
    expect(median([42])).toBe(42);
  });

  it("returns that value when every element is equal", () => {
    expect(median([7, 7, 7, 7])).toBe(7);
  });
});

describe("Quadrant", () => {
  it("calls out high-activation low-score skills, worst first", async () => {
    // {450, 500, 600} has a true median of 500: bad-and-busy (500) and
    // worse-and-busy (600) are both >= 500, so both qualify on the
    // activation axis; good-and-busy (450) is below the median but is
    // excluded anyway because its score (85) clears the y=50 cut. No
    // fixture value sits exactly on the median other than bad-and-busy
    // itself, which is deliberately >= (inclusive), so the boundary case is
    // exercised unambiguously.
    mockAnalyticsSkills([
      { name: "bad-and-busy", activations: 500, latest_version: 1, quality: q(20) },
      { name: "worse-and-busy", activations: 600, latest_version: 1, quality: q(10) },
      { name: "good-and-busy", activations: 450, latest_version: 1, quality: q(85) },
    ]);
    render(withQuery(<Quadrant />));
    const rows = await screen.findAllByTestId("attention-row");
    expect(rows.map((r) => r.textContent)).toEqual([
      expect.stringContaining("worse-and-busy"),
      expect.stringContaining("bad-and-busy"),
    ]);
  });

  it("never plots an unscored skill at zero", async () => {
    mockAnalyticsSkills([
      { name: "unscored-busy", activations: 900, latest_version: 1 },
      { name: "scored", activations: 10, latest_version: 1, quality: q(70) },
    ]);
    const { container } = render(withQuery(<Quadrant />));
    await screen.findByText("scored");
    expect(container.querySelectorAll("[data-plotted]")).toHaveLength(1);
    expect(screen.getByText(/1 skill not scored/i)).toBeInTheDocument();
    expect(screen.getByText("unscored-busy")).toBeInTheDocument();
  });

  it("says so when nothing has been scored yet", async () => {
    mockAnalyticsSkills([{ name: "a", activations: 5, latest_version: 1 }]);
    render(withQuery(<Quadrant />));
    expect(await screen.findByText(/no skills scored yet/i)).toBeInTheDocument();
  });

  it("excludes incomplete-panel scores from the attention list", async () => {
    mockAnalyticsSkills([
      { name: "incomplete", activations: 900, latest_version: 1, quality: { ...q(5), panel_complete: false } },
    ]);
    render(withQuery(<Quadrant />));
    // An incomplete panel is not a low score, so it must not be presented as
    // the worst skill in the registry.
    expect(screen.queryByTestId("attention-row")).not.toBeInTheDocument();
    expect(await screen.findByText(/1 incomplete/i)).toBeInTheDocument();
  });

  it("pages through the full population instead of stopping at the first 100", async () => {
    // 150 skills, only fitting in two 100-skill pages. If the component
    // still used a single limit:100 request, only the first page (offset 0)
    // would be seen and the median would be computed over a biased subset.
    const skills: Partial<SkillAnalytics>[] = [];
    for (let i = 0; i < 150; i++) {
      skills.push({ name: `skill-${i}`, activations: i, latest_version: 1, quality: q(70) });
    }
    server.use(
      http.get("/api/analytics/skills", ({ request }) => {
        const url = new URL(request.url);
        const offset = Number(url.searchParams.get("offset") ?? "0");
        const limit = Number(url.searchParams.get("limit") ?? "50");
        return HttpResponse.json({ skills: skills.slice(offset, offset + limit), total: skills.length });
      }),
    );
    const { container } = render(withQuery(<Quadrant />));
    await screen.findByText("skill-0");
    expect(container.querySelectorAll("[data-plotted]")).toHaveLength(150);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows a truncation notice when the page cap is hit instead of silently reporting a partial median", async () => {
    // A `total` that never converges (larger than any number of pages could
    // satisfy) forces the loop to hit MAX_PAGES rather than looping forever.
    server.use(
      http.get("/api/analytics/skills", ({ request }) => {
        const url = new URL(request.url);
        const offset = Number(url.searchParams.get("offset") ?? "0");
        const page: Partial<SkillAnalytics>[] = [
          { name: `skill-${offset}`, activations: offset, latest_version: 1, quality: q(70) },
        ];
        return HttpResponse.json({ skills: page, total: 1_000_000 });
      }),
    );
    render(withQuery(<Quadrant />));
    expect(await screen.findByRole("alert")).toHaveTextContent(/partial/i);
  });
});
