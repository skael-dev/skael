import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/handlers";
import { renderWithProviders, screen, waitFor, within, userEvent } from "@/test/render";
import { Settings } from "./settings";

describe("Settings", () => {
  it("renders workspace section with 'skael' text", async () => {
    renderWithProviders(<Settings />);

    // The WorkspaceSection has a Row with label "Workspace name" and value "skael"
    expect(await screen.findByText("Workspace name")).toBeInTheDocument();
    expect(screen.getByText("skael")).toBeInTheDocument();
  });

  it("shows the platform version reported by the server", async () => {
    renderWithProviders(<Settings />);

    // The handler reports a bare "0.10.0"; the UI prefixes the v.
    expect(await screen.findByText("v0.10.0")).toBeInTheDocument();
  });

  it("renders a dash for the version when capabilities fails", async () => {
    server.use(
      http.get("/api/capabilities", () => new HttpResponse(null, { status: 500 })),
    );
    renderWithProviders(<Settings />);

    // Fail-soft: the settings page still renders, the version reads "—".
    expect(await screen.findByText("Platform version")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.getByText("—")).toBeInTheDocument();
    });
  });

  it("API key list shows key names and prefixes", async () => {
    renderWithProviders(<Settings />);

    // mockApiKeys has two keys: "CI Pipeline" (prefix: sk_live_ci) and "Local Dev" (prefix: sk_live_dev)
    expect(await screen.findByText("CI Pipeline")).toBeInTheDocument();
    expect(screen.getByText("Local Dev")).toBeInTheDocument();

    // Prefixes are rendered with "..." appended
    expect(screen.getByText("sk_live_ci...")).toBeInTheDocument();
    expect(screen.getByText("sk_live_dev...")).toBeInTheDocument();
  });

  it("create key button exists and is clickable", async () => {
    const user = userEvent.setup();
    renderWithProviders(<Settings />);

    // Wait for keys to load
    await screen.findByText("CI Pipeline");

    // The "Create API Key" button is rendered via Button component
    const createBtn = screen.getByRole("button", { name: /Create API Key/i });
    expect(createBtn).toBeInTheDocument();

    // Click should open the create dialog
    await user.click(createBtn);

    // Dialog should appear with "Create API Key" title and input placeholder
    expect(await screen.findByPlaceholderText("e.g. CI/CD Pipeline")).toBeInTheDocument();
  });

  it("shows error state when API keys list returns 500", async () => {
    server.use(
      http.get("/api/auth/keys", () => {
        return HttpResponse.json({ detail: "internal server error" }, { status: 500 });
      }),
    );

    renderWithProviders(<Settings />);

    expect(await screen.findByText(/couldn't load api keys/i)).toBeInTheDocument();
  });

  it("keeps key row visible when DELETE returns 500", async () => {
    server.use(
      http.delete("/api/auth/keys/:id", () => {
        return HttpResponse.json({ detail: "internal server error" }, { status: 500 });
      }),
    );

    const user = userEvent.setup();
    renderWithProviders(<Settings />);

    // Wait for keys to render
    const keyRow = await screen.findByText("CI Pipeline");

    // The trash icon button is the only button inside the key row container.
    // Scope within the row's parent element to avoid clicking nav buttons.
    const rowEl = keyRow.closest("[class*='flex items-center gap-3']") as HTMLElement;
    const deleteBtn = within(rowEl).getByRole("button");
    await user.click(deleteBtn);

    // Confirmation dialog appears — click the destructive "Delete" button
    const confirmDelete = await screen.findByRole("button", { name: /^Delete$/ });
    await user.click(confirmDelete);

    // After the failed DELETE, the key list is NOT invalidated — CI Pipeline stays
    // in the list. The name may appear more than once (dialog + list), so use
    // getAllByText and assert at least one match exists.
    expect(screen.getAllByText("CI Pipeline").length).toBeGreaterThan(0);
  });

  describe("Team roles", () => {
    it("lists team members with a role control, except the owner", async () => {
      renderWithProviders(<Settings />);

      // Owner row shows the role as a static badge — there is one owner and
      // the role is not transferable.
      expect(await screen.findByText("Admin User")).toBeInTheDocument();
      expect(screen.getByText("owner")).toBeInTheDocument();
      expect(screen.queryByLabelText("Role for admin@test.com")).not.toBeInTheDocument();

      // Everyone else gets a control preselected to their current role.
      const memberSelect = screen.getByLabelText("Role for dana@test.com") as HTMLSelectElement;
      expect(memberSelect.value).toBe("member");
      const adminSelect = screen.getByLabelText("Role for sam@test.com") as HTMLSelectElement;
      expect(adminSelect.value).toBe("admin");
    });

    it("promotes a member to admin and reflects it in the list", async () => {
      const user = userEvent.setup();

      // After a successful change the list is refetched — serve the new role.
      let promoted = false;
      server.use(
        http.get("/api/admin/users", () => {
          return HttpResponse.json({
            users: [
              { id: "user-001", email: "admin@test.com", name: "Admin User", role: "owner", created_at: "2026-01-01T00:00:00Z" },
              {
                id: "user-002",
                email: "dana@test.com",
                name: "Dana Dev",
                role: promoted ? "admin" : "member",
                created_at: "2026-02-01T00:00:00Z",
              },
            ],
          });
        }),
        http.put("/api/admin/users/:id/role", async ({ request }) => {
          const body = (await request.json()) as { role: string };
          expect(body.role).toBe("admin");
          promoted = true;
          return HttpResponse.json({
            id: "user-002",
            email: "dana@test.com",
            name: "Dana Dev",
            role: "admin",
            created_at: "2026-02-01T00:00:00Z",
          });
        }),
      );

      renderWithProviders(<Settings />);

      const select = (await screen.findByLabelText("Role for dana@test.com")) as HTMLSelectElement;
      expect(select.value).toBe("member");

      await user.selectOptions(select, "admin");

      await waitFor(() => {
        const refreshed = screen.getByLabelText("Role for dana@test.com") as HTMLSelectElement;
        expect(refreshed.value).toBe("admin");
      });
    });

    it("demotes an admin to member", async () => {
      const user = userEvent.setup();

      let demoted = false;
      server.use(
        http.get("/api/admin/users", () => {
          return HttpResponse.json({
            users: [
              {
                id: "user-003",
                email: "sam@test.com",
                name: "Sam Ops",
                role: demoted ? "member" : "admin",
                created_at: "2026-03-01T00:00:00Z",
              },
            ],
          });
        }),
        http.put("/api/admin/users/:id/role", async ({ request }) => {
          const body = (await request.json()) as { role: string };
          expect(body.role).toBe("member");
          demoted = true;
          return HttpResponse.json({
            id: "user-003",
            email: "sam@test.com",
            name: "Sam Ops",
            role: "member",
            created_at: "2026-03-01T00:00:00Z",
          });
        }),
      );

      renderWithProviders(<Settings />);

      const select = (await screen.findByLabelText("Role for sam@test.com")) as HTMLSelectElement;
      await user.selectOptions(select, "member");

      await waitFor(() => {
        const refreshed = screen.getByLabelText("Role for sam@test.com") as HTMLSelectElement;
        expect(refreshed.value).toBe("member");
      });
    });

    it("keeps the previous role visible when the update is refused", async () => {
      const user = userEvent.setup();
      server.use(
        http.put("/api/admin/users/:id/role", () => {
          return HttpResponse.json({ detail: "owner role required" }, { status: 403 });
        }),
      );

      renderWithProviders(<Settings />);

      const select = (await screen.findByLabelText("Role for dana@test.com")) as HTMLSelectElement;
      await user.selectOptions(select, "admin");

      // The list is not invalidated on failure, so the row still reads member.
      await waitFor(() => {
        const refreshed = screen.getByLabelText("Role for dana@test.com") as HTMLSelectElement;
        expect(refreshed.value).toBe("member");
      });
    });

    it("hides the Team section from non-owners", async () => {
      server.use(
        http.get("/api/auth/me", () => {
          return HttpResponse.json({
            id: "user-002",
            email: "dana@test.com",
            name: "Dana Dev",
            role: "member",
          });
        }),
      );

      renderWithProviders(<Settings />);

      // Wait for a section that always renders, then assert Team is absent.
      expect(await screen.findByText("Workspace name")).toBeInTheDocument();
      await waitFor(() => {
        expect(screen.queryByText("Manage team members and credentials")).not.toBeInTheDocument();
      });
      expect(screen.queryByLabelText("Role for dana@test.com")).not.toBeInTheDocument();
    });
  });
});
