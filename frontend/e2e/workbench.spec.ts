import { expect, test } from "@playwright/test";

// The dev server runs with import.meta.env.DEV === true, so useWorkspaceQuery
// falls back to mockWorkspaces (lib/mock-data.ts). These assertions target that
// deterministic mock UI; they run in Chromium (no window.ao), so the terminal
// shows its browser-preview surface.

test("renders the workbench shell with the default session", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Desktop shell scaffold" })).toBeVisible();
  await expect(page.getByText("agent-orchestrator-1").first()).toBeVisible();
  await expect(page.getByRole("tab", { name: "Terminal" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Details" })).toBeVisible();
});

test("deep-links to a session route", async ({ page }) => {
  await page.goto("/#/workspaces/agent-orchestrator/sessions/ao-api-contract");
  await expect(page.getByRole("heading", { name: "Daemon bridge wiring" })).toBeVisible();
});

test("Details tab shows session metadata", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("tab", { name: "Details" }).click();
  await expect(page.getByText("Provider")).toBeVisible();
  await expect(page.getByText("Branch")).toBeVisible();
});
