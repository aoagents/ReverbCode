import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

// Drives the real useWorkspaceQuery + real Board / PR-page consumers end to end
// for a normal project, mocking only the HTTP client and the router. Proves the
// #251 fix: PR facts fetched from /pr flow through the shared workspace cache
// into every consumer.
const { getMock, navigateMock } = vi.hoisted(() => ({ getMock: vi.fn(), navigateMock: vi.fn() }));

vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: vi.fn() },
	apiErrorMessage: (e: unknown) => (e instanceof Error ? e.message : "error"),
}));

vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return { ...actual, useNavigate: () => navigateMock };
});

import { SessionsBoard } from "../../components/SessionsBoard";
import { PullRequestsPage } from "../../components/PullRequestsPage";

// One ordinary project with one worker session that has an open PR (#278).
function respondWithProjectAndPR() {
	getMock.mockImplementation(async (url: string, options?: { params?: { path?: { sessionId?: string } } }) => {
		if (url === "/api/v1/projects") {
			return { data: { projects: [{ id: "proj-1", name: "my-app", path: "/repo/my-app" }] }, error: undefined };
		}
		if (url === "/api/v1/sessions") {
			return {
				data: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							displayName: "fix the bug",
							harness: "claude-code",
							status: "pr_open",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			};
		}
		if (url === "/api/v1/sessions/{sessionId}/pr") {
			expect(options?.params?.path?.sessionId).toBe("sess-1");
			return {
				data: {
					sessionId: "sess-1",
					prs: [
						{
							number: 278,
							state: "open",
							url: "https://github.com/aoagents/ReverbCode/pull/278",
							ci: "passing",
							review: "approved",
							mergeability: "clean",
							reviewComments: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
				error: undefined,
			};
		}
		throw new Error(`unexpected GET ${url}`);
	});
}

function renderWithProviders(node: ReactNode) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(<QueryClientProvider client={queryClient}>{node}</QueryClientProvider>);
}

beforeEach(() => {
	getMock.mockReset();
	navigateMock.mockReset();
	respondWithProjectAndPR();
});

describe("PR hydration for a normal project (#251)", () => {
	it("renders the PR on the Board card instead of 'no PR yet'", async () => {
		renderWithProviders(<SessionsBoard />);

		expect(await screen.findByText("PR #278 · open")).toBeInTheDocument();
		expect(screen.queryByText("no PR yet")).not.toBeInTheDocument();
	});

	it("lists the session on the PR page instead of being empty", async () => {
		renderWithProviders(<PullRequestsPage />);

		expect(await screen.findByText("#278")).toBeInTheDocument();
		expect(screen.queryByText("No open pull requests.")).not.toBeInTheDocument();
		expect(screen.getByText("fix the bug")).toBeInTheDocument();
	});
});
