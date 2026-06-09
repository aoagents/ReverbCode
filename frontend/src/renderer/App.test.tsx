import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { useUiStore } from "./stores/ui-store";

const { postMock } = vi.hoisted(() => ({
  postMock: vi.fn(),
}));

vi.mock("./lib/api-client", () => ({
  apiBaseUrl: "http://127.0.0.1:4317",
  apiClient: {
    GET: vi.fn(async () => ({ error: new Error("offline") })),
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
  window.localStorage.clear();
  useUiStore.setState({
    view: "orchestrator",
    workbenchTab: "changes",
    isSidebarOpen: true,
    selectedSessionId: null,
    selectedWorkspaceId: "agent-orchestrator",
    theme: "dark",
  });
});

test("renders the orchestrator-first workbench", async () => {
  renderApp();

  // The single pinned Orchestrator anchor + a name-only worker row from the fleet.
  expect(await screen.findByRole("button", { name: "Orchestrator" })).toBeInTheDocument();
  expect(await screen.findByRole("button", { name: "fix-webgl-fallback" })).toBeInTheDocument();
  // No legacy theme toggle.
  expect(screen.queryByRole("button", { name: /switch to .* theme/i })).not.toBeInTheDocument();
});

test("adds a project from the rail", async () => {
  const user = userEvent.setup();
  window.ao.app.chooseDirectory = vi.fn(async () => "/Users/me/new-project");
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

  expect(window.ao.app.chooseDirectory).toHaveBeenCalled();
  expect(postMock).toHaveBeenCalledWith("/api/v1/projects", { body: { path: "/Users/me/new-project" } });
  expect(await screen.findByText("New Project")).toBeInTheDocument();
});

test("spawns a worker from the New worker modal", async () => {
  const user = userEvent.setup();
  postMock.mockResolvedValueOnce({
    data: {
      session: {
        id: "new-task",
        projectId: "agent-orchestrator",
        harness: "claude-code",
        branch: "main",
        isTerminated: false,
      },
    },
  });

  renderApp();

  await user.click(await screen.findByRole("button", { name: "New worker" }));
  await user.type(await screen.findByLabelText("Prompt"), "Make task creation work");
  await user.click(screen.getByRole("button", { name: /Spawn worker/ }));

  expect(postMock).toHaveBeenCalledWith("/api/v1/sessions", {
    body: {
      projectId: "agent-orchestrator",
      kind: "worker",
      harness: "claude-code",
      prompt: "Make task creation work",
      branch: "main",
    },
  });
  expect(await screen.findAllByText("Make task creation work")).toHaveLength(2);
});

test("surfaces an error when spawning fails", async () => {
  const user = userEvent.setup();
  postMock.mockResolvedValueOnce({ error: new TypeError("Failed to fetch") });

  renderApp();

  await user.click(await screen.findByRole("button", { name: "New worker" }));
  await user.type(await screen.findByLabelText("Prompt"), "Failing task");
  await user.click(screen.getByRole("button", { name: /Spawn worker/ }));

  expect(postMock).toHaveBeenCalledWith("/api/v1/sessions", {
    body: {
      projectId: "agent-orchestrator",
      kind: "worker",
      harness: "claude-code",
      prompt: "Failing task",
      branch: "main",
    },
  });
  expect(await screen.findByText("Failed to fetch")).toBeInTheDocument();
});
