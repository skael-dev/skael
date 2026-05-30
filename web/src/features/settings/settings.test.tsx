import { describe, it, expect } from "vitest";
import { renderWithProviders, screen, waitFor, userEvent } from "@/test/render";
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
});
