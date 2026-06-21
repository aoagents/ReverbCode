import { expect, test, type Page } from "@playwright/test";

// The Playwright web server runs `dev:web` (VITE_NO_ELECTRON=1), so
// useWorkspaceQuery serves the deterministic preview fixtures from
// lib/mock-data.ts instead of hitting a daemon. The tests run in Chromium
// (no window.ao), so the terminal shows its browser-preview surface.

test("renders the orchestrator-first workbench shell", async ({ page }) => {
	await page.goto("/");
	// The single pinned Orchestrator anchor + the Projects group + current title-based worker rows.
	await expect(page.getByRole("button", { name: "Orchestrator board" })).toBeVisible();
	await expect(page.getByText("Projects")).toBeVisible();
	await expect(
		page.getByRole("button", { name: "Open Restore fallback renderer after WebGL init fails" }),
	).toBeVisible();
	await expect(page.getByRole("button", { name: "Open Split terminal mux responsibilities" })).toBeVisible();
});

test("deep-links into a worker session", async ({ page }) => {
	await page.goto("/#/projects/api-gateway/sessions/refactor-mux");
	// Worker view = terminal preview plus current Summary inspector rail.
	await expectSessionDetail(page);
});

test("drilling into a worker opens its session detail view", async ({ page }) => {
	await page.goto("/");
	await page.getByRole("button", { name: "Open Split terminal mux responsibilities" }).click();
	await expect(page).toHaveURL(/projects\/api-gateway\/sessions\/refactor-mux/);
	await expectSessionDetail(page);
});

async function expectSessionDetail(page: Page) {
	const inspector = page.getByTestId("inspector");
	await expect(inspector).toBeVisible();
	await expect(page.getByText("Split terminal mux responsibilities")).toBeVisible();
	await expect(inspector.getByText("feat/refactor-mux")).toBeVisible();
	await expect(
		page.getByTestId("terminal").getByText("Browser preview renders a static terminal surface."),
	).toBeVisible();
}
