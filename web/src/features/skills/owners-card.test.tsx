import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/handlers";
import { renderWithProviders, screen } from "@/test/render";
import { OwnersCard } from "./owners-card";

function mockOwners(body: {
  owners: Array<{ id: string; name: string; email: string }>;
  unowned: boolean;
  rule_pattern?: string;
}) {
  server.use(
    http.get("/api/skills/:name/owners", () => {
      return HttpResponse.json(body);
    }),
  );
}

describe("OwnersCard", () => {
  it("names the rule the owners came from", async () => {
    mockOwners({
      owners: [{ id: "user-alice-001", name: "Alice", email: "alice@test.com" }],
      unowned: false,
      rule_pattern: "payments:*",
    });

    renderWithProviders(<OwnersCard skillName="payments:refunds" />);

    expect(await screen.findByText("Alice")).toBeInTheDocument();
    expect(screen.getByText(/via/i)).toBeInTheDocument();
    expect(screen.getByText("payments:*")).toBeInTheDocument();
  });

  it("renders an unowned skill distinctly", async () => {
    mockOwners({ owners: [], unowned: true });

    renderWithProviders(<OwnersCard skillName="scratch-skill" />);

    expect(await screen.findByText(/no owners/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /assign an owner/i })).toHaveAttribute(
      "href",
      "/settings/ownership",
    );
  });
});
