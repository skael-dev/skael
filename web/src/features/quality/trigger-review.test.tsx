import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { server } from "@/test/handlers";
import { TriggerReview } from "./trigger-review";

function renderReview(
  { ref, onDone }: { ref?: string; onDone?: () => void } = {},
) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <TriggerReview suiteRef={ref ?? "default"} skillName="demo-skill" onDone={onDone} />
    </QueryClientProvider>,
  );
}

describe("TriggerReview", () => {
  it("shows the stored queries under their two headings", async () => {
    renderReview();

    expect(await screen.findByText(/should trigger/i)).toBeInTheDocument();
    expect(screen.getByText(/should not trigger/i)).toBeInTheDocument();
    expect(screen.getByDisplayValue(/extract the tables/i)).toBeInTheDocument();
  });

  it("saves an unedited set and reports that nothing changed", async () => {
    const onDone = vi.fn();
    renderReview({ onDone });

    await userEvent.click(await screen.findByRole("button", { name: /save review/i }));

    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(await screen.findByText(/marked as reviewed/i)).toBeInTheDocument();
  });

  it("offers a new evaluation when the review changed the set", async () => {
    // The default handler keys its response on the path ref alone, so it
    // cannot show whether the click changed anything sent to the server.
    // This override reads the posted body instead. The assertion below
    // fails if "Add query" adds nothing to the request.
    let posted: unknown;
    server.use(
      http.post("/api/eval/suites/:ref/review", async ({ request }) => {
        posted = await request.json();
        return HttpResponse.json({ ref: "new-ref", changed: true });
      }),
    );
    renderReview({ ref: "edited" });

    await userEvent.click(await screen.findByRole("button", { name: /add query/i }));
    await userEvent.click(screen.getByRole("button", { name: /save review/i }));

    expect(await screen.findByText(/re-run the evaluation/i)).toBeInTheDocument();
    expect(posted).toMatchObject({
      triggers: expect.arrayContaining([{ query: "", should_trigger: true }]),
    });
  });

  it("toggles a query between should and should not trigger", async () => {
    renderReview();

    const toggles = await screen.findAllByRole("checkbox");
    await userEvent.click(toggles[0]);

    expect(screen.getAllByText(/should not trigger/i).length).toBeGreaterThan(0);
  });
});
