import { useEffect, useState } from "react";
import type { QueryClient } from "@tanstack/react-query";
import { aoBridge } from "../lib/bridge";
import { queryClient as defaultQueryClient } from "../lib/query-client";
import { createEventTransport } from "../lib/event-transport";
import { setApiBaseUrl } from "../lib/api-client";

type DaemonStatus = Awaited<ReturnType<typeof aoBridge.daemon.getStatus>>;

export function useDaemonStatus(queryClient: QueryClient = defaultQueryClient) {
	const [status, setStatus] = useState<DaemonStatus>({ state: "stopped" });

	useEffect(() => {
		let active = true;
		let stopTransport: () => void = () => undefined;
		const applyStatus = (nextStatus: DaemonStatus) => {
			// Only point REST at the new port; the workspace refetch is the event
			// transport's job (it invalidates, debounced, on every daemon status).
			if (nextStatus.port) {
				setApiBaseUrl(`http://127.0.0.1:${nextStatus.port}`);
			}
			setStatus(nextStatus);
		};

		void aoBridge.daemon
			.getStatus()
			.then((nextStatus) => {
				if (active) applyStatus(nextStatus);
			})
			.catch(() => {
				// IPC unavailable (browser preview, broken preload): stay "stopped";
				// REST against the default base URL still works where it can.
			})
			.then(() => {
				if (active) stopTransport = createEventTransport(queryClient).connect();
			});

		const stopStatusListener = aoBridge.daemon.onStatus(applyStatus);

		return () => {
			active = false;
			stopTransport();
			stopStatusListener();
		};
	}, [queryClient]);

	return status;
}
