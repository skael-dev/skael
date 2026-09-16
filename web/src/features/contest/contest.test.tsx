import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthProvider } from "@/app/auth-provider";
import { server } from "@/test/handlers";
import { ContestPage } from "./contest-page";
import type { ContestOutput } from "@/api/types.gen";

const base: ContestOutput = {
  id: "c1",
  status: "done",
  outcome: "winner",
  winner: "payments:deploy@3",
  detail: "payments:deploy@3 wins",
  suite_note: "suite derived from every candidate",
  tier: "full",
  attempts: 3,
  chain_link: false,
  created_at: "2026-09-16T00:00:00Z",
  candidates: [
    { skill: "payments:deploy", version: 3, label: "payments:deploy@3" },
    { skill: "platform:deploy", version: 7, label: "platform:deploy@7" },
  ],
  pairings: [
    {
      a: "payments:deploy@3",
      b: "platform:deploy@7",
      a_wins: 6,
      b_wins: 0,
      ties: 6,
      p_value: 0.031,
      outcome: "winner",
      winner: "payments:deploy@3",
      tasks: [
        { task_id: "1", a: "payments:deploy@3", b: "platform:deploy@7", a_rate: 1, b_rate: 0, winner: "payments:deploy@3" },
        { task_id: "2", a: "payments:deploy@3", b: "platform:deploy@7", a_rate: 1, b_rate: 1, winner: "" },
      ],
    },
  ],
  losses: [
    { label: "platform:deploy@7", task_id: "1", missed: ["tags the release"], evidence: "no tag was created" },
  ],
};

function renderContest(contest: ContestOutput) {
  server.use(http.get("/api/eval/contests/:id", () => HttpResponse.json(contest)));
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <MemoryRouter initialEntries={["/contests/c1"]}>
          <Routes>
            <Route path="/contests/:id" element={<ContestPage />} />
          </Routes>
        </MemoryRouter>
      </AuthProvider>
    </QueryClientProvider>,
  );
}

describe("ContestPage", () => {
  it("shows the split and every task, not just the verdict", async () => {
    renderContest(base);
    // The split is what a reviewer who distrusts one number reads.
    await screen.findByText(/6–0/);
    expect(screen.getByText(/6–0 · 6 tied · p=0\.031/)).toBeInTheDocument();
    expect(screen.getByText("tie")).toBeInTheDocument();
  });

  it("says who chose the tasks", async () => {
    renderContest(base);
    expect(await screen.findByText(/derived from every candidate/)).toBeInTheDocument();
  });

  it("lists what the losing candidate missed", async () => {
    renderContest(base);
    expect(await screen.findByText(/tags the release/)).toBeInTheDocument();
    expect(screen.getByText(/no tag was created/)).toBeInTheDocument();
  });

  // Too close to call is a verdict, not a missing one.
  it("reads a too-close-to-call verdict as the answer", async () => {
    renderContest({
      ...base,
      outcome: "too_close_to_call",
      winner: "",
      detail:
        "too close to call: only 1 of 12 tasks separated the candidates, and 6 are needed before any split can beat chance — the suite needs tasks these candidates handle differently",
    });
    expect(await screen.findByText(/the suite needs tasks/)).toBeInTheDocument();
  });

  it("states that a contest changes nothing", async () => {
    renderContest(base);
    expect(await screen.findByText(/releases nothing and holds nothing/)).toBeInTheDocument();
  });
});
