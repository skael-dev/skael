import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { server } from "@/test/handlers";
import { SubmissionDetail } from "./submission-detail";

const sub = {
  id: "11111111-1111-1111-1111-111111111111",
  skill_name: "deploy-helper",
  consumer: "factoree",
  submitted_by: "factoree@service",
  rationale: "Fix the rollback step.",
  run_link: "",
  external_run_id: "",
  checksum: "abc",
  file_manifest: [],
  scan_result: { status: "clean", findings: [], summary: { critical: 0, high: 0, medium: 0, info: 0 } },
  state: "pending",
  decided_by: "",
  reason: "",
  edited: false,
  created_at: "2026-10-01T00:00:00Z",
};

const diff = {
  against: 1,
  files: [
    { path: "SKILL.md", status: "modified", binary: false, size: 5, content: "hello", diff: "+hello\n" },
    { path: "scripts/run.sh", status: "added", binary: false, size: 4, content: "echo", diff: "+echo\n" },
  ],
};

let posted: unknown[];

function renderDetail() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SubmissionDetail id={sub.id} backHref="/review?tab=submissions" />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("SubmissionDetail decisions", () => {
  beforeEach(() => {
    posted = [];
    server.use(
      http.get("/api/submissions/:id/diff", () => HttpResponse.json(diff)),
      http.get("/api/submissions/:id", () => HttpResponse.json(sub)),
      http.get("/api/skills/:name/versions", () => HttpResponse.json({ versions: [] })),
      http.post("/api/submissions/:id/decision", async ({ request }) => {
        posted.push(await request.json());
        return HttpResponse.json({ ...sub, state: "approved", version: 2 });
      }),
    );
  });

  it("does not send a rejection without a reason", async () => {
    const user = userEvent.setup();
    renderDetail();
    await user.click(await screen.findByRole("button", { name: "Reject" }));
    await user.type(screen.getByRole("textbox", { name: /reason/i }), "   ");
    await user.click(screen.getByRole("button", { name: "Reject submission" }));
    expect(await screen.findByText(/a rejection needs a reason/i)).toBeInTheDocument();
    expect(posted).toEqual([]);
  });

  it("sends only the changed files on edit then approve", async () => {
    const user = userEvent.setup();
    renderDetail();
    await screen.findByText("+hello");
    await user.click(screen.getByRole("button", { name: "Edit then approve" }));
    await user.type(screen.getByRole("textbox", { name: "scripts/run.sh" }), " hi");
    await user.click(screen.getByRole("button", { name: "Approve with edits" }));
    await screen.findByText(/Approved/);
    expect(posted).toEqual([
      { action: "approve", edits: [{ path: "scripts/run.sh", content: "echo hi" }] },
    ]);
  });
});
