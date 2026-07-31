import { expect, test } from "@playwright/test";

// The Playwright web server runs `dev:web` (VITE_NO_ELECTRON=1), so
// useWorkspaceQuery serves the deterministic preview fixtures from
// lib/mock-data.ts instead of hitting a daemon. The tests run in Chromium
// (no window.ao), so the terminal shows its browser-preview surface.

test("renders the orchestrator-first workbench shell", async ({ page }) => {
	await page.goto("/");
	// The pinned board anchor + the Projects group + a title-based worker row.
	await expect(page.getByRole("button", { name: "Orchestrator board" })).toBeVisible();
	await expect(page.getByText("Projects")).toBeVisible();
	await expect(page.getByRole("button", { name: "Open Restore fallback renderer after WebGL init fails" })).toBeVisible();
	await expect(page.getByRole("button", { name: "Open Split terminal mux responsibilities" })).toBeVisible();
});

test("deep-links into a worker session", async ({ page }) => {
	await page.goto("/#/projects/api-gateway/sessions/refactor-mux");
	// Worker view = terminal plus the inspector rail for the fixture's no-PR state.
	await expect(page.getByText("No pull request opened yet.")).toBeVisible();
	await expect(page.getByRole("banner").getByText("feat/refactor-mux", { exact: true })).toBeVisible();
});

test("drilling into a worker opens its Git review rail", async ({ page }) => {
	await page.goto("/");
	await page.getByRole("button", { name: "Open Split terminal mux responsibilities" }).click();
	await expect(page).toHaveURL(/projects\/api-gateway\/sessions\/refactor-mux/);
	await expect(page.getByRole("tab", { name: "Summary" })).toBeVisible();
	await expect(page.getByText("internal/mux/terminal_mux.go")).toHaveCount(0);
});
