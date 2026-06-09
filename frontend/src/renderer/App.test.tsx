import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { beforeEach, vi } from "vitest";
import { App } from "./App";
import { useUiStore } from "./stores/ui-store";

const { postMock } = vi.hoisted(() => ({
  postMock: vi.fn(),
}));

vi.mock("./lib/api-client", () => ({
  apiClient: {
    GET: vi.fn(async () => ({ error: new Error("offline") })),
    POST: postMock,
  },
}));

vi.mock("./components/TerminalPane", () => ({
  TerminalPane: () => <div>Terminal scaffold</div>,
}));

beforeEach(() => {
  postMock.mockReset();
  window.localStorage.clear();
  useUiStore.setState({
    activePane: "sessions",
    isSidebarOpen: true,
    selectedSessionId: "ao-shell-scaffold",
    selectedWorkspaceId: "agent-orchestrator",
    theme: "dark",
  });
});

test("renders the desktop workbench scaffold", async () => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <App queryClient={queryClient} />
    </QueryClientProvider>,
  );

  expect(await screen.findAllByText("Desktop shell scaffold")).toHaveLength(2);
  expect(screen.queryByRole("button", { name: "New task" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /switch to .* theme/i })).not.toBeInTheDocument();
});

test("adds a project from the sidebar", async () => {
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
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <App queryClient={queryClient} />
    </QueryClientProvider>,
  );

  await user.click(await screen.findByRole("button", { name: "New project" }));

  expect(window.ao.app.chooseDirectory).toHaveBeenCalled();
  expect(postMock).toHaveBeenCalledWith("/api/v1/projects", {
    body: {
      path: "/Users/me/new-project",
    },
  });
  expect(await screen.findByText("New Project")).toBeInTheDocument();
});

test("starts a new task from a project", async () => {
  const user = userEvent.setup();
  postMock.mockResolvedValueOnce({
    data: {
      session: {
        id: "new-task",
        projectId: "agent-orchestrator",
        harness: "codex",
        branch: "codex/electron-stack-scaffold",
        isTerminated: false,
      },
    },
  });
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <App queryClient={queryClient} />
    </QueryClientProvider>,
  );

  await user.click(await screen.findByRole("button", { name: "Start task in agent-orchestrator-1" }));
  fireEvent.change(screen.getByLabelText("Prompt"), { target: { value: "Make task creation work" } });
  fireEvent.change(screen.getByLabelText("Branch"), { target: { value: "codex/electron-stack-scaffold" } });
  await user.click(screen.getByRole("button", { name: "Start task" }));

  expect(postMock).toHaveBeenCalledWith("/api/v1/sessions", {
    body: {
      projectId: "agent-orchestrator",
      kind: "worker",
      harness: "codex",
      prompt: "Make task creation work",
      branch: "codex/electron-stack-scaffold",
    },
  });
  expect(await screen.findAllByText("Make task creation work")).toHaveLength(2);
});

test("starts a dummy task when session creation cannot reach the daemon", async () => {
  const user = userEvent.setup();
  postMock.mockResolvedValueOnce({ error: new TypeError("Failed to fetch") });
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  render(
    <QueryClientProvider client={queryClient}>
      <App queryClient={queryClient} />
    </QueryClientProvider>,
  );

  await user.click(await screen.findByRole("button", { name: "Start task in agent-orchestrator-1" }));
  fireEvent.change(screen.getByLabelText("Prompt"), { target: { value: "Dummy fallback task" } });
  await user.click(screen.getByRole("button", { name: "Start task" }));

  expect(postMock).toHaveBeenCalledWith("/api/v1/sessions", {
    body: {
      projectId: "agent-orchestrator",
      kind: "worker",
      harness: "codex",
      prompt: "Dummy fallback task",
      branch: undefined,
    },
  });
  expect(await screen.findAllByText("Dummy fallback task")).toHaveLength(2);
});
