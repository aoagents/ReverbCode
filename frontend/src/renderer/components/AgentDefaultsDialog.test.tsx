import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	putMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: getMock,
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

import { AgentDefaultsDialog } from "./AgentDefaultsDialog";

function renderDialog(open = false) {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	const onOpenChange = vi.fn();
	render(
		<QueryClientProvider client={queryClient}>
			<AgentDefaultsDialog daemonReady open={open} onOpenChange={onOpenChange} />
		</QueryClientProvider>,
	);
	return onOpenChange;
}

async function chooseOption(trigger: HTMLElement, optionName: string) {
	await userEvent.click(trigger);
	await userEvent.click(await screen.findByRole("option", { name: optionName }));
}

beforeEach(() => {
	getMock.mockReset();
	putMock.mockReset();
});

describe("AgentDefaultsDialog", () => {
	it("opens on first run and saves selected defaults", async () => {
		getMock.mockResolvedValue({
			data: { configured: false },
			error: undefined,
		});
		putMock.mockResolvedValue({
			data: {
				configured: true,
				defaultWorkerAgent: "codex",
				defaultOrchestratorAgent: "goose",
			},
			error: undefined,
		});
		const onOpenChange = renderDialog(false);

		expect(await screen.findByRole("dialog", { name: "Choose Default Agents" })).toBeInTheDocument();
		const save = screen.getByRole("button", { name: "Save defaults" });
		expect(save).toBeDisabled();

		await chooseOption(screen.getByRole("combobox", { name: "Worker agent" }), "codex");
		await chooseOption(screen.getByRole("combobox", { name: "Orchestrator agent" }), "goose");
		await userEvent.click(save);

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/settings/agents", {
			body: { defaultWorkerAgent: "codex", defaultOrchestratorAgent: "goose" },
		});
		expect(onOpenChange).toHaveBeenCalledWith(false);
	});

	it("stays hidden when configured and not explicitly opened", async () => {
		getMock.mockResolvedValue({
			data: {
				configured: true,
				defaultWorkerAgent: "codex",
				defaultOrchestratorAgent: "claude-code",
			},
			error: undefined,
		});
		renderDialog(false);

		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	});
});
