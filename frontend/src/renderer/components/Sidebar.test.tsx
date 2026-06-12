import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

// The Sidebar reads selection from the router; stub the three hooks it uses so
// the component renders without a full RouterProvider.
vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => vi.fn(),
	useParams: () => ({}),
	useRouterState: () => "/",
}));

import { TooltipProvider } from "./ui/tooltip";
import { SidebarProvider } from "./ui/sidebar";
import { Sidebar } from "./Sidebar";
import type { WorkspaceSummary } from "../types/workspace";

const workspaces: WorkspaceSummary[] = [{ id: "proj-1", name: "my-app", path: "/p", type: "main", sessions: [] }];

function renderSidebar(onRemoveProject = vi.fn().mockResolvedValue(undefined)) {
	render(
		<TooltipProvider>
			<SidebarProvider>
				<Sidebar
					daemonStatus={{ state: "running" }}
					workspaces={workspaces}
					onCreateProject={vi.fn().mockResolvedValue(undefined)}
					onRemoveProject={onRemoveProject}
				/>
			</SidebarProvider>
		</TooltipProvider>,
	);
	return onRemoveProject;
}

describe("Sidebar", () => {
	it("removes a project from the row kebab menu", async () => {
		const user = userEvent.setup();
		const onRemoveProject = renderSidebar();

		await user.click(screen.getByRole("button", { name: "Actions for my-app" }));
		await user.click(await screen.findByText("Remove project"));

		expect(onRemoveProject).toHaveBeenCalledWith("proj-1");
	});
});
