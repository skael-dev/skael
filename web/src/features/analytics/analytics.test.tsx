import { describe, it, expect } from "vitest";
import { renderWithProviders, screen, waitFor } from "@/test/render";
import { Analytics } from "./analytics";

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
});
