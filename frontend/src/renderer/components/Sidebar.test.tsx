import { SidebarProvider } from "@/components/ui/sidebar";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Sidebar } from "./Sidebar";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

const { navigateMock } = vi.hoisted(() => ({ navigateMock: vi.fn() }));

vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return {
		...actual,
		useNavigate: () => navigateMock,
		useParams: () => ({}),
		useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) =>
			select({ location: { pathname: "/" } }),
	};
});

const workspace: WorkspaceSummary = {
	id: "proj-1",
	name: "Project One",
	path: "/repo/project-one",
	sessions: [],
};

function workerSession(overrides: Partial<WorkspaceSession>): WorkspaceSession {
	return {
		id: "reverbcode-2",
		workspaceId: "proj-1",
		workspaceName: "Project One",
		title: "reverbcode-2",
		provider: "claude-code",
		kind: "worker",
		branch: "session/reverbcode-2",
		status: "working",
		updatedAt: "2026-06-17T00:00:00Z",
		...overrides,
	};
}

function renderSidebar(onRemoveProject = vi.fn().mockResolvedValue(undefined)) {
	render(
		<SidebarProvider>
			<Sidebar
				daemonStatus={{ state: "running" }}
				onCreateProject={vi.fn()}
				onRemoveProject={onRemoveProject}
				workspaces={[workspace]}
			/>
		</SidebarProvider>,
	);
	return onRemoveProject;
}

beforeEach(() => {
	navigateMock.mockReset();
	vi.spyOn(window, "confirm").mockReturnValue(true);
	vi.spyOn(window, "alert").mockImplementation(() => undefined);
});

afterEach(() => {
	vi.restoreAllMocks();
});

describe("Sidebar", () => {
	it("confirms project removal before calling the remove handler", async () => {
		const user = userEvent.setup();
		const onRemoveProject = renderSidebar();

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));

		expect(window.confirm).toHaveBeenCalledWith(
			"Remove project Project One? This stops its live sessions and removes it from the sidebar, but keeps the repository folder and stored history on disk.",
		);
		await waitFor(() => expect(onRemoveProject).toHaveBeenCalledTimes(1));
	});

	it("does not remove the project when confirmation is cancelled", async () => {
		vi.mocked(window.confirm).mockReturnValue(false);
		const user = userEvent.setup();
		const onRemoveProject = renderSidebar();

		await user.click(screen.getByLabelText("Project actions for Project One"));
		await user.click(await screen.findByRole("menuitem", { name: "Remove project" }));

		expect(onRemoveProject).not.toHaveBeenCalled();
	});

	it("does not repeat the session id under the title when the title is the id", () => {
		const sessions = [workerSession({ id: "reverbcode-2", title: "reverbcode-2" })];
		render(
			<SidebarProvider>
				<Sidebar
					daemonStatus={{ state: "running" }}
					onCreateProject={vi.fn()}
					onRemoveProject={vi.fn()}
					workspaces={[{ ...workspace, sessions }]}
				/>
			</SidebarProvider>,
		);

		// Title renders once; the id subtitle is suppressed because it would duplicate it.
		expect(screen.getAllByText("reverbcode-2")).toHaveLength(1);
	});

	it("shows the session id under a distinct display name", () => {
		const sessions = [workerSession({ id: "reverbcode-2", title: "fix the sidebar" })];
		render(
			<SidebarProvider>
				<Sidebar
					daemonStatus={{ state: "running" }}
					onCreateProject={vi.fn()}
					onRemoveProject={vi.fn()}
					workspaces={[{ ...workspace, sessions }]}
				/>
			</SidebarProvider>,
		);

		expect(screen.getByText("fix the sidebar")).toBeInTheDocument();
		expect(screen.getByText("reverbcode-2")).toBeInTheDocument();
	});

	it("hides the worker count in every state that reveals project actions", () => {
		renderSidebar();

		const projectRow = screen.getByText("Project One").closest("button");
		const count = screen.getByText("0");

		if (!projectRow) throw new Error("Project row button not found");
		expect(projectRow).toHaveClass("group-hover/menu-item:pr-[34px]");
		expect(projectRow).toHaveClass("group-focus-within/menu-item:pr-[34px]");
		expect(projectRow).toHaveClass("group-has-data-[state=open]/menu-item:pr-[34px]");
		expect(count).toHaveClass("group-hover/menu-item:opacity-0");
		expect(count).toHaveClass("group-focus-within/menu-item:opacity-0");
		expect(count).toHaveClass("group-has-data-[state=open]/menu-item:opacity-0");
	});
});
