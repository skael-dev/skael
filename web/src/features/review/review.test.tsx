import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthProvider } from "@/app/auth-provider";
import { server } from "@/test/handlers";
import { mockUser as defaultMockUser } from "@/test/fixtures";
import { ReviewQueue } from "./review-queue";
import type { HeldVersion } from "@/api/types.gen";

function withQuery(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={qc}>
      <AuthProvider>{ui}</AuthProvider>
    </QueryClientProvider>
  );
}

function mockReviewQueue(held: Partial<HeldVersion>[]) {
  server.use(
    http.get("/api/review/queue", () => {
      return HttpResponse.json({ held, total: held.length });
    }),
  );
}

function oneHeld(
  overrides: Partial<{ clears: string }> & Partial<HeldVersion>,
): Partial<HeldVersion> {
  const { clears, ...rest } = overrides;
  return {
    skill_name: "deploy-helper",
    version: 4,
    gate_state: "needs_review",
    created_at: "2026-08-01T00:00:00Z",
    gate_decision: {
      outcome: "needs_review",
      reasons: [
        {
          rule: "DATA_EXFILTRATION",
          class: "exfiltration",
          severity: "high",
          file: "SKILL.md",
          line: 12,
          message: "pipe to shell",
          clears: clears ?? "a verified evaluation, or an owner or admin approval",
        },
      ],
    },
    scan_result: {
      status: "warn",
      findings: [
        {
          rule: "DATA_EXFILTRATION",
          severity: "high",
          confidence: "high",
          file: "SKILL.md",
          line: 12,
          match: "curl | bash",
          message: "pipe to shell",
        },
      ],
      summary: { critical: 0, high: 1, medium: 0, info: 0 },
    },
    ...rest,
  };
}

// mockUser overrides the /api/auth/me handler so useAuth resolves the given
// role. Mirrors the idiom in settings.test.tsx, wrapped as a fixture helper.
function mockUser(overrides: Partial<{ role: string }>) {
  server.use(
    http.get("/api/auth/me", () => {
      return HttpResponse.json({ ...defaultMockUser, ...overrides });
    }),
  );
}

describe("ReviewQueue", () => {
  it("lists held versions with the finding that held them", async () => {
    mockReviewQueue([{
      skill_name: "deploy-helper", version: 4, gate_state: "needs_review",
      created_at: "2026-08-01T00:00:00Z",
      gate_decision: { outcome: "needs_review", reasons: [{ rule: "DATA_EXFILTRATION", class: "exfiltration", severity: "high", file: "SKILL.md", line: 12, message: "pipe to shell", clears: "a verified evaluation, or an owner or admin approval" }] },
      scan_result: { status: "warn", findings: [{ rule: "DATA_EXFILTRATION", severity: "high", confidence: "high", file: "SKILL.md", line: 12, match: "curl | bash", message: "pipe to shell" }], summary: { critical: 0, high: 1, medium: 0, info: 0 } },
    }]);
    render(withQuery(<ReviewQueue />));
    expect(await screen.findByText("deploy-helper")).toBeInTheDocument();
    expect(screen.getAllByText(/DATA_EXFILTRATION/).length).toBeGreaterThan(0);
  });

  it("renders the gate's clears sentence verbatim", async () => {
    mockReviewQueue([oneHeld({
      clears: "a verified evaluation scoring at or above 0, or an owner or admin approval",
    })]);
    render(withQuery(<ReviewQueue />));
    // Verbatim: the gate owns the wording. Paraphrasing here would put a
    // second definition of appealability in the UI.
    expect(await screen.findByText(/a verified evaluation scoring at or above 0/i)).toBeInTheDocument();
  });

  it("renders an unappealable finding's sentence too, without inventing an action", async () => {
    mockReviewQueue([oneHeld({
      clears: "nothing: credential-theft findings are unappealable.",
    })]);
    render(withQuery(<ReviewQueue />));
    expect(await screen.findByText(/unappealable/i)).toBeInTheDocument();
  });

  it("offers run eval as an action on a held version", async () => {
    mockReviewQueue([oneHeld({ clears: "a verified evaluation" })]);
    render(withQuery(<ReviewQueue />));
    expect(await screen.findByRole("button", { name: /run eval/i })).toBeInTheDocument();
  });

  it("disables approve for a member and says why", async () => {
    mockUser({ role: "member" });
    mockReviewQueue([oneHeld({ clears: "an owner or admin approval" })]);
    render(withQuery(<ReviewQueue />));
    const approve = await screen.findByRole("button", { name: /approve/i });
    expect(approve).toBeDisabled();
    expect(screen.getByText(/owner or admin/i)).toBeInTheDocument();
  });

  it("enables approve for an admin", async () => {
    mockUser({ role: "admin" });
    mockReviewQueue([oneHeld({ clears: "an owner or admin approval" })]);
    render(withQuery(<ReviewQueue />));
    expect(await screen.findByRole("button", { name: /approve/i })).toBeEnabled();
  });

  it("sends the held version's own version number when running an eval, not the latest", async () => {
    mockReviewQueue([oneHeld({ clears: "a verified evaluation" })]);
    let capturedBody: unknown;
    server.use(
      http.post("/api/skills/:name/evals", async ({ request }) => {
        capturedBody = await request.json();
        return HttpResponse.json({ id: "job-1", status: "queued" });
      }),
    );
    render(withQuery(<ReviewQueue />));
    const button = await screen.findByRole("button", { name: /run eval/i });
    await userEvent.click(button);
    expect(capturedBody).toEqual({ version: 4 });
  });

  it("does not enable run eval for a non-privileged member", async () => {
    mockUser({ role: "member" });
    mockReviewQueue([oneHeld({ clears: "an owner or admin approval" })]);
    render(withQuery(<ReviewQueue />));
    const button = await screen.findByRole("button", { name: /run eval/i });
    expect(button).toBeDisabled();
  });

  it("reads as clear, not empty, when nothing is held", async () => {
    mockReviewQueue([]);
    render(withQuery(<ReviewQueue />));
    expect(await screen.findByText(/nothing awaiting review/i)).toBeInTheDocument();
  });

  it("renders 'no decision recorded' instead of crashing on a malformed gate_decision", async () => {
    mockReviewQueue([{
      skill_name: "deploy-helper", version: 4, gate_state: "needs_review",
      created_at: "2026-08-01T00:00:00Z",
      // Malformed: not the { outcome, reasons } shape the gate actually sends.
      gate_decision: { unexpected: true } as unknown as HeldVersion["gate_decision"],
      scan_result: { status: "warn", findings: [], summary: { critical: 0, high: 0, medium: 0, info: 0 } },
    }]);
    render(withQuery(<ReviewQueue />));
    expect(await screen.findByText("deploy-helper")).toBeInTheDocument();
    expect(screen.getByText(/no decision recorded/i)).toBeInTheDocument();
  });

  // ── Per-reason chips and controls (Task 19) ────────────────────────

  it("shows one chip per outstanding reason", async () => {
    mockReviewQueue([oneHeld({
      clears: "a verified evaluation, or an owner or admin approval",
      hold_reasons: ["scan", "ownership"],
      outstanding: ["scan", "ownership"],
    })]);
    render(withQuery(<ReviewQueue />));
    expect(await screen.findByText("deploy-helper")).toBeInTheDocument();
    expect(screen.getAllByText(/— outstanding/i)).toHaveLength(2);
  });

  // A partly-cleared hold must NEVER read as released — this is the exact
  // case the whole per-reason review model exists to render honestly.
  it("shows a cleared reason as cleared while the version stays held", async () => {
    mockReviewQueue([oneHeld({
      clears: "an owner or admin approval",
      hold_reasons: ["scan", "ownership"],
      outstanding: ["ownership"],
    })]);
    render(withQuery(<ReviewQueue />));
    expect(await screen.findByText(/security finding/i)).toBeInTheDocument();
    expect(screen.getByText(/security finding.*cleared/i)).toBeInTheDocument();
    expect(screen.queryByText(/released/i)).not.toBeInTheDocument();
  });

  it("disables the approve control for a reason you cannot clear, with the reason", async () => {
    mockUser({ role: "member" });
    mockReviewQueue([oneHeld({
      clears: "an owner or admin approval",
      hold_reasons: ["ownership"],
      outstanding: ["ownership"],
      owners: [{ id: "user-someone-else", name: "Someone Else", email: "else@test.com" }],
      unowned: false,
    } as Partial<HeldVersion>)]);
    render(withQuery(<ReviewQueue />));
    const approve = await screen.findByRole("button", { name: /approve/i });
    expect(approve).toBeDisabled();
    expect(screen.getByText(/only an owner of this skill, or an instance admin/i)).toBeInTheDocument();
  });

  // Regression: the shared reason input must not be gated by the coarse
  // role-only `canDecide` prop alone. A member-role user who is listed in
  // held.owners can legitimately clear the `ownership` reason — they must
  // be able to type a reason and submit it, not have the approve button
  // enabled while the input beside it stays locked.
  it("lets a member-role namespace owner type a reason and approve the ownership hold", async () => {
    mockUser({ role: "member" });
    mockReviewQueue([oneHeld({
      clears: "an owner or admin approval",
      hold_reasons: ["ownership"],
      outstanding: ["ownership"],
      owners: [{ id: "user-001", name: "Admin User", email: "admin@test.com" }],
      unowned: false,
    } as Partial<HeldVersion>)]);
    let capturedBody: unknown;
    server.use(
      http.post("/api/skills/:name/versions/:version/review", async ({ request }) => {
        capturedBody = await request.json();
        return HttpResponse.json({ skill_name: "deploy-helper", version: 4 });
      }),
    );

    const user = userEvent.setup();
    render(withQuery(<ReviewQueue />));

    const approve = await screen.findByRole("button", { name: /approve/i });
    expect(approve).toBeEnabled();

    const reasonInput = screen.getByPlaceholderText(/reason \(recorded on the version\)/i);
    expect(reasonInput).toBeEnabled();

    await user.type(reasonInput, "I own this namespace, approving.");
    await user.click(approve);

    expect(capturedBody).toEqual({
      action: "approve",
      reason: "I own this namespace, approving.",
      hold_reason: "ownership",
    });
  });

  it("renders the SKILL.md diff", async () => {
    mockReviewQueue([oneHeld({ clears: "a verified evaluation" })]);
    server.use(
      http.get("/api/skills/:name/versions/:version/diff", () => {
        return HttpResponse.json({
          against: 3,
          skill_md: "--- v3/SKILL.md\n+++ v4/SKILL.md\n@@ -1,3 +1,3 @@\n-line two\n+line TWO\n context",
          files: [],
        });
      }),
    );
    render(withQuery(<ReviewQueue />));
    expect(await screen.findByText(/-line two/)).toBeInTheDocument();
    expect(screen.getByText(/\+line TWO/)).toBeInTheDocument();
  });

  it("flags a non-SKILL.md file addition prominently", async () => {
    mockReviewQueue([oneHeld({ clears: "a verified evaluation" })]);
    server.use(
      http.get("/api/skills/:name/versions/:version/diff", () => {
        return HttpResponse.json({
          against: 3,
          skill_md: "",
          files: [{ path: "scripts/setup.sh", status: "added" }],
        });
      }),
    );
    render(withQuery(<ReviewQueue />));
    expect(await screen.findByText("scripts/setup.sh")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /new file outside skill\.md/i })).toBeInTheDocument();
  });
});
