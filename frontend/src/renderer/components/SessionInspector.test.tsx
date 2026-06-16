import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionInspector } from "./SessionInspector";
import type { WorkspaceSession } from "../types/workspace";

const api = vi.hoisted(() => ({
	GET: vi.fn(),
	POST: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: api,
	apiErrorMessage: (error: unknown, fallback = "Request failed") =>
		error instanceof Error ? error.message : fallback,
}));

const session = {
	id: "sess-1",
	terminalHandleId: "worker-pane",
	workspaceId: "proj-1",
	workspaceName: "my-app",
	title: "review me",
	provider: "codex",
	kind: "worker",
	branch: "session/sess-1",
	status: "working",
	createdAt: "2026-06-16T10:00:00Z",
	updatedAt: "2026-06-16T10:05:00Z",
	pullRequest: { number: 3, state: "open" },
} satisfies WorkspaceSession;

function renderWithQuery(children: ReactNode) {
	const client = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	return render(<QueryClientProvider client={client}>{children}</QueryClientProvider>);
}

function mockCommonGets(reviews: unknown[] = [], reviewerHandleId = "") {
	api.GET.mockImplementation(async (path: string) => {
		if (path === "/api/v1/sessions/{sessionId}/pr") {
			return {
				data: {
					prs: [
						{
							url: "https://github.com/aoagents/reverbcode/pull/3",
							number: 3,
							state: "open",
							ci: "passing",
							review: "required",
							mergeability: "mergeable",
							reviewComments: false,
							updatedAt: "2026-06-16T10:05:00Z",
						},
					],
				},
			};
		}
		if (path === "/api/v1/sessions/{sessionId}/reviews") {
			return { data: { reviewerHandleId, reviews } };
		}
		if (path === "/api/v1/projects/{id}") {
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						kind: "git",
						name: "my-app",
						path: "/repo",
						repo: "my-app",
						defaultBranch: "main",
						config: { reviewers: [{ harness: "codex" }] },
					},
				},
			};
		}
		return { data: undefined };
	});
}

describe("SessionInspector reviews", () => {
	beforeEach(() => {
		api.GET.mockReset();
		api.POST.mockReset();
	});

	it("triggers a review and opens the returned reviewer terminal", async () => {
		mockCommonGets();
		api.POST.mockResolvedValue({
			data: {
				reviewerHandleId: "reviewer-pane",
				review: {
					id: "run-1",
					reviewId: "review-1",
					sessionId: "sess-1",
					harness: "codex",
					status: "running",
					verdict: "",
					body: "",
					prUrl: "https://github.com/aoagents/reverbcode/pull/3",
					targetSha: "abc123",
					createdAt: "2026-06-16T10:06:00Z",
				},
			},
		});
		const onOpenReviewerTerminal = vi.fn();

		renderWithQuery(<SessionInspector onOpenReviewerTerminal={onOpenReviewerTerminal} session={session} />);

		fireEvent.click(await screen.findByRole("button", { name: /run review/i }));

		await waitFor(() =>
			expect(api.POST).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/reviews/trigger", {
				params: { path: { sessionId: "sess-1" } },
			}),
		);
		expect(onOpenReviewerTerminal).toHaveBeenCalledWith({ handleId: "reviewer-pane", harness: "codex" });
	});

	it("shows an approved review and opens its terminal", async () => {
		mockCommonGets(
			[
				{
					id: "run-1",
					reviewId: "review-1",
					sessionId: "sess-1",
					harness: "codex",
					status: "complete",
					verdict: "approved",
					body: "Looks good.",
					prUrl: "https://github.com/aoagents/reverbcode/pull/3",
					targetSha: "abc123",
					createdAt: "2026-06-16T10:06:00Z",
				},
			],
			"reviewer-pane",
		);
		const onOpenReviewerTerminal = vi.fn();

		renderWithQuery(<SessionInspector onOpenReviewerTerminal={onOpenReviewerTerminal} session={session} />);

		await waitFor(() => expect(screen.getAllByText("Approved").length).toBeGreaterThan(0));
		fireEvent.click(screen.getByRole("button", { name: /open terminal/i }));

		expect(onOpenReviewerTerminal).toHaveBeenCalledWith({ handleId: "reviewer-pane", harness: "codex" });
	});
});
