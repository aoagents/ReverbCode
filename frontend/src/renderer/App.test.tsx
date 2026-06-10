import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { useUiStore } from "./stores/ui-store";

const { postMock, mockData } = vi.hoisted(() => ({
	postMock: vi.fn(),
	mockData: {
		projectsError: undefined as Error | undefined,
		projects: [] as { id: string; name: string; path: string; sessionPrefix: string }[],
		sessions: [] as {
			id: string;
			projectId: string;
			displayName?: string;
			harness?: string;
			status: string;
			isTerminated: boolean;
			updatedAt: string;
		}[],
	},
}));

vi.mock("./lib/api-client", () => ({
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	setApiBaseUrl: () => undefined,
	subscribeApiBaseUrl: () => () => undefined,
	apiClient: {
		GET: vi.fn(async (url: string) => {
			if (url === "/api/v1/projects") {
				if (mockData.projectsError) return { data: undefined, error: mockData.projectsError };
				return { data: { projects: mockData.projects }, error: undefined };
			}
			if (url === "/api/v1/sessions") {
				return { data: { sessions: mockData.sessions }, error: undefined };
			}
			return { data: undefined, error: new Error(`unexpected GET ${url}`) };
		}),
		POST: postMock,
	},
}));

vi.mock("./components/TerminalPane", () => ({
	TerminalPane: () => <div>Terminal scaffold</div>,
}));

function renderApp() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<App />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	postMock.mockReset();
	mockData.projectsError = undefined;
	mockData.projects = [];
	mockData.sessions = [];
	window.localStorage.clear();
	useUiStore.setState({
		view: "orchestrator",
		workbenchTab: "changes",
		isSidebarOpen: true,
		selectedSessionId: null,
		selectedWorkspaceId: null,
		theme: "dark",
	});
});

test("renders the orchestrator anchor and empty state", async () => {
	renderApp();

	expect(await screen.findByRole("button", { name: "Orchestrator" })).toBeInTheDocument();
	expect(await screen.findByText("No projects yet.")).toBeInTheDocument();
});

test("surfaces project load failures instead of the empty state", async () => {
	mockData.projectsError = new TypeError("Failed to fetch");

	renderApp();

	expect(await screen.findByText("Could not load projects.", undefined, { timeout: 3_000 })).toBeInTheDocument();
	expect(await screen.findByText("Failed to fetch")).toBeInTheDocument();
	expect(screen.queryByText("No projects yet.")).not.toBeInTheDocument();
});

test("renders projects and sessions from the API", async () => {
	mockData.projects = [{ id: "proj-1", name: "my-app", path: "/home/me/my-app", sessionPrefix: "" }];
	mockData.sessions = [
		{
			id: "sess-1",
			projectId: "proj-1",
			displayName: "fix-bug",
			harness: "claude-code",
			status: "working",
			isTerminated: false,
			updatedAt: new Date().toISOString(),
		},
	];

	renderApp();

	expect(await screen.findByRole("button", { name: "Select my-app" })).toBeInTheDocument();
	expect(await screen.findByRole("button", { name: "fix-bug" })).toBeInTheDocument();
});

test("adds a project from the rail", async () => {
	const user = userEvent.setup();
	const bridge = window.ao;
	if (!bridge) throw new Error("test preload bridge is not installed");
	bridge.app.chooseDirectory = vi.fn(async () => "/Users/me/new-project");
	postMock.mockResolvedValueOnce({
		data: {
			project: {
				id: "new-project",
				name: "New Project",
				path: "/Users/me/new-project",
				repo: "git@example.com:new-project.git",
				defaultBranch: "main",
			},
		},
	});

	renderApp();

	await user.click(await screen.findByRole("button", { name: "New project" }));

	expect(bridge.app.chooseDirectory).toHaveBeenCalled();
	expect(postMock).toHaveBeenCalledWith("/api/v1/projects", { body: { path: "/Users/me/new-project" } });
	expect(await screen.findByText("New Project")).toBeInTheDocument();
});

test("spawns a worker from the New worker modal", async () => {
	const user = userEvent.setup();
	mockData.projects = [{ id: "proj-1", name: "my-app", path: "/home/me/my-app", sessionPrefix: "" }];
	postMock.mockResolvedValueOnce({
		data: {
			session: {
				id: "new-task",
				projectId: "proj-1",
				harness: "claude-code",
				branch: "main",
				isTerminated: false,
			},
		},
	});

	renderApp();

	// Wait for projects to load.
	await screen.findByRole("button", { name: "Select my-app" });

	// Open spawn modal from the orchestrator topbar.
	await user.click(screen.getByRole("button", { name: "New worker" }));
	await user.type(await screen.findByLabelText("Prompt"), "Make task creation work");
	await user.click(screen.getByRole("button", { name: /Spawn worker/ }));

	expect(postMock).toHaveBeenCalledWith("/api/v1/sessions", {
		body: {
			projectId: "proj-1",
			kind: "worker",
			harness: "claude-code",
			prompt: "Make task creation work",
			branch: "main",
		},
	});
});

test("surfaces an error when spawning fails", async () => {
	const user = userEvent.setup();
	mockData.projects = [{ id: "proj-1", name: "my-app", path: "/home/me/my-app", sessionPrefix: "" }];
	postMock.mockResolvedValueOnce({ error: new TypeError("Failed to fetch") });

	renderApp();

	await screen.findByRole("button", { name: "Select my-app" });

	await user.click(screen.getByRole("button", { name: "New worker" }));
	await user.type(await screen.findByLabelText("Prompt"), "Failing task");
	await user.click(screen.getByRole("button", { name: /Spawn worker/ }));

	expect(postMock).toHaveBeenCalledWith("/api/v1/sessions", {
		body: {
			projectId: "proj-1",
			kind: "worker",
			harness: "claude-code",
			prompt: "Failing task",
			branch: "main",
		},
	});
	expect(await screen.findByText("Failed to fetch")).toBeInTheDocument();
});
