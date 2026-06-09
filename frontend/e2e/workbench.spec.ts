import { expect, test } from "@playwright/test";

test("loads the workbench shell", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Desktop shell scaffold" })).toBeVisible();
  await expect(page.getByRole("button", { name: "New task" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /switch to .* theme/i })).toHaveCount(0);
});

test("loads a session route", async ({ page }) => {
  await page.goto("/#/workspaces/agent-orchestrator/sessions/ao-api-contract");
  await expect(page.getByRole("heading", { name: "Daemon bridge wiring" })).toBeVisible();
});
