import { useEffect, useRef, useState } from "react";
import type { QueryClient } from "@tanstack/react-query";
import { aoBridge } from "../lib/bridge";
import { queryClient as defaultQueryClient } from "../lib/query-client";
import { createEventTransport } from "../lib/event-transport";
import { setApiBaseUrl } from "../lib/api-client";

type DaemonStatus = Awaited<ReturnType<typeof aoBridge.daemon.getStatus>>;
const STATUS_REFRESH_MS = 2_000;

export function useDaemonStatus(queryClient: QueryClient = defaultQueryClient) {
	const [status, setStatus] = useState<DaemonStatus>({ state: "stopped" });
	const statusRef = useRef(status);

	useEffect(() => {
		let active = true;
		let stopTransport: () => void = () => undefined;
		let refreshTimer: ReturnType<typeof setTimeout> | undefined;

		const clearRefresh = () => {
			if (refreshTimer) {
				clearTimeout(refreshTimer);
				refreshTimer = undefined;
			}
		};

		const refreshStatus = () => {
			clearRefresh();
			void aoBridge.daemon
				.getStatus()
				.then((nextStatus) => {
					if (active) applyStatus(nextStatus);
				})
				.catch(() => {
					// IPC unavailable (browser preview, broken preload): stay on the
					// last known status and keep the recovery loop alive.
				})
				.finally(() => {
					if (active && statusRef.current.state !== "ready") scheduleRefresh();
				});
		};

		const scheduleRefresh = () => {
			if (refreshTimer || !active) return;
			refreshTimer = setTimeout(refreshStatus, STATUS_REFRESH_MS);
		};

		const applyStatus = (nextStatus: DaemonStatus) => {
			// Only point REST at the new port; the workspace refetch is the event
			// transport's job (it invalidates, debounced, on every daemon status).
			statusRef.current = nextStatus;
			if (nextStatus.state === "ready" && nextStatus.port) {
				setApiBaseUrl(`http://127.0.0.1:${nextStatus.port}`);
				clearRefresh();
			} else {
				scheduleRefresh();
			}
			setStatus(nextStatus);
		};

		refreshStatus();
		const refreshOnFocus = () => {
			if (statusRef.current.state !== "ready") refreshStatus();
		};
		const refreshOnVisibility = () => {
			if (document.visibilityState === "visible") refreshOnFocus();
		};
		window.addEventListener("focus", refreshOnFocus);
		document.addEventListener("visibilitychange", refreshOnVisibility);

		void Promise.resolve().then(() => {
				if (active) stopTransport = createEventTransport(queryClient).connect();
			});

		const stopStatusListener = aoBridge.daemon.onStatus(applyStatus);

		return () => {
			active = false;
			clearRefresh();
			window.removeEventListener("focus", refreshOnFocus);
			document.removeEventListener("visibilitychange", refreshOnVisibility);
			stopTransport();
			stopStatusListener();
		};
	}, [queryClient]);

	return status;
}
