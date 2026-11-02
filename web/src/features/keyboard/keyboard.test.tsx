import { describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useLocation } from "react-router-dom";
import { http, HttpResponse } from "msw";
import { AuthProvider } from "@/app/auth-provider";
import { Sidebar } from "@/app/sidebar";
import { screen, userEvent, waitFor } from "@/test/render";
import { server } from "@/test/handlers";
import { CHORD_TIMEOUT_MS, createChordTracker, NAV_CHORDS } from "./keys";
import { useKeyboardLayer } from "./use-keyboard-layer";
import { useListNavigation } from "./use-list-navigation";

function mockRole(role: string) {
  server.use(
    http.get("/api/auth/session", () =>
      HttpResponse.json({ session: {}, user: { id: "u1", email: "u@test.com", name: "U", role } }),
    ),
    http.get("/api/review/queue", () => HttpResponse.json({ held: [], total: 0 })),
  );
}

function Harness({ onOpen, onRow }: { onOpen: () => void; onRow: (n: string) => void }) {
  useKeyboardLayer({ paletteOpen: false, setPaletteOpen: vi.fn(), openShortcuts: onOpen });
  const row = useListNavigation<string>(onRow);
  const { pathname } = useLocation();
  return (
    <div>
      <output data-testid="path">{pathname}</output>
      <input aria-label="field" />
      <textarea aria-label="area" />
      <div contentEditable suppressContentEditableWarning aria-label="editable">
        text
      </div>
      <div {...row("one")}>one</div>
      <div {...row("two")}>two</div>
    </div>
  );
}

function renderTree(ui: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/start"]}>
        <AuthProvider>{ui}</AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

async function setup() {
  mockRole("owner");
  const onOpen = vi.fn();
  const onRow = vi.fn();
  renderTree(<Harness onOpen={onOpen} onRow={onRow} />);
  return { onOpen, onRow, user: userEvent.setup() };
}

describe("chord tracker", () => {
  it("matches g then a key inside the timeout", () => {
    const t = createChordTracker(NAV_CHORDS);
    expect(t.press("g").kind).toBe("started");
    const result = t.press("t");
    expect(result.kind === "matched" && result.chord.path).toBe("/sets");
  });

  it("drops the prefix after the timeout", () => {
    let clock = 0;
    const t = createChordTracker(NAV_CHORDS, CHORD_TIMEOUT_MS, () => clock);
    t.press("g");
    clock = CHORD_TIMEOUT_MS + 1;
    expect(t.press("t").kind).toBe("none");
  });

  it("does not match a chord key with no prefix", () => {
    expect(createChordTracker(NAV_CHORDS).press("s").kind).toBe("none");
  });
});

describe("keyboard layer", () => {
  it("opens a page from a chord", async () => {
    const { user } = await setup();
    await user.keyboard("gr");
    expect(screen.getByTestId("path")).toHaveTextContent("/review");
  });

  it("opens the sheet, and moves and opens a list row", async () => {
    const { onOpen, onRow, user } = await setup();
    await user.keyboard("?");
    expect(onOpen).toHaveBeenCalledOnce();
    await user.keyboard("jj{Enter}");
    expect(onRow).toHaveBeenCalledWith("two");
    await user.keyboard("k{Enter}");
    expect(onRow).toHaveBeenLastCalledWith("one");
  });

  it.each(["field", "area", "editable"])("does not fire while typing in %s", async (label) => {
    const { onOpen, onRow, user } = await setup();
    await user.click(screen.getByLabelText(label));
    await user.keyboard("gr?j{Enter}");
    expect(screen.getByTestId("path")).toHaveTextContent("/start");
    expect(onOpen).not.toHaveBeenCalled();
    expect(onRow).not.toHaveBeenCalled();
  });

  it("does not fire while a dialog is open", async () => {
    const { onOpen, user } = await setup();
    const dialog = document.createElement("div");
    dialog.setAttribute("role", "dialog");
    document.body.appendChild(dialog);
    await user.keyboard("gr?");
    dialog.remove();
    expect(screen.getByTestId("path")).toHaveTextContent("/start");
    expect(onOpen).not.toHaveBeenCalled();
  });
});

describe("sidebar", () => {
  it("hides Service accounts from a member", async () => {
    mockRole("member");
    renderTree(<Sidebar />);
    // The avatar renders only once the session has loaded, so the role is known.
    await waitFor(() => expect(document.querySelector("aside button")).not.toBeNull());
    expect(screen.getByRole("link", { name: "Sets" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Service accounts" })).not.toBeInTheDocument();
  });

  it("hides Webhooks from a member", async () => {
    mockRole("member");
    renderTree(<Sidebar />);
    await waitFor(() => expect(document.querySelector("aside button")).not.toBeNull());
    expect(screen.queryByRole("link", { name: "Webhooks" })).not.toBeInTheDocument();
  });

  it("shows Service accounts to an owner", async () => {
    mockRole("owner");
    renderTree(<Sidebar />);
    expect(await screen.findByRole("link", { name: "Service accounts" })).toBeInTheDocument();
  });
});
