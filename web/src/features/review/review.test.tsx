import { render, screen } from "@testing-library/react";
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

function oneHeld(overrides: Partial<{ clears: string }>): Partial<HeldVersion> {
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
          clears: overrides.clears ?? "a verified evaluation, or an owner or admin approval",
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

  it("reads as clear, not empty, when nothing is held", async () => {
    mockReviewQueue([]);
    render(withQuery(<ReviewQueue />));
    expect(await screen.findByText(/nothing awaiting review/i)).toBeInTheDocument();
  });
});
