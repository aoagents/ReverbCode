import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SpawnWorkerModal } from "./SpawnWorkerModal";
import { TooltipProvider } from "./ui/tooltip";
import type { WorkspaceSummary } from "../types/workspace";

const workspaces: WorkspaceSummary[] = [{ id: "proj-1", name: "my-app", path: "/p", type: "main", sessions: [] }];

function renderModal(onCreateTask = vi.fn().mockResolvedValue(undefined), onOpenChange = () => undefined) {
	render(
		<TooltipProvider>
			<SpawnWorkerModal
				open
				onOpenChange={onOpenChange}
				workspaces={workspaces}
				defaultProjectId="proj-1"
				onCreateTask={onCreateTask}
			/>
		</TooltipProvider>,
	);
	return onCreateTask;
}

describe("SpawnWorkerModal", () => {
	it("requires a non-empty prompt before it can spawn", () => {
		const onCreateTask = renderModal();
		expect(screen.getByRole("button", { name: /Spawn worker/ })).toBeDisabled();
		expect(onCreateTask).not.toHaveBeenCalled();
	});

	it("keeps the modal open and shows the daemon error when the spawn fails", async () => {
		const user = userEvent.setup();
		const onOpenChange = vi.fn();
		let rejectSpawn!: (reason: Error) => void;
		const onCreateTask = vi.fn(
			() =>
				new Promise<void>((_, reject) => {
					rejectSpawn = reject;
				}),
		);
		renderModal(onCreateTask, onOpenChange);

		await user.type(await screen.findByLabelText("Prompt"), "do the thing");
		await user.click(screen.getByRole("button", { name: /Spawn worker/ }));
		expect(screen.getByRole("button", { name: /Spawn worker/ })).toBeDisabled();

		rejectSpawn(new Error("branch already checked out at ~/Projects/skills"));

		expect(await screen.findByRole("alert")).toHaveTextContent("branch already checked out at ~/Projects/skills");
		expect(onOpenChange).not.toHaveBeenCalled();
		expect(screen.getByLabelText("Prompt")).toHaveValue("do the thing");

		onCreateTask.mockResolvedValueOnce(undefined);
		await user.click(screen.getByRole("button", { name: /Spawn worker/ }));
		await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
		expect(onCreateTask).toHaveBeenCalledTimes(2);
	});
});
