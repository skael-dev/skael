import { test, expect } from "@playwright/test";

test.describe("Auth flow", () => {
  const email = `e2e-${Date.now()}@test.com`;
  const password = "testpassword123";
  const name = "E2E User";

  test("unauthenticated user is redirected to login", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveURL(/\/login/);
  });

  test("signup creates account and lands on dashboard", async ({ page }) => {
    await page.goto("/signup");
    await page.getByPlaceholder(/email/i).fill(email);
    await page.getByPlaceholder(/name/i).fill(name);
    await page.getByPlaceholder(/password/i).fill(password);
    await page.getByRole("button", { name: /sign up|create/i }).click();
    await expect(page).toHaveURL("/");
  });

  test("login with existing credentials works", async ({ page }) => {
    await page.goto("/login");
    await page.getByPlaceholder(/email/i).fill(email);
    await page.getByPlaceholder(/password/i).fill(password);
    await page.getByRole("button", { name: /log in|sign in/i }).click();
    await expect(page).toHaveURL("/");
  });
});
