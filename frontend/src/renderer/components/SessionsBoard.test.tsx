import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

const { navigateMock, workspaces } = vi.hoisted(() => {
	const session = (id: string, status: WorkspaceSession["status"], title: string): WorkspaceSession => ({
		id,
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title,
		provider: "claude-code",
		kind: "worker",
		status,
		branch: `session/${id}`,
		updatedAt: "2026-06-10T16:15:04Z",
		prs: [],
	});
	const workspaces: WorkspaceSummary[] = [
		{
			id: "proj-1",
			name: "my-app",
			path: "/repo/my-app",
			sessions: [
				session("sess-pending", "review_pending", "waiting on review"),
				session("sess-working", "working", "actively coding"),
				session("sess-idle", "idle", "awaiting next prompt"),
				session("sess-input", "needs_input", "blocked on decision"),
			],
		},
		{
			id: "proj-no-idle",
			name: "my-app-no-idle",
			path: "/repo/my-app-no-idle",
			sessions: [
				session("sess-working-2", "working", "building feature"),
				session("sess-input-2", "needs_input", "blocked elsewhere"),
			].map((s) => ({ ...s, workspaceId: "proj-no-idle", workspaceName: "my-app-no-idle" })),
		},
	];
	return { navigateMock: vi.fn(), workspaces };
});

vi.mock("../hooks/useSessionScmSummary", () => ({
	useSessionScmSummary: () => ({ data: undefined }),
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: workspaces, isError: false, isLoading: false }),
	workspaceQueryKey: ["workspaces"],
}));

vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return { ...actual, useNavigate: () => navigateMock };
});

import { SessionsBoard } from "./SessionsBoard";

function renderWithProviders(node: ReactNode) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(<QueryClientProvider client={queryClient}>{node}</QueryClientProvider>);
}

beforeEach(() => {
	navigateMock.mockReset();
});

describe("SessionsBoard", () => {
	it("keeps the first pending column and splits it into working and idle sections", () => {
		renderWithProviders(<SessionsBoard />);

		const pendingColumn = screen.getByRole("region", { name: "Pending sessions" });
		expect(within(pendingColumn).getByText("awaiting next prompt")).toBeInTheDocument();
		expect(within(pendingColumn).getByText("actively coding")).toBeInTheDocument();
		expect(within(pendingColumn).queryByText("blocked on decision")).not.toBeInTheDocument();

		const sections = within(pendingColumn).getAllByRole("region");
		expect(sections).toHaveLength(2);
		expect(sections[0]).toHaveAccessibleName("Working sessions in pending column");
		expect(sections[1]).toHaveAccessibleName("Idle sessions in pending column");

		const idleSection = screen.getByRole("region", { name: "Idle sessions in pending column" });
		expect(within(idleSection).getByText("awaiting next prompt")).toBeInTheDocument();
		expect(within(idleSection).queryByText("actively coding")).not.toBeInTheDocument();

		const workingSection = screen.getByRole("region", { name: "Working sessions in pending column" });
		expect(within(workingSection).getByText("actively coding")).toBeInTheDocument();
		expect(within(workingSection).queryByText("awaiting next prompt")).not.toBeInTheDocument();

		const inReview = screen.getByRole("region", { name: "In review sessions" });
		expect(within(inReview).getByText("waiting on review")).toBeInTheDocument();

		const needsYou = screen.getByRole("region", { name: "Needs you sessions" });
		expect(within(needsYou).getByText("blocked on decision")).toBeInTheDocument();
		expect(within(needsYou).queryByText("awaiting next prompt")).not.toBeInTheDocument();
	});

	it("collapses the idle section to a quiet count row when no sessions are idle", () => {
		renderWithProviders(<SessionsBoard projectId="proj-no-idle" />);

		const idleSection = screen.getByRole("region", { name: "Idle sessions in pending column" });
		expect(within(idleSection).getByText("Idle")).toBeInTheDocument();
		expect(within(idleSection).getByText("0")).toBeInTheDocument();
		expect(within(idleSection).queryByText("No sessions")).not.toBeInTheDocument();
	});
});
