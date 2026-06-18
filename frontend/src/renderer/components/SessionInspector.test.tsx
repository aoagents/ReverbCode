import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SessionInspector } from "./SessionInspector";
import type { PRState, PullRequestFacts, WorkspaceSession } from "../types/workspace";

const pr = (n: number, state: PRState): PullRequestFacts => ({
	url: `https://example.com/pr/${n}`,
	number: n,
	state,
	ci: "passing",
	review: "approved",
	mergeability: "mergeable",
	reviewComments: false,
	updatedAt: "2026-06-15T00:00:00Z",
});

const session = (prs: PullRequestFacts[]): WorkspaceSession => ({
	id: "sess-1",
	workspaceId: "ws-1",
	workspaceName: "my-app",
	title: "do the thing",
	provider: "claude-code",
	kind: "worker",
	branch: "feat/ns",
	status: "review_pending",
	updatedAt: "2026-06-15T00:00:00Z",
	prs,
});

describe("SessionInspector PR section", () => {
	// Scope assertions to the PR section: the activity timeline also renders
	// "Opened PR #n", so an unscoped query matches both the card and the event.
	const prSection = (title: string) =>
		within(screen.getByText(title).closest("section.inspector-section") as HTMLElement);

	it("renders one card per PR, ordered actionable-first, when a session owns a stack", () => {
		render(<SessionInspector session={session([pr(40, "merged"), pr(41, "open"), pr(42, "draft")])} />);

		expect(screen.getByText("Pull requests (3)")).toBeInTheDocument();
		const cards = prSection("Pull requests (3)")
			.getAllByText(/^PR #\d+$/)
			.map((el) => el.textContent);
		// open (41), draft (42), merged (40)
		expect(cards).toEqual(["PR #41", "PR #42", "PR #40"]);
	});

	it("uses the singular heading and shows enriched facts for a single PR", () => {
		render(<SessionInspector session={session([pr(7, "open")])} />);

		expect(screen.getByText("Pull request")).toBeInTheDocument();
		expect(screen.queryByText(/Pull requests \(/)).not.toBeInTheDocument();
		expect(prSection("Pull request").getByText("PR #7")).toBeInTheDocument();
		// CI/Merge/Review facts surface per card.
		expect(prSection("Pull request").getAllByText("passing").length).toBeGreaterThan(0);
	});

	it("shows the empty state when there are no PRs", () => {
		render(<SessionInspector session={session([])} />);
		expect(screen.getByText("No pull request opened yet.")).toBeInTheDocument();
	});

	it("links each PR to its url", () => {
		render(<SessionInspector session={session([pr(41, "open"), pr(42, "draft")])} />);
		const links = screen.getAllByRole("link", { name: /Open/ });
		expect(links.map((a) => a.getAttribute("href"))).toEqual([
			"https://example.com/pr/41",
			"https://example.com/pr/42",
		]);
	});
});

describe("SessionInspector tabs", () => {
	it("exposes Summary, Reviews, and Browser as the three inspector tabs", () => {
		render(<SessionInspector session={session([pr(1, "open")])} />);
		const tabs = screen.getAllByRole("tab").map((el) => el.textContent?.trim());
		expect(tabs).toEqual(["Summary", "Reviews", "Browser"]);
	});

	it("switches to the Reviews tab and shows the empty placeholder", () => {
		render(<SessionInspector session={session([pr(1, "open")])} />);
		fireEvent.click(screen.getByRole("tab", { name: /Reviews/ }));
		expect(screen.getByText("No reviews yet.")).toBeInTheDocument();
	});
});
