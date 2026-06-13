import { SidebarProvider } from "@/components/ui/sidebar";
import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Sidebar } from "./Sidebar";
import type { WorkspaceSummary } from "../types/workspace";

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

function renderSidebar() {
	render(
		<SidebarProvider>
			<Sidebar
				daemonStatus={{ state: "running" }}
				onCreateProject={vi.fn()}
				workspaces={[workspace]}
			/>
		</SidebarProvider>,
	);
}

beforeEach(() => {
	navigateMock.mockReset();
});

afterEach(() => {
	vi.restoreAllMocks();
});

describe("Sidebar", () => {
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
