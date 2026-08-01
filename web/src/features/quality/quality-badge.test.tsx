import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { QualityBadge } from "./quality-badge";
import type { QualitySummary } from "@/api/types.gen";

function q(overrides: Partial<QualitySummary> = {}): QualitySummary {
  return {
    version: 1,
    headline_score: 90,
    verified: true,
    panel_complete: true,
    scored_at: "2026-08-01T00:00:00Z",
    ...overrides,
  };
}

describe("QualityBadge", () => {
  it("shows a confident current badge when the scored version is the latest served one", () => {
    render(<QualityBadge quality={q({ version: 3 })} latestVersion={3} />);
    const badge = screen.getByTitle("Verified score");
    expect(badge).toHaveTextContent("90");
  });

  it("does not show a not-served indicator for a normal current score", () => {
    const { container } = render(<QualityBadge quality={q({ version: 3 })} latestVersion={3} />);
    expect(container.querySelector('[aria-hidden="true"]')).not.toBeInTheDocument();
  });

  it("marks a held-only skill's score as not currently served, not as current or stale", () => {
    // latestVersion 0: nothing has been released (the only version is held
    // for review). quality.version 1: that held version was scored anyway.
    // quality.version (1) > latestVersion (0), so this must not read as a
    // confident "Verified score" — the scored bundle isn't servable.
    render(<QualityBadge quality={q({ version: 1 })} latestVersion={0} />);
    const badge = screen.getByTitle(/not currently served/i);
    expect(badge).toHaveTextContent("90");
    // Must not fall through to the plain "current" title, and must not be
    // mistaken for the (unrelated) stale-version case either.
    expect(screen.queryByTitle("Verified score")).not.toBeInTheDocument();
  });

  it("keeps the displayed score unchanged for the not-served case", () => {
    render(<QualityBadge quality={q({ version: 1, headline_score: 74.2 })} latestVersion={0} />);
    expect(screen.getByText("74")).toBeInTheDocument();
  });
});
