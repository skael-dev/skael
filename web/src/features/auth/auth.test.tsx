import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { AuthProvider } from "@/app/auth-provider";
import { server } from "@/test/handlers";
import { Login } from "./login";
import { Signup } from "./signup";

function renderAuth(path: string) {
  // Override /api/auth/me to return 401 so user appears unauthenticated
  server.use(
    http.get("/api/auth/me", () => HttpResponse.json({}, { status: 401 })),
  );

  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route path="/signup" element={<Signup />} />
            <Route path="/" element={<div>Dashboard</div>} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("Auth", () => {
  it("login form renders email and password inputs", async () => {
    renderAuth("/login");

    // The Login form has labeled inputs for email and password
    expect(await screen.findByLabelText(/email/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();

    // Also check placeholders
    expect(screen.getByPlaceholderText("you@example.com")).toBeInTheDocument();
  });

  it("login with valid credentials redirects to dashboard", async () => {
    const user = userEvent.setup();
    renderAuth("/login");

    // Wait for the form to render
    const emailInput = await screen.findByLabelText(/email/i);
    const passwordInput = screen.getByLabelText(/password/i);

    // Type valid credentials (admin@test.com / password123 from MSW handler)
    await user.type(emailInput, "admin@test.com");
    await user.type(passwordInput, "password123");

    // Submit the form
    const submitBtn = screen.getByRole("button", { name: /sign in/i });
    await user.click(submitBtn);

    // Should navigate to "/" which renders "Dashboard"
    expect(await screen.findByText("Dashboard")).toBeInTheDocument();
  });

  it("login with bad credentials shows error message", async () => {
    const user = userEvent.setup();
    renderAuth("/login");

    const emailInput = await screen.findByLabelText(/email/i);
    const passwordInput = screen.getByLabelText(/password/i);

    // Type invalid credentials
    await user.type(emailInput, "wrong@test.com");
    await user.type(passwordInput, "wrongpassword");

    const submitBtn = screen.getByRole("button", { name: /sign in/i });
    await user.click(submitBtn);

    // The error handler returns { detail: "Invalid credentials" }
    // The Login component catches the error and shows err.message
    expect(await screen.findByText("Invalid credentials")).toBeInTheDocument();
  });

  it("signup form renders email, name, and password inputs", async () => {
    renderAuth("/signup");

    // The Signup form has labeled inputs for email, name, and password
    expect(await screen.findByLabelText(/email/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/name/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();

    // Also check heading
    expect(screen.getByText("Create your account")).toBeInTheDocument();
  });

  it("login page has link to signup", async () => {
    renderAuth("/login");

    // The Login page has "Sign up" link at the bottom
    const signupLink = await screen.findByRole("link", { name: /sign up/i });
    expect(signupLink).toBeInTheDocument();
    expect(signupLink).toHaveAttribute("href", "/signup");
  });
});
