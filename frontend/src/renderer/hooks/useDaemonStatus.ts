import { useEffect, useState } from "react";
import type { QueryClient } from "@tanstack/react-query";
import { aoBridge } from "../lib/bridge";
import { queryClient as defaultQueryClient } from "../lib/query-client";
import { createEventTransport } from "../lib/event-transport";
import { setApiBaseUrl } from "../lib/api-client";
import { workspaceQueryKey } from "./useWorkspaceQuery";

type DaemonStatus = Awaited<ReturnType<typeof aoBridge.daemon.getStatus>>;

export function useDaemonStatus(queryClient: QueryClient = defaultQueryClient) {
  const [status, setStatus] = useState<DaemonStatus>({ state: "stopped" });

  useEffect(() => {
    let active = true;
    let stopTransport: () => void = () => undefined;
    const applyStatus = (nextStatus: DaemonStatus) => {
      if (nextStatus.port) {
        setApiBaseUrl(`http://127.0.0.1:${nextStatus.port}`);
        void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
      }
      setStatus(nextStatus);
    };

    void aoBridge.daemon.getStatus().then((nextStatus) => {
      if (!active) return;
      applyStatus(nextStatus);
      stopTransport = createEventTransport(queryClient).connect();
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
