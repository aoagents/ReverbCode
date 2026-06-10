import { renderHook, waitFor } from "@testing-library/react";
import { act } from "react";
import type { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getStatusMock, onStatusMock, removeStatusMock, connectMock, stopTransportMock, setApiBaseUrlMock } = vi.hoisted(
	() => ({
		getStatusMock: vi.fn(),
		onStatusMock: vi.fn(),
		removeStatusMock: vi.fn(),
		connectMock: vi.fn(),
		stopTransportMock: vi.fn(),
		setApiBaseUrlMock: vi.fn(),
	}),
);

vi.mock("../lib/bridge", () => ({
	aoBridge: { daemon: { getStatus: getStatusMock, onStatus: onStatusMock } },
}));

vi.mock("../lib/event-transport", () => ({
	createEventTransport: vi.fn(() => ({ connect: connectMock })),
}));

vi.mock("../lib/api-client", () => ({
	setApiBaseUrl: setApiBaseUrlMock,
}));

import { useDaemonStatus } from "./useDaemonStatus";

type DaemonStatus = { state: "starting" | "ready" | "stopped" | "error"; port?: number; message?: string };

function fakeQueryClient(): QueryClient {
	return { invalidateQueries: vi.fn() } as unknown as QueryClient;
}

beforeEach(() => {
	getStatusMock.mockReset().mockResolvedValue({ state: "stopped" });
	onStatusMock.mockReset().mockReturnValue(removeStatusMock);
	removeStatusMock.mockReset();
	connectMock.mockReset().mockReturnValue(stopTransportMock);
	stopTransportMock.mockReset();
	setApiBaseUrlMock.mockReset();
});

describe("useDaemonStatus", () => {
	it("applies the initial status, points REST at the reported port, and connects the transport", async () => {
		getStatusMock.mockResolvedValue({ state: "ready", port: 3037 });
		const queryClient = fakeQueryClient();

		const { result } = renderHook(() => useDaemonStatus(queryClient));

		await waitFor(() => expect(result.current).toEqual({ state: "ready", port: 3037 }));
		expect(setApiBaseUrlMock).toHaveBeenCalledWith("http://127.0.0.1:3037");
		expect(connectMock).toHaveBeenCalledTimes(1);
		// Refetching is the (debounced) event transport's job — no direct invalidate.
		expect(queryClient.invalidateQueries).not.toHaveBeenCalled();
	});

	it("does not touch the base URL for statuses without a port", async () => {
		getStatusMock.mockResolvedValue({ state: "stopped", message: "daemon not configured" });
		const queryClient = fakeQueryClient();

		const { result } = renderHook(() => useDaemonStatus(queryClient));

		await waitFor(() => expect(result.current.message).toBe("daemon not configured"));
		expect(setApiBaseUrlMock).not.toHaveBeenCalled();
	});

	it("applies pushed status events from the bridge", async () => {
		const queryClient = fakeQueryClient();
		const { result } = renderHook(() => useDaemonStatus(queryClient));
		await waitFor(() => expect(onStatusMock).toHaveBeenCalled());
		const pushStatus = onStatusMock.mock.calls[0][0] as (status: DaemonStatus) => void;

		act(() => pushStatus({ state: "ready", port: 4555 }));

		expect(result.current).toEqual({ state: "ready", port: 4555 });
		expect(setApiBaseUrlMock).toHaveBeenCalledWith("http://127.0.0.1:4555");
	});

	it("still connects the transport when the initial IPC status call fails", async () => {
		getStatusMock.mockRejectedValue(new Error("ipc unavailable"));
		const queryClient = fakeQueryClient();

		const { result } = renderHook(() => useDaemonStatus(queryClient));

		await waitFor(() => expect(connectMock).toHaveBeenCalledTimes(1));
		expect(result.current).toEqual({ state: "stopped" });
	});

	it("tears down the transport and the status listener on unmount", async () => {
		const queryClient = fakeQueryClient();
		const { unmount } = renderHook(() => useDaemonStatus(queryClient));
		await waitFor(() => expect(connectMock).toHaveBeenCalledTimes(1));

		unmount();

		expect(stopTransportMock).toHaveBeenCalledTimes(1);
		expect(removeStatusMock).toHaveBeenCalledTimes(1);
	});
});
