import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { SessionInspector } from "./SessionInspector";

const { getMock, postMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: getMock,
		POST: postMock,
	},
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (error instanceof Error) return error.message;
		if (typeof error === "object" && error !== null && "message" in error) {
			return String((error as { message: unknown }).message);
		}
		return fallback;
	},
}));

const worker: WorkspaceSession = {
	id: "sess-1",
	workspaceId: "proj-1",
	workspaceName: "my-app",
	title: "do the thing",
	provider: "claude-code",
	kind: "worker",
	branch: "ao/sess-1",
	status: "working",
	updatedAt: "2026-06-10T00:00:00Z",
};

const reviewSession = {
	...worker,
	terminalHandleId: "worker-pane",
	title: "review me",
	provider: "codex",
	branch: "session/sess-1",
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
	getMock.mockImplementation(async (path: string) => {
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

const approvedReview = {
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
};

beforeEach(() => {
	getMock.mockReset();
	postMock.mockReset();
	getMock.mockResolvedValue({ data: { prs: [] }, error: undefined });
	postMock.mockResolvedValue({ data: { ok: true, sessionId: "sess-1" }, error: undefined });
});

describe("SessionInspector reviews", () => {
	it("triggers a review and opens the returned reviewer terminal", async () => {
		mockCommonGets();
		postMock.mockResolvedValue({
			response: { status: 201 },
			data: {
				reviewerHandleId: "reviewer-pane",
				review: {
					...approvedReview,
					status: "running",
					verdict: "",
					body: "",
				},
			},
		});
		const onOpenReviewerTerminal = vi.fn();

		renderWithQuery(<SessionInspector onOpenReviewerTerminal={onOpenReviewerTerminal} session={reviewSession} />);

		await userEvent.click(await screen.findByRole("button", { name: /run review/i }));

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/reviews/trigger", {
				params: { path: { sessionId: "sess-1" } },
			}),
		);
		expect(onOpenReviewerTerminal).toHaveBeenCalledWith({ handleId: "reviewer-pane", harness: "codex" });
	});

	it("shows an up-to-date notice instead of opening the terminal when the backend reuses a run", async () => {
		mockCommonGets([approvedReview], "reviewer-pane");
		postMock.mockResolvedValue({
			response: { status: 200 },
			data: {
				reviewerHandleId: "reviewer-pane",
				review: approvedReview,
			},
		});
		const onOpenReviewerTerminal = vi.fn();

		renderWithQuery(<SessionInspector onOpenReviewerTerminal={onOpenReviewerTerminal} session={reviewSession} />);

		await userEvent.click(await screen.findByRole("button", { name: /re-run review/i }));

		expect(await screen.findByText("Review is already up to date for this commit.")).toBeInTheDocument();
		expect(onOpenReviewerTerminal).not.toHaveBeenCalled();
	});

	it("shows an approved review and opens its terminal", async () => {
		mockCommonGets([approvedReview], "reviewer-pane");
		const onOpenReviewerTerminal = vi.fn();

		renderWithQuery(<SessionInspector onOpenReviewerTerminal={onOpenReviewerTerminal} session={reviewSession} />);

		await waitFor(() => expect(screen.getAllByText("Approved").length).toBeGreaterThan(0));
		await userEvent.click(screen.getByRole("button", { name: /open terminal/i }));

		expect(onOpenReviewerTerminal).toHaveBeenCalledWith({ handleId: "reviewer-pane", harness: "codex" });
	});
});
