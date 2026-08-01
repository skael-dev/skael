import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { server } from "@/test/handlers";
import { QualityBadge } from "./quality-badge";
import { EvalStatus } from "./eval-status";
import type { JobOutput } from "@/api/types.gen";

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

function withQuery(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{ui}</QueryClientProvider>;
}

function mockEvals(jobs: Partial<JobOutput>[]) {
  server.use(
    http.get("/api/skills/:name/evals", () => {
      return HttpResponse.json({ jobs });
    }),
  );
}

describe("EvalStatus", () => {
  it("shows queue position rather than a spinner", async () => {
    // Wire value is 0-indexed; display is 1-indexed (a human is "first",
    // not "zeroth"). queue_position: 3 -> "position 4".
    mockEvals([{ id: "j1", status: "queued", queue_position: 3, enqueued_at: "2026-08-01T00:00:00Z" }]);
    render(withQuery(<EvalStatus skillName="s" quality={null} latestVersion={1} />));
    expect(await screen.findByText(/position 4/i)).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("shows the front of the queue as position 1, not position 0", async () => {
    mockEvals([{ id: "j1", status: "queued", queue_position: 0, enqueued_at: "2026-08-01T00:00:00Z" }]);
    render(withQuery(<EvalStatus skillName="s" quality={null} latestVersion={1} />));
    expect(await screen.findByText(/position 1/i)).toBeInTheDocument();
    expect(screen.queryByText(/position 0/i)).not.toBeInTheDocument();
  });

  it("shows elapsed time while running", async () => {
    mockEvals([{
      id: "j1", status: "running", queue_position: 0,
      enqueued_at: "2026-08-01T00:00:00Z",
      started_at: new Date(Date.now() - 12 * 60_000).toISOString(),
    }]);
    render(withQuery(<EvalStatus skillName="s" quality={null} latestVersion={1} />));
    expect(await screen.findByText(/12m elapsed/i)).toBeInTheDocument();
  });

  it("offers to run an eval when there is no score and no job", async () => {
    mockEvals([]);
    render(withQuery(<EvalStatus skillName="s" quality={null} latestVersion={1} />));
    expect(await screen.findByRole("button", { name: /run eval/i })).toBeInTheDocument();
  });

  it("names both versions and offers a re-run when the score is stale", async () => {
    mockEvals([]);
    render(withQuery(
      <EvalStatus
        skillName="s"
        quality={{ version: 3, headline_score: 74, verified: true, panel_complete: true, scored_at: "2026-08-01T00:00:00Z" }}
        latestVersion={7}
      />,
    ));
    expect(await screen.findByText(/scored on v3 · current v7/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /re-run eval/i })).toBeInTheDocument();
  });

  it("surfaces a failed job's error", async () => {
    mockEvals([{ id: "j1", status: "failed", queue_position: 0, last_error: "sandbox image missing", enqueued_at: "2026-08-01T00:00:00Z" }]);
    render(withQuery(<EvalStatus skillName="s" quality={null} latestVersion={1} />));
    expect(await screen.findByText(/sandbox image missing/i)).toBeInTheDocument();
  });

  it("surfaces an enqueue failure so a silent click isn't mistaken for a no-op", async () => {
    const user = userEvent.setup();
    mockEvals([]);
    server.use(
      http.post("/api/skills/:name/evals", () => {
        return HttpResponse.json({ detail: "queue is full" }, { status: 500 });
      }),
    );
    render(withQuery(<EvalStatus skillName="s" quality={null} latestVersion={1} />));

    const button = await screen.findByRole("button", { name: /run eval/i });
    await user.click(button);

    expect(await screen.findByText(/failed|queue is full/i)).toBeInTheDocument();
  });
});
