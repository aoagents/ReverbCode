import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ImportOffer } from "./ImportOffer";

const { getMock, postMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: getMock,
		POST: postMock,
	},
	apiErrorMessage: (error: unknown, fallback = "Request failed") => {
		if (error instanceof Error) return error.message;
		if (typeof error === "object" && error !== null && "message" in error) {
			return String((error as { message: unknown }).message);
		}
		return fallback;
	},
}));

function renderOffer() {
	const queryClient = new QueryClient({
		defaultOptions: {
			queries: { retry: false },
			mutations: { retry: false },
		},
	});
	render(
		<QueryClientProvider client={queryClient}>
			<ImportOffer />
		</QueryClientProvider>,
	);
	return queryClient;
}

beforeEach(() => {
	getMock.mockReset();
	postMock.mockReset();
	getMock.mockResolvedValue({ data: { available: true, legacyRoot: "/home/u/.agent-orchestrator" }, error: undefined });
	postMock.mockResolvedValue({ data: { report: { projectsImported: 2 } }, error: undefined });
});

describe("ImportOffer", () => {
	it("shows the offer when the daemon reports an importable install", async () => {
		renderOffer();
		expect(await screen.findByText(/Import projects from your earlier AO/i)).toBeInTheDocument();
		expect(screen.getByText("/home/u/.agent-orchestrator")).toBeInTheDocument();
	});

	it("renders nothing when no install is available", async () => {
		getMock.mockResolvedValue({ data: { available: false, legacyRoot: "" }, error: undefined });
		renderOffer();
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(screen.queryByText(/Import projects from your earlier AO/i)).not.toBeInTheDocument();
	});

	it("runs the import on accept", async () => {
		renderOffer();
		await screen.findByText(/Import projects from your earlier AO/i);

		await userEvent.click(screen.getByRole("button", { name: "Import" }));

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock).toHaveBeenCalledWith("/api/v1/import");
		// On success the banner retires.
		await waitFor(() => expect(screen.queryByText(/Import projects from your earlier AO/i)).not.toBeInTheDocument());
	});

	it("dismisses without importing on decline", async () => {
		renderOffer();
		await screen.findByText(/Import projects from your earlier AO/i);

		await userEvent.click(screen.getByRole("button", { name: "Not now" }));

		expect(screen.queryByText(/Import projects from your earlier AO/i)).not.toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("surfaces the daemon error when the import fails", async () => {
		postMock.mockResolvedValue({ data: undefined, error: { message: "disk full" } });
		renderOffer();
		await screen.findByText(/Import projects from your earlier AO/i);

		await userEvent.click(screen.getByRole("button", { name: "Import" }));

		expect(await screen.findByText(/disk full/i)).toBeInTheDocument();
	});
});
