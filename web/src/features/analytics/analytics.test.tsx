import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/handlers";
import { renderWithProviders, screen, waitFor, userEvent, within } from "@/test/render";
import { Analytics } from "./analytics";
import { AnalyticsTable } from "./analytics-table";
import type { SkillAnalytics } from "@/api/types.gen";

// Minimal SkillAnalytics rows for AnalyticsTable-only tests (client-side
// sort), independent of the mocked /api/analytics/skills fixtures.
function renderTable(skills: Array<Partial<SkillAnalytics> & { name: string }>) {
  const full: SkillAnalytics[] = skills.map((s) => ({
    description: "",
    author: "",
    spec_compliance: "",
    activations: 0,
    unique_devs: 0,
    last_triggered: null,
    latest_version: 1,
    reviewed_at: null,
    security_status: "clean",
    tags: [],
    updated_at: "2026-08-01T00:00:00Z",
    ...s,
  }));
  return renderWithProviders(<AnalyticsTable skills={full} />);
}

function rowNames(): string[] {
  const rows = screen.getAllByRole("row").slice(1); // drop header row
  return rows.map((row) => within(row).getByRole("link").textContent ?? "");
}

describe("Analytics", () => {
  it("KPI tiles render correct numbers", async () => {
    renderWithProviders(<Analytics />);

    // KpiStrip renders: total_skills (3), active_skills (2), total_activations (468), security status
    // Wait for data to load
    expect(await screen.findByText("3")).toBeInTheDocument();
    expect(screen.getByText("468")).toBeInTheDocument();

    // Labels from KpiStrip
    expect(screen.getByText("Total skills")).toBeInTheDocument();
    expect(screen.getByText("Total activations")).toBeInTheDocument();
  });

  it("table renders skill rows", async () => {
    renderWithProviders(<Analytics />);

    // The AnalyticsTable renders skill names as links
    // mockSkillAnalytics has: code-review (312), test-writer (156), doc-generator (0)
    expect(await screen.findByText("code-review")).toBeInTheDocument();
    expect(screen.getByText("test-writer")).toBeInTheDocument();
    expect(screen.getByText("doc-generator")).toBeInTheDocument();

    // Check that activations number for code-review is shown (312)
    expect(screen.getByText("312")).toBeInTheDocument();
  });

  it("dead skills get muted styling (opacity class)", async () => {
    renderWithProviders(<Analytics />);

    // Wait for data
    await screen.findByText("code-review");

    // doc-generator has 0 activations so it should have "opacity-50" class on its row
    const docGenLink = screen.getByText("doc-generator");
    // The link is inside a TableCell, which is inside a TableRow (tr)
    const row = docGenLink.closest("tr");
    expect(row).not.toBeNull();
    expect(row!.className).toContain("opacity-50");
  });

  it("time period buttons exist", async () => {
    renderWithProviders(<Analytics />);

    // The period toggle renders Button components with text "7d", "30d", "90d"
    expect(await screen.findByRole("button", { name: "7d" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "30d" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "90d" })).toBeInTheDocument();
  });

  it("shows an error state when analytics requests fail", async () => {
    server.use(
      http.get("/api/analytics/overview", () =>
        HttpResponse.json({ error: "boom" }, { status: 500 }),
      ),
      http.get("/api/analytics/skills", () =>
        HttpResponse.json({ error: "boom" }, { status: 500 }),
      ),
    );
    renderWithProviders(<Analytics />);
    expect(
      await screen.findByText(/couldn't load analytics/i),
    ).toBeInTheDocument();
  });

  it("sorts unscored skills last in both directions", async () => {
    renderTable([
      { name: "mid", quality: { version: 1, headline_score: 50, verified: true, panel_complete: true, scored_at: "2026-08-01T00:00:00Z" } },
      { name: "none" },
      { name: "high", quality: { version: 1, headline_score: 90, verified: true, panel_complete: true, scored_at: "2026-08-01T00:00:00Z" } },
    ]);
    await userEvent.click(screen.getByRole("columnheader", { name: /score/i })); // desc
    expect(rowNames()).toEqual(["high", "mid", "none"]);
    await userEvent.click(screen.getByRole("columnheader", { name: /score/i })); // asc
    expect(rowNames()).toEqual(["mid", "high", "none"]);
  });
});
