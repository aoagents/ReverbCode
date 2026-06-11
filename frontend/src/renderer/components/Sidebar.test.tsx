import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test, vi } from "vitest";
import { Sidebar } from "./Sidebar";
import { SidebarProvider } from "./ui/sidebar";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

// Selection comes from the router in production; the archive interactions
// under test only need stable params and a recordable navigate.
const { navigateMock } = vi.hoisted(() => ({ navigateMock: vi.fn() }));
vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
	useParams: () => ({}),
	useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => string }) =>
		select({ location: { pathname: "/" } }),
}));
vi.mock("../hooks/useEventsConnection", () => ({ useEventsConnection: () => "connected" }));

function worker(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: "sess-1",
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: "old-task",
		provider: "claude-code",
		kind: "worker",
		branch: "session/sess-1",
		status: "terminated",
		updatedAt: "2026-06-11T00:00:00Z",
		...overrides,
	};
}

function renderSidebar(workspaces: WorkspaceSummary[]) {
	const onArchiveSession = vi.fn(async () => {});
	const onUnarchiveSession = vi.fn(async () => {});
	render(
		<SidebarProvider defaultOpen>
			<Sidebar
				daemonStatus={{ state: "running" }}
				onArchiveSession={onArchiveSession}
				onCreateProject={vi.fn(async () => {})}
				onNewWorker={vi.fn()}
				onUnarchiveSession={onUnarchiveSession}
				workspaces={workspaces}
			/>
		</SidebarProvider>,
	);
	return { onArchiveSession, onUnarchiveSession };
}

test("archives a terminated worker from its row's context menu", async () => {
	const user = userEvent.setup();
	const { onArchiveSession } = renderSidebar([
		{ id: "proj-1", name: "my-app", path: "/p", sessions: [worker()], archivedSessions: [] },
	]);

	fireEvent.contextMenu(screen.getByRole("button", { name: "Open old-task" }));
	await user.click(await screen.findByRole("menuitem", { name: "Archive worker" }));

	expect(onArchiveSession).toHaveBeenCalledWith("sess-1");
});

test("running workers get no context menu", () => {
	renderSidebar([{ id: "proj-1", name: "my-app", path: "/p", sessions: [worker({ status: "working" })] }]);

	fireEvent.contextMenu(screen.getByRole("button", { name: "Open old-task" }));

	expect(screen.queryByRole("menu")).not.toBeInTheDocument();
});

test("archived workers sit behind the Archived disclosure and offer Unarchive", async () => {
	const user = userEvent.setup();
	const { onUnarchiveSession } = renderSidebar([
		{
			id: "proj-1",
			name: "my-app",
			path: "/p",
			sessions: [],
			archivedSessions: [worker({ archived: true })],
		},
	]);

	// Hidden until the disclosure expands; the project worker count ignores them.
	expect(screen.queryByRole("button", { name: "Open old-task" })).not.toBeInTheDocument();
	await user.click(screen.getByRole("button", { name: "Archived workers in my-app" }));

	fireEvent.contextMenu(screen.getByRole("button", { name: "Open old-task" }));
	await user.click(await screen.findByRole("menuitem", { name: "Unarchive worker" }));

	expect(onUnarchiveSession).toHaveBeenCalledWith("sess-1");
});
