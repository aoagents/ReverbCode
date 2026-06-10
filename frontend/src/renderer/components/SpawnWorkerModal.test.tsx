import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "./ui/tooltip";
import { SpawnWorkerModal } from "./SpawnWorkerModal";
import type { WorkspaceSummary } from "../types/workspace";

const workspaces: WorkspaceSummary[] = [{ id: "proj-1", name: "my-app", path: "/p", type: "main", sessions: [] }];

function renderModal(onCreateTask = vi.fn().mockResolvedValue(undefined)) {
  render(
    <TooltipProvider>
      <SpawnWorkerModal
        open
        onOpenChange={() => undefined}
        workspaces={workspaces}
        defaultProjectId="proj-1"
        onCreateTask={onCreateTask}
      />
    </TooltipProvider>,
  );
  return onCreateTask;
}

describe("SpawnWorkerModal", () => {
  // Regression: "Based on main" must NOT send branch:"main" — git refuses a
  // second worktree on a checked-out branch, so the daemon 409s. Omitting it
  // lets the daemon mint a fresh ao/<sessionId>.
  it("omits the base branch from the spawn payload", async () => {
    const user = userEvent.setup();
    const onCreateTask = renderModal();

    await user.type(await screen.findByLabelText("Prompt"), "do the thing");
    await user.click(screen.getByRole("button", { name: /Spawn worker/ }));

    expect(onCreateTask).toHaveBeenCalledWith(
      expect.objectContaining({ projectId: "proj-1", prompt: "do the thing", branch: undefined }),
    );
  });

  it("requires a non-empty prompt before it can spawn", async () => {
    const onCreateTask = renderModal();
    expect(screen.getByRole("button", { name: /Spawn worker/ })).toBeDisabled();
    expect(onCreateTask).not.toHaveBeenCalled();
  });
});
