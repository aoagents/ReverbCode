import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { NewTaskDialog } from "./NewTaskDialog";

const { postMock, captureMock } = vi.hoisted(() => ({
	postMock: vi.fn(),
	captureMock: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		POST: postMock,
	},
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (typeof error === "object" && error !== null && "message" in error) {
			return String((error as { message: unknown }).message);
		}
		return fallback;
	},
}));

vi.mock("../lib/telemetry", () => ({
	captureRendererEvent: captureMock,
}));

beforeEach(() => {
	postMock.mockReset();
	captureMock.mockClear();
	postMock.mockResolvedValue({ data: { session: { id: "task-1" } }, error: undefined });
});

describe("NewTaskDialog", () => {
	it("starts a worker task with the entered title and brief", async () => {
		const onCreated = vi.fn();
		const onOpenChange = vi.fn();
		render(<NewTaskDialog open projectId="proj-1" onCreated={onCreated} onOpenChange={onOpenChange} />);

		await userEvent.type(screen.getByLabelText("Title"), "Fix fallback renderer");
		await userEvent.type(screen.getByLabelText("Brief"), "Restore the fallback renderer after WebGL init fails.");
		await userEvent.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions", {
			body: {
				projectId: "proj-1",
				kind: "worker",
				harness: "codex",
				issueId: "Fix fallback renderer",
				prompt: "Restore the fallback renderer after WebGL init fails.",
				branch: undefined,
			},
		});
		expect(onCreated).toHaveBeenCalledWith("task-1");
		expect(onOpenChange).toHaveBeenCalledWith(false);
	});

	it("requires both title and brief", async () => {
		render(<NewTaskDialog open projectId="proj-1" onCreated={vi.fn()} onOpenChange={vi.fn()} />);

		await userEvent.click(screen.getByRole("button", { name: "Start task" }));

		expect(await screen.findByText("Title and brief are required.")).toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalled();
		expect(captureMock).not.toHaveBeenCalled();
	});

	it("emits the task_create triad on success", async () => {
		render(<NewTaskDialog open projectId="proj-1" onCreated={vi.fn()} onOpenChange={vi.fn()} />);

		await userEvent.type(screen.getByLabelText("Title"), "Fix fallback renderer");
		await userEvent.type(screen.getByLabelText("Brief"), "Restore the fallback renderer.");
		await userEvent.click(screen.getByRole("button", { name: "Start task" }));

		await waitFor(() =>
			expect(captureMock).toHaveBeenCalledWith("ao.renderer.task_create_succeeded", { project_id: "proj-1" }),
		);
		expect(captureMock).toHaveBeenCalledWith("ao.renderer.task_create_requested", { project_id: "proj-1" });
		expect(captureMock).not.toHaveBeenCalledWith("ao.renderer.task_create_failed", expect.anything());
	});

	it("emits task_create_failed when the daemon rejects the task", async () => {
		postMock.mockResolvedValue({ data: undefined, error: { message: "no such project" } });
		render(<NewTaskDialog open projectId="proj-1" onCreated={vi.fn()} onOpenChange={vi.fn()} />);

		await userEvent.type(screen.getByLabelText("Title"), "Fix fallback renderer");
		await userEvent.type(screen.getByLabelText("Brief"), "Restore the fallback renderer.");
		await userEvent.click(screen.getByRole("button", { name: "Start task" }));

		expect(await screen.findByText("no such project")).toBeInTheDocument();
		expect(captureMock).toHaveBeenCalledWith("ao.renderer.task_create_failed", { project_id: "proj-1" });
		expect(captureMock).not.toHaveBeenCalledWith("ao.renderer.task_create_succeeded", expect.anything());
	});
});
