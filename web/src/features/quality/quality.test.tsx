import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { QualityBadge } from "./quality-badge";

const verified = {
  version: 3, headline_score: 74.2, verified: true,
  panel_complete: true, scored_at: "2026-08-01T00:00:00Z",
};

describe("QualityBadge", () => {
  it("reads neutral when the skill has never been scored", () => {
    render(<QualityBadge quality={null} latestVersion={3} />);
    expect(screen.getByTitle(/not yet scored/i)).toBeInTheDocument();
    // The failure this guards: an unscored skill rendered as a zero.
    expect(screen.queryByText("0")).not.toBeInTheDocument();
  });

  it("shows the rounded score when verified", () => {
    render(<QualityBadge quality={verified} latestVersion={3} />);
    expect(screen.getByText("74")).toBeInTheDocument();
    expect(screen.getByTitle(/verified/i)).toBeInTheDocument();
  });

  it("distinguishes attested from verified", () => {
    const { container } = render(
      <QualityBadge quality={{ ...verified, verified: false }} latestVersion={3} />,
    );
    expect(screen.getByTitle(/attested/i)).toBeInTheDocument();
    expect(container.querySelector("[data-track='hairline']")).toBeTruthy();
  });

  it("shows an incomplete panel as incomplete, never as a low score", () => {
    render(
      <QualityBadge
        quality={{ ...verified, panel_complete: false, headline_score: 12 }}
        latestVersion={3}
      />,
    );
    expect(screen.getByTitle(/incomplete panel/i)).toBeInTheDocument();
    expect(screen.getByText(/~/)).toBeInTheDocument();
  });

  it("marks a score that was computed on an older version", () => {
    render(<QualityBadge quality={verified} latestVersion={7} />);
    expect(screen.getByTitle(/scored on v3.*current v7/i)).toBeInTheDocument();
  });

  it("does not mark a current score as stale", () => {
    render(<QualityBadge quality={verified} latestVersion={3} />);
    expect(screen.queryByTitle(/current v/i)).not.toBeInTheDocument();
  });
});
