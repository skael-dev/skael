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
});
