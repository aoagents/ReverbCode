import { useEffect, useState } from "react";
import type { QueryClient } from "@tanstack/react-query";
import { aoBridge } from "../lib/bridge";
import { queryClient as defaultQueryClient } from "../lib/query-client";
import { createEventTransport } from "../lib/event-transport";

type DaemonStatus = Awaited<ReturnType<typeof aoBridge.daemon.getStatus>>;

export function useDaemonStatus(queryClient: QueryClient = defaultQueryClient) {
  const [status, setStatus] = useState<DaemonStatus>({ state: "stopped" });

  useEffect(() => {
    let active = true;

    void aoBridge.daemon.getStatus().then((nextStatus) => {
      if (active) setStatus(nextStatus);
    });

    const stopTransport = createEventTransport(queryClient).connect();
    const stopStatusListener = aoBridge.daemon.onStatus(setStatus);

    return () => {
      active = false;
      stopTransport();
      stopStatusListener();
    };
  }, [queryClient]);

  return status;
}
