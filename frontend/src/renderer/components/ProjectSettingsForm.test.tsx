import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, postMock, putMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	putMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: getMock,
		POST: postMock,
		PUT: putMock,
	},
	apiErrorMessage: (error: unknown) => {
		if (error instanceof Error) return error.message;
		if (typeof error === "object" && error !== null && "message" in error) {
			return String((error as { message: unknown }).message);
		}
		return "Request failed";
	},
}));

import { ProjectSettingsForm } from "./ProjectSettingsForm";

function renderSettings(projectId = "proj-1") {
	const queryClient = new QueryClient({
		defaultOptions: {
			queries: { retry: false },
			mutations: { retry: false },
		},
	});
	render(
		<QueryClientProvider client={queryClient}>
			<ProjectSettingsForm projectId={projectId} />
		</QueryClientProvider>,
	);
	return queryClient;
}

async function chooseOption(trigger: HTMLElement, optionName: string) {
	await userEvent.click(trigger);
	await userEvent.click(await screen.findByRole("option", { name: optionName }));
}

let projectResponse: unknown;
let agentsResponse: unknown;
let orchestratorsResponse: unknown;

beforeEach(() => {
	getMock.mockReset();
	postMock.mockReset();
	putMock.mockReset();
	postMock.mockResolvedValue({ data: { orchestrator: { id: "proj-1-orchestrator", projectId: "proj-1" } }, error: undefined });
	putMock.mockResolvedValue({ data: { project: {} }, error: undefined });
	projectResponse = undefined;
	agentsResponse = undefined;
	orchestratorsResponse = { data: { sessions: [] }, error: undefined };
});

describe("ProjectSettingsForm", () => {
	it("loads the current project settings and saves the exposed fields without dropping hidden config", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				defaultBranch: "develop",
				sessionPrefix: "po",
				env: { FOO: "bar" },
				symlinks: [".env"],
				postCreate: ["npm install"],
				worker: {
					agent: "codex",
					agentConfig: { model: "worker-model" },
				},
				orchestrator: { agent: "claude-code" },
				agentConfig: {
					model: "claude-opus-4-5",
					permissions: "auto",
				},
			},
		});
		mockAgents({
			supported: [
				{ id: "claude-code", label: "Claude Code" },
				{ id: "codex", label: "Codex" },
				{ id: "goose", label: "Goose" },
				{ id: "opencode", label: "OpenCode" },
			],
			installed: [
				{ id: "claude-code", label: "Claude Code" },
				{ id: "codex", label: "Codex" },
				{ id: "goose", label: "Goose" },
				{ id: "opencode", label: "OpenCode" },
			],
			counts: { supported: 4, installed: 4 },
		});
		mockGetResponses();

		renderSettings();

		expect(await screen.findByText("git@github.com:acme/project-one.git")).toBeInTheDocument();
		expect(screen.getByText("4 of 4 supported agents installed on this machine.")).toBeInTheDocument();
		expect(screen.getByLabelText("Default branch")).toHaveValue("develop");
		expect(screen.getByLabelText("Session prefix")).toHaveValue("po");
		expect(screen.getByLabelText("Model override")).toHaveValue("claude-opus-4-5");

		const workerAgent = screen.getByRole("combobox", { name: "Default worker agent" });
		const orchestratorAgent = screen.getByRole("combobox", { name: "Default orchestrator agent" });
		const permissionMode = screen.getByRole("combobox", { name: "Permission mode" });
		expect(workerAgent).toHaveTextContent("Codex");
		expect(orchestratorAgent).toHaveTextContent("Claude Code");
		expect(permissionMode).toHaveTextContent("Auto");

		await userEvent.clear(screen.getByLabelText("Default branch"));
		await userEvent.type(screen.getByLabelText("Default branch"), "release");
		await userEvent.clear(screen.getByLabelText("Session prefix"));
		await userEvent.type(screen.getByLabelText("Session prefix"), "rel");
		await userEvent.clear(screen.getByLabelText("Model override"));
		await userEvent.type(screen.getByLabelText("Model override"), "gpt-5-codex");
		await chooseOption(workerAgent, "OpenCode");
		await chooseOption(orchestratorAgent, "Goose");
		await chooseOption(permissionMode, "Bypass permissions");

		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(getMock).toHaveBeenCalledWith("/api/v1/orchestrators");
		expect(postMock).toHaveBeenCalledWith("/api/v1/orchestrators", {
			body: { projectId: "proj-1", clean: true },
		});
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}/config", {
			params: { path: { id: "proj-1" } },
			body: {
				config: {
					defaultBranch: "release",
					sessionPrefix: "rel",
					env: { FOO: "bar" },
					symlinks: [".env"],
					postCreate: ["npm install"],
					worker: {
						agent: "opencode",
						agentConfig: { model: "worker-model" },
					},
					orchestrator: { agent: "goose" },
					agentConfig: {
						model: "gpt-5-codex",
						permissions: "bypass-permissions",
					},
				},
			},
		});
		expect(await screen.findByText("Saved. Orchestrator restarted.")).toBeInTheDocument();
	});

	it("does not restart the orchestrator when unrelated settings change", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				defaultBranch: "main",
				orchestrator: { agent: "codex" },
			},
		});
		mockAgents({
			supported: [{ id: "codex", label: "Codex" }],
			installed: [{ id: "codex", label: "Codex" }],
			counts: { supported: 1, installed: 1 },
		});
		mockGetResponses();

		renderSettings();

		await userEvent.clear(await screen.findByLabelText("Default branch"));
		await userEvent.type(screen.getByLabelText("Default branch"), "release");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(postMock).not.toHaveBeenCalled();
		expect(await screen.findByText("Saved.")).toBeInTheDocument();
	});

	it("blocks orchestrator agent changes while the project orchestrator is active", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				orchestrator: { agent: "claude-code" },
			},
		});
		mockAgents({
			supported: [
				{ id: "claude-code", label: "Claude Code" },
				{ id: "codex", label: "Codex" },
			],
			installed: [
				{ id: "claude-code", label: "Claude Code" },
				{ id: "codex", label: "Codex" },
			],
			counts: { supported: 2, installed: 2 },
		});
		mockOrchestrators([
			{
				id: "proj-1-orchestrator",
				projectId: "proj-1",
				kind: "orchestrator",
				status: "working",
				isTerminated: false,
			},
		]);
		mockGetResponses();

		renderSettings();

		await chooseOption(await screen.findByRole("combobox", { name: "Default orchestrator agent" }), "Codex");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		expect(
			await screen.findByText("Orchestrator is currently active. Wait until it is idle before switching agents."),
		).toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("allows orchestrator agent changes when the current orchestrator is idle", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				orchestrator: { agent: "claude-code" },
			},
		});
		mockAgents({
			supported: [
				{ id: "claude-code", label: "Claude Code" },
				{ id: "codex", label: "Codex" },
			],
			installed: [
				{ id: "claude-code", label: "Claude Code" },
				{ id: "codex", label: "Codex" },
			],
			counts: { supported: 2, installed: 2 },
		});
		mockOrchestrators([
			{
				id: "proj-1-orchestrator",
				projectId: "proj-1",
				kind: "orchestrator",
				status: "idle",
				isTerminated: false,
			},
		]);
		mockGetResponses();

		renderSettings();

		await chooseOption(await screen.findByRole("combobox", { name: "Default orchestrator agent" }), "Codex");
		await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(postMock).toHaveBeenCalledWith("/api/v1/orchestrators", {
			body: { projectId: "proj-1", clean: true },
		});
		expect(await screen.findByText("Saved. Orchestrator restarted.")).toBeInTheDocument();
	});

	it("keeps a configured but missing agent visible with a warning", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "aider" },
				orchestrator: { agent: "codex" },
			},
		});
		mockAgents({
			supported: [
				{ id: "aider", label: "Aider" },
				{ id: "codex", label: "Codex" },
			],
			installed: [{ id: "codex", label: "Codex" }],
			counts: { supported: 2, installed: 1 },
		});
		mockGetResponses();

		renderSettings();

		expect(await screen.findByText("Aider is configured but was not detected on this machine.")).toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Default worker agent" })).toHaveTextContent("Aider");
		await userEvent.click(screen.getByRole("combobox", { name: "Default orchestrator agent" }));
		expect(screen.queryByRole("option", { name: "Aider" })).not.toBeInTheDocument();
	});

	it("disables agent dropdowns when no supported agents are installed", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
		});
		mockAgents({
			supported: [{ id: "codex", label: "Codex" }],
			installed: [],
			counts: { supported: 1, installed: 0 },
		});
		mockGetResponses();

		renderSettings();

		expect(await screen.findByText("No supported agent runtime was detected.")).toBeInTheDocument();
		expect(screen.getByRole("combobox", { name: "Default worker agent" })).toBeDisabled();
		expect(screen.getByRole("combobox", { name: "Default orchestrator agent" })).toBeDisabled();
	});

	it("shows the daemon validation message when save fails", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
		});
		mockAgents({
			supported: [{ id: "codex", label: "Codex" }],
			installed: [{ id: "codex", label: "Codex" }],
			counts: { supported: 1, installed: 1 },
		});
		mockGetResponses();
		putMock.mockResolvedValue({
			data: undefined,
			error: { message: "invalid permissions" },
		});

		renderSettings();

		await userEvent.click(await screen.findByRole("button", { name: "Save changes" }));

		expect(await screen.findByText("invalid permissions")).toBeInTheDocument();
		expect(screen.queryByText("Saved.")).not.toBeInTheDocument();
	});
});

function mockProject(project: unknown) {
	projectResponse = {
		data: {
			status: "ok",
			project,
		},
		error: undefined,
	};
}

function mockAgents(agents: unknown) {
	agentsResponse = {
		data: agents,
		error: undefined,
	};
}

function mockOrchestrators(sessions: unknown[]) {
	orchestratorsResponse = {
		data: { sessions },
		error: undefined,
	};
}

function mockGetResponses() {
	getMock.mockImplementation((path: string) => {
		if (path === "/api/v1/agents") return Promise.resolve(agentsResponse);
		if (path === "/api/v1/orchestrators") return Promise.resolve(orchestratorsResponse);
		return Promise.resolve(projectResponse);
	});
}
