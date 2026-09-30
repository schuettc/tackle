import { test, expect } from "@playwright/test";

test.describe("home page", () => {
  test("loads the title", async ({ page }) => {
    await page.goto("https://example.com");
    await expect(page).toHaveTitle(/Example/);
  });
});

test("standalone check", async ({ page }) => {
  await page.goto("https://example.com");
});
