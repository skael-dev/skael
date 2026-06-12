import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { AuthProvider } from "@/app/auth-provider";
import { App } from "@/app/app";

function renderApp(path: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  // App renders its own Routes tree; we wrap it with MemoryRouter so we can
  // inject the initial path without touching the real BrowserRouter.
  // App itself does NOT render a Router (it only renders <Routes>), so this works.
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <App />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("App routing", () => {
  it("renders a 404 page for unknown routes", async () => {
    renderApp("/definitely/not/a/route");
    expect(await screen.findByText(/page not found/i)).toBeInTheDocument();
  });
});
